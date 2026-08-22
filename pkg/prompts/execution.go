// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
)

//go:embed execution_output-format.md
var executionOutputFormat string

const prefilledArgsHeaderTemplate = "## Pre-filled arguments\n\n" +
	"The procedure below describes a `/coding:pr-review` slash command that takes\n" +
	"`<target-branch>` and a mode argument. Those arguments have already been\n" +
	"resolved for this run — do NOT prompt for them, do NOT re-derive them:\n\n" +
	"- **TARGET_BRANCH**: %s\n" +
	"- **mode**: %s\n" +
	"- **Working directory**: your cwd is already the cloned worktree at the PR head — " +
	"run plain `git` from it, never `git -C <path>`; `git -C` is not needed.\n\n" +
	"Now follow the procedure below as if the slash command had been invoked with\n" +
	"those arguments. The procedure references sub-agents via the `Task` tool;\n" +
	"dispatch them as written.\n\n" +
	"---\n\n"

// funnelInjectSteerTemplate is prepended to the inlined /coding:pr-review
// procedure when the agent has ALREADY run the mechanical funnel (the normal
// path). The review runs non-interactively under a fixed --allowedTools
// allowlist (see factory.executionTools); the funnel runner is deliberately NOT
// on it — a weak model wraps the invocation in forms the allowlist can't match
// (`> redirect`, `bash -c`, `$RUNNER`), gets denied, and silently drops the
// mechanical MUST-tier pass. So the agent runs the funnel in Go and injects its
// JSON here; the model must consume it, not re-run the runner.
//
// IMPORTANT (2026-08-19, github-pr-reviewer false-positive incident): the
// funnel output is deliberately OVER-INCLUSIVE — several rules (main-test-with-
// compiles, secret-fields-need-display-length, slog-not-glog-in-new-projects,
// external-call-logs-response, counterfeiter-directive-on-interface) fire on
// every candidate and rely on the adjudicator to confirm the actual condition
// (file exists, tag present, directive present, project age). Earlier wording
// ("treat every finding as a confirmed MUST-tier finding, do NOT second-guess")
// suppressed that adjudication and caused present code to be re-reported as
// missing every round. The model MUST verify each finding against the actual
// file content in the worktree before reporting it; findings it cannot confirm
// are dropped, not escalated.
// %[1]s = plugin root, %[2]s = funnel findings JSON.
const funnelInjectSteerTemplate = "## Pre-computed mechanical funnel + tool paths (non-interactive run)\n\n" +
	"This review runs headless under a fixed tool allowlist. Two things are handled for you:\n\n" +
	"1. **Mechanical funnel (Step 4a) — ALREADY RUN.** The agent executed the ast-grep " +
	"mechanical funnel over this PR's changed files before invoking you; its JSON output " +
	"is below. Do NOT run `ast-grep-runner.sh` yourself — it is not on the allowlist and " +
	"the call will be denied. The funnel is intentionally OVER-INCLUSIVE: it flags every " +
	"candidate so nothing is missed, and the ast-grep relation layer cannot see comment " +
	"blocks above an interface or whether a companion file exists. VERIFY EACH FINDING " +
	"against the actual file in the checked-out worktree (you have read access) before " +
	"reporting it: confirm the flagged code really is present and the claimed condition " +
	"really is missing (directive absent, main_test.go absent, display:length tag absent, " +
	"log line absent, project genuinely new). Report ONLY findings you have confirmed; " +
	"drop any finding the real file refutes. Never escalate a finding you did not verify.\n\n" +
	"```json\n%[2]s\n```\n\n" +
	"2. **Selector-mode guide (Step 4c-sel)** is always present — skip the " +
	"`GUIDE_OK`/`GUIDE_MISSING` probe and Read `%[1]s/docs/selector-mode-guide.md` directly.\n\n" +
	"---\n\n"

// funnelFailedSteerTemplate is used when the agent's own funnel run failed
// (runner missing, tooling exit, or changed-file computation failed). Fail
// closed: the model must surface the gap and must NOT approve as though the
// mechanical pass had succeeded. %[1]s = plugin root, %[2]s = failure detail.
const funnelFailedSteerTemplate = "## Mechanical funnel status + tool paths (non-interactive run)\n\n" +
	"This review runs headless under a fixed tool allowlist. Note:\n\n" +
	"1. **Mechanical funnel (Step 4a) — COULD NOT RUN.** The agent attempted the ast-grep " +
	"mechanical funnel before invoking you, but it failed: %[2]s. You have NO machine-" +
	"verified MUST-tier result, and you must NOT run the runner yourself (not on the " +
	"allowlist). You MUST state prominently in your review `summary` that the mechanical " +
	"MUST-tier check was UNAVAILABLE, and you MUST NOT post a clean `approve` as though it " +
	"had passed — treat the missing mechanical pass as a blocking gap (verdict " +
	"`request-changes`) unless the diff is trivially safe (e.g. docs-only).\n\n" +
	"2. **Selector-mode guide (Step 4c-sel)** is always present — skip the probe and Read " +
	"`%[1]s/docs/selector-mode-guide.md` directly.\n\n" +
	"---\n\n"

const verdictTranslationFooter = "---\n\n" +
	"## Final step — emit verdict JSON\n\n" +
	"After Step 7 (Manual Review) completes and the consolidated report is\n" +
	"produced, ALSO emit a JSON verdict matching the agent's frozen schema (see\n" +
	"`<output-format>`).\n\n" +
	"Severity map (deterministic):\n" +
	"- Must Fix finding → comment severity \"critical\", contributes to verdict \"request-changes\"\n" +
	"- Should Fix finding → comment severity \"major\", contributes to verdict \"request-changes\"\n" +
	"- Nice to Have finding → comment severity \"nit\"\n" +
	"- The severity \"minor\" is reserved for LLM judgment on findings that\n" +
	"  genuinely don't fit a plugin bucket; the deterministic map never emits it.\n\n" +
	"Verdict roll-up (binary — exactly one of two values):\n" +
	"- Any Must Fix present → verdict \"request-changes\"\n" +
	"- Any Should Fix present → verdict \"request-changes\"\n" +
	"- Only Nice to Have, or nothing flagged → verdict \"approve\"\n\n" +
	"Each comment must pin to a real `file` and `line` from the report. If a\n" +
	"finding has no coordinates, fold it into `summary` instead of emitting an\n" +
	"un-pinned comment. Preserve the plugin's bucket label verbatim in the\n" +
	"comment `message` for traceability.\n"

// timeBudgetFooter is appended to the assembled execution instructions when the
// agent enforces the soft REVIEW_MAX_DURATION budget. It tells the model to stop
// investigating at the budget and write the verdict from what it already knows,
// explicitly flagging every ## Plan concern it could not examine. %s = budget.
const timeBudgetFooter = "---\n\n" +
	"## Time budget\n\n" +
	"This run has a soft time budget of %s. When the budget is reached, STOP " +
	"investigating immediately and write the verdict from what you already know — " +
	"do not keep going until the job kills the run.\n\n" +
	"Disposition EVERY concern in `## Plan` inside `concerns_addressed` with one of:\n" +
	"- `addressed` — a code change or a comment resolves it\n" +
	"- `not an issue` — you examined it and confirmed it is a non-issue\n" +
	"- `not verified` — the time budget stopped you before you could examine it\n\n" +
	"A concern listed as `not verified` means the review is incomplete: the verdict " +
	"will be fail-closed to request-changes and the partial output is salvaged for a " +
	"human. Never silently drop a concern because investigation ended.\n\n"

// BuildExecutionInstructions assembles the execution-phase prompt by reading
// the /coding:pr-review plugin file at runtime, stripping its YAML frontmatter,
// prepending a pre-filled-arguments header, and appending a verdict-translation
// footer so the inlined plugin procedure runs as native instructions.
//
// The agent runs the mechanical funnel itself and passes its outcome in:
// funnelRan true injects the authoritative findings JSON (model must consume,
// not re-run); funnelRan false injects a fail-closed status carrying
// funnelFailDetail so the review surfaces the gap instead of silently approving.
// maxDuration is the soft REVIEW_MAX_DURATION budget appended as the time-budget
// wrap-up contract so the model stops investigating at the budget and flags any
// ## Plan concern it could not examine as `not verified`.
func BuildExecutionInstructions(
	ctx context.Context,
	claudeConfigDir claudelib.ClaudeConfigDir,
	reviewMode string,
	baseRef string,
	funnelRan bool,
	funnelFindings string,
	funnelFailDetail string,
	maxDuration libtime.Duration,
) (claudelib.Instructions, error) {
	if baseRef == "" {
		return nil, errors.New(ctx, "base_ref is empty")
	}
	if reviewMode == "" {
		return nil, errors.New(ctx, "reviewMode is empty")
	}

	pluginPath := filepath.Join(
		string(claudeConfigDir),
		"plugins",
		"marketplaces",
		"coding",
		"commands",
		"pr-review.md",
	)
	raw, err := os.ReadFile(pluginPath) // #nosec G304 -- path constructed from trusted config dir
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read plugin command file path=%s", pluginPath)
	}

	pluginRoot := filepath.Join(
		string(claudeConfigDir),
		"plugins",
		"marketplaces",
		"coding",
	)
	header := fmt.Sprintf(prefilledArgsHeaderTemplate, baseRef, reviewMode)
	var steer string
	if funnelRan {
		steer = fmt.Sprintf(funnelInjectSteerTemplate, pluginRoot, funnelFindings)
	} else {
		steer = fmt.Sprintf(funnelFailedSteerTemplate, pluginRoot, funnelFailDetail)
	}
	assembled := header + steer + stripFrontmatter(string(raw)) + verdictTranslationFooter +
		fmt.Sprintf(timeBudgetFooter, maxDuration)
	return claudelib.Instructions{
		{Name: "workflow", Content: assembled},
		{Name: "output-format", Content: executionOutputFormat},
	}, nil
}

// stripFrontmatter removes a leading YAML frontmatter block delimited by
// "---\n" ... "\n---\n". If no leading frontmatter is present, the input
// is returned unchanged.
func stripFrontmatter(s string) string {
	const delim = "---\n"
	if !strings.HasPrefix(s, delim) {
		return s
	}
	rest := s[len(delim):]
	end := strings.Index(rest, "\n"+delim)
	if end < 0 {
		return s
	}
	return rest[end+len("\n"+delim):]
}
