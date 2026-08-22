// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// codeFenceRegexp matches a run of three or more backticks — a markdown code
// fence — used to neutralise fence sequences in PR-author-controlled finding text.
var codeFenceRegexp = regexp.MustCompile("`{3,}")

// FunnelResult is the outcome of running the ast-grep mechanical funnel.
type FunnelResult struct {
	// Ran is true when the runner executed cleanly and produced findings JSON.
	Ran bool
	// FindingsJSON is the runner's stdout (a single JSON object) when Ran is true.
	FindingsJSON string
	// FailDetail explains why the funnel could not run when Ran is false. The
	// execution prompt surfaces it as a fail-closed condition so a review can
	// never silently approve as though the mechanical pass had succeeded.
	FailDetail string
}

//counterfeiter:generate -o ../mocks/funnel-runner.go --fake-name FunnelRunner . FunnelRunner

// FunnelRunner runs the deterministic ast-grep mechanical funnel over the
// changed files of a checked-out worktree and returns its JSON findings.
//
// The agent runs the funnel itself rather than steering the review model to
// invoke it. A weak review model (e.g. MiniMax) wraps the runner in forms the
// execution allowlist cannot match (`> redirect`, `bash -c`, `$RUNNER`), gets
// denied, and silently falls back to a judgment-only review — dropping the
// entire MUST-tier mechanical pass. Running it in Go removes both the
// allowlist-vs-invocation-form fragility and the model's ability to skip it.
type FunnelRunner interface {
	Run(ctx context.Context, worktreePath string, baseRef string) (FunnelResult, error)
}

// funnelRunner is the production FunnelRunner. It resolves the operator-shipped
// ast-grep-runner.sh from the trusted CLAUDE_CONFIG_DIR and diff-scopes the scan
// to the PR's changed files.
type funnelRunner struct {
	claudeConfigDir claudelib.ClaudeConfigDir
}

// NewFunnelRunner constructs a FunnelRunner bound to a Claude config dir; the
// runner script is resolved beneath it (container: /home/claude/.claude,
// local cmd/run-task: ~/.claude).
func NewFunnelRunner(claudeConfigDir claudelib.ClaudeConfigDir) FunnelRunner {
	return &funnelRunner{claudeConfigDir: claudeConfigDir}
}

// runnerPath returns the resolved literal path of the coding plugin's funnel runner.
func (r *funnelRunner) runnerPath() string {
	return filepath.Join(
		string(r.claudeConfigDir),
		"plugins", "marketplaces", "coding", "scripts", "ast-grep-runner.sh",
	)
}

// Run diff-scopes to the PR's changed files and executes the funnel runner.
// It returns FunnelResult (never a bare error) for the two expected non-fatal
// outcomes — runner missing / non-zero tooling exit — so the caller can surface
// them as a fail-closed prompt condition. A non-nil error is reserved for
// unexpected Go-level failures (process could not be started, context cancel).
func (r *funnelRunner) Run(
	ctx context.Context,
	worktreePath string,
	baseRef string,
) (FunnelResult, error) {
	runner := r.runnerPath()
	if _, statErr := os.Stat(runner); statErr != nil {
		return FunnelResult{
			Ran:        false,
			FailDetail: "ast-grep runner script not found at " + runner,
		}, nil
	}

	files, filesErr := r.changedFiles(ctx, worktreePath, baseRef)
	if filesErr != nil {
		return FunnelResult{
			Ran:        false,
			FailDetail: "could not compute changed files for base_ref " + baseRef,
		}, nil
	}
	if len(files) == 0 {
		return FunnelResult{
			Ran:          true,
			FindingsJSON: `{"stats":{"yamls_run":0,"findings_count":0,"elapsed_ms":0},"findings_by_owner":{},"errors":[]}`,
		}, nil
	}

	args := append([]string{worktreePath}, files...)
	start := time.Now()
	// #nosec G204 -- runner is a fixed operator-shipped script beneath the trusted
	// CLAUDE_CONFIG_DIR; args are the target dir plus repo-relative changed-file
	// paths derived from git diff, never raw PR-author input.
	cmd := exec.CommandContext(ctx, runner, args...)
	cmd.Dir = worktreePath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	elapsedMs := time.Since(start).Milliseconds()

	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			glog.Warningf(
				"exec ast-grep-runner start-failed elapsed_ms=%d stderr=%q",
				elapsedMs, lastChars(stderr.String(), 300),
			)
			return FunnelResult{}, errors.Wrapf(ctx, runErr, "exec ast-grep runner %s", runner)
		}
		// Runner documents exit 2 = ast-grep/sg binary missing or usage error.
		glog.Warningf(
			"exec ast-grep-runner exit=%d elapsed_ms=%d files=%d stderr=%q",
			exitErr.ExitCode(), elapsedMs, len(files), lastChars(stderr.String(), 300),
		)
		return FunnelResult{
			Ran: false,
			FailDetail: "ast-grep runner exited non-zero (" +
				strings.TrimSpace(lastChars(stderr.String(), 200)) + ")",
		}, nil
	}

	glog.Infof("exec ast-grep-runner exit=0 elapsed_ms=%d files=%d", elapsedMs, len(files))

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return FunnelResult{Ran: false, FailDetail: "ast-grep runner produced no output"}, nil
	}
	// Defense-in-depth: the runner's findings carry PR-author-controlled strings
	// (matched_text / message copied from the diff), and the caller embeds this
	// JSON into the review prompt. Reject non-JSON output (a compromised or broken
	// runner) fail-closed, and neutralise code-fence sequences so a crafted PR
	// cannot break out of the prompt's ```json block and inject directives.
	if !json.Valid([]byte(out)) {
		return FunnelResult{
			Ran:        false,
			FailDetail: "ast-grep runner output was not valid JSON",
		}, nil
	}
	return FunnelResult{Ran: true, FindingsJSON: neutralizeCodeFences(out)}, nil
}

// neutralizeCodeFences replaces any run of three-or-more backticks with a
// visually-similar sequence that cannot close a markdown code fence, so
// PR-author-controlled finding text embedded in the execution prompt cannot
// escape its ```json block.
func neutralizeCodeFences(s string) string {
	return codeFenceRegexp.ReplaceAllString(s, "[code-fence]")
}

// changedFiles returns the PR's changed file paths (relative to worktreePath) by
// diffing HEAD against the base ref. The worktree is a --local clone of a mirror
// that carries all branches, so origin/<baseRef> is normally present; a
// best-effort fetch covers the case where it is not.
func (r *funnelRunner) changedFiles(
	ctx context.Context,
	worktreePath string,
	baseRef string,
) ([]string, error) {
	// Best-effort: make sure origin/<baseRef> exists locally. Ignore failure —
	// the diff below falls back to the bare ref name.
	_ = r.git(ctx, worktreePath, "fetch", "origin", baseRef+":refs/remotes/origin/"+baseRef)

	out, err := r.gitOutput(ctx, worktreePath, "diff", "--name-only", "origin/"+baseRef+"...HEAD")
	if err != nil {
		out, err = r.gitOutput(ctx, worktreePath, "diff", "--name-only", baseRef+"...HEAD")
		if err != nil {
			return nil, errors.Wrapf(ctx, err, "git diff --name-only base_ref=%s", baseRef)
		}
	}

	var files []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ".git/") || strings.Contains(line, "/.git/") {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// git runs a git subcommand in worktreePath, discarding stdout.
func (r *funnelRunner) git(ctx context.Context, worktreePath string, args ...string) error {
	// #nosec G204 -- git subcommand args are hardcoded verbs plus a validated base ref.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", worktreePath}, args...)...)
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(ctx, err, "git %s", args[0])
	}
	return nil
}

// gitOutput runs a git subcommand in worktreePath and returns trimmed stdout.
func (r *funnelRunner) gitOutput(
	ctx context.Context,
	worktreePath string,
	args ...string,
) (string, error) {
	// #nosec G204 -- git subcommand args are hardcoded verbs plus a validated base ref.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", worktreePath}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.Errorf(ctx, "git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
