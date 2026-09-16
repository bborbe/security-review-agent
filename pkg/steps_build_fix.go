// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// defaultMaxExtractedLines bounds the failure excerpt written into the task.
// A package constant, not a config knob.
const defaultMaxExtractedLines = 20

// failureMarkers are the case-insensitive substrings that mark a log line as
// error-shaped. Package-level so there is one list to extend.
var failureMarkers = []string{
	"error",
	"failed",
	"failure",
	"panic:",
	"exit status",
	"not found",
	"cannot",
	"no such file",
}

// ExtractFailureCause returns the failure excerpt from a Cloud Build log.
// maxLines bounds the excerpt; a value below 1 is treated as 1.
//
// The failure is the LAST error the log reports: a build log carries many
// earlier error-shaped lines (retried steps, `exit status 1` from a probe)
// before the fatal one, so anchoring on the first match reports the wrong
// cause. When the log carries no marker at all, the last maxLines non-empty
// lines are returned instead. An empty log returns ("", nil) — never an error;
// the caller decides what an empty analysis means.
func ExtractFailureCause(_ context.Context, log string, maxLines int) (string, error) {
	if maxLines < 1 {
		maxLines = 1
	}
	lines := splitLogLines(log)
	if len(lines) == 0 {
		return "", nil
	}
	excerpt := failureExcerpt(lines, maxLines)
	if len(excerpt) == 0 {
		return "", nil
	}
	return sanitizeExcerpt(excerpt), nil
}

// splitLogLines splits a log into lines, normalizing CRLF. An all-whitespace
// log yields no lines.
func splitLogLines(log string) []string {
	if strings.TrimSpace(log) == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
}

// failureExcerpt returns at most maxLines lines ending at the last
// error-marked line, or the last maxLines non-empty lines when the log carries
// no marker. Clipping happens at the front so the fatal line survives.
func failureExcerpt(lines []string, maxLines int) []string {
	if last := lastFailureLine(lines); last >= 0 {
		return tailWindow(lines, last, maxLines)
	}
	nonEmpty := nonEmptyLines(lines)
	if len(nonEmpty) == 0 {
		return nil
	}
	return tailWindow(nonEmpty, len(nonEmpty)-1, maxLines)
}

// lastFailureLine returns the index of the last error-marked line, or -1 when
// no line carries a marker.
func lastFailureLine(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if hasFailureMarker(lines[i]) {
			return i
		}
	}
	return -1
}

// hasFailureMarker reports whether the line carries any case-insensitive
// failure marker.
func hasFailureMarker(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range failureMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// tailWindow returns the at-most-maxLines slice of lines ending at end
// (inclusive). Out-of-range ends clamp to the start of the slice.
func tailWindow(lines []string, end, maxLines int) []string {
	if end < 0 {
		return nil
	}
	start := end - maxLines + 1
	if start < 0 {
		start = 0
	}
	return lines[start : end+1]
}

// nonEmptyLines filters out whitespace-only lines.
func nonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// sanitizeExcerpt escapes a leading '#' on every excerpt line. agentlib
// ParseMarkdown treats any line beginning with "# " or "## " as a section
// heading regardless of fences, and a Go/cgo build log legitimately contains
// lines like "# github.com/foo/bar" — without the escape, re-parsing the
// enriched task would split "## Failure Analysis" apart.
func sanitizeExcerpt(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			line = " " + line
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// buildFixStep is the single, code-only step of the build-fix task type. It
// reads a failed Cloud Build's log, extracts the failure cause, and enriches
// the task in place — then unassigns it so a human picks it up. No Claude, no
// clone, no code modification, no PR: v1 is analysis only.
type buildFixStep struct {
	fetcher           BuildLogFetcher
	cloudBuildProject string
	cloudBuildRegion  string
	maxLines          int
}

// NewBuildFixStep constructs the single, code-only build-fix step. The project
// and region come from configuration (the task frontmatter carries neither).
func NewBuildFixStep(cloudBuildProject, cloudBuildRegion string) agentlib.Step {
	return newBuildFixStep(
		NewCloudBuildLogFetcher(),
		cloudBuildProject,
		cloudBuildRegion,
		defaultMaxExtractedLines,
	)
}

// newBuildFixStep is the concrete constructor — exposed for tests
// (export_test.go) so they can inject a fake fetcher and a small excerpt cap.
func newBuildFixStep(
	fetcher BuildLogFetcher,
	cloudBuildProject, cloudBuildRegion string,
	maxLines int,
) *buildFixStep {
	return &buildFixStep{
		fetcher:           fetcher,
		cloudBuildProject: cloudBuildProject,
		cloudBuildRegion:  cloudBuildRegion,
		maxLines:          maxLines,
	}
}

// Name implements agentlib.Step.
func (s *buildFixStep) Name() string { return "build-fix" }

// ShouldRun always returns true — the analysis is idempotent because
// ReplaceSection overwrites the previous ## Failure Analysis.
func (s *buildFixStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run fetches the failed build's log, extracts the failure cause, writes it
// into the task's ## Failure Analysis section, and clears the assignee so the
// task escalates to a human. It never advances to done and never fixes the
// build.
func (s *buildFixStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	buildID, ok := md.Frontmatter.String("build_id")
	if !ok || buildID == "" {
		return needsInput(
			"build-fix: build_id missing from task frontmatter — cannot fetch the build log",
		), nil
	}

	logText, partial, err := s.fetchLog(ctx, buildID)
	if err != nil {
		return failed(fmt.Sprintf("build-fix: %v", err)), nil
	}

	cause, err := ExtractFailureCause(ctx, logText, s.maxLines)
	if err != nil {
		return failed(fmt.Sprintf(
			"build-fix: extract failure cause for build %s: %v", buildID, err,
		)), nil
	}

	dashboard, _ := md.Frontmatter.String("dashboard")
	md.ReplaceSection(agentlib.Section{
		Heading: "## Failure Analysis",
		Body:    buildFailureAnalysisBody(buildID, dashboard, cause, partial),
	})
	// Clearing assignee IS the escalation: in this fleet `assignee` names which
	// agent handles the task, so an empty value hands it to a human. The
	// framework records the pre-clear owner as previous_assignee on its own write.
	md.Frontmatter["assignee"] = ""

	glog.V(2).Infof("build-fix: enriched task build_id=%s partial=%t", buildID, partial)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "human_review",
		Message: fmt.Sprintf(
			"build-fix: build %s analyzed — %s", buildID, firstLine(cause),
		),
	}, nil
}

// fetchLog fetches the build log. A fetch error with an empty log is transient
// and returned as an error (the controller retries); a fetch error with a
// non-empty log is partial and the analysis proceeds on what was returned.
func (s *buildFixStep) fetchLog(ctx context.Context, buildID string) (string, bool, error) {
	logText, err := s.fetcher.FetchLog(ctx, BuildRef{
		BuildID: buildID,
		Project: s.cloudBuildProject,
		Region:  s.cloudBuildRegion,
	})
	if err == nil {
		return logText, false, nil
	}
	if logText == "" {
		return "", false, errors.Wrapf(ctx, err, "fetch log for build %s", buildID)
	}
	return logText, true, nil
}

// buildFailureAnalysisBody renders the ## Failure Analysis section body. The
// body is deterministic — no timestamps — so the same log yields the same
// analysis.
func buildFailureAnalysisBody(buildID, dashboard, cause string, partial bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Build `%s`", buildID)
	if dashboard != "" {
		fmt.Fprintf(&b, " (dashboard `%s`)", dashboard)
	}
	b.WriteString(" failed.\n\n")
	if partial {
		b.WriteString("_The log fetch was partial — the excerpt below may be incomplete._\n\n")
	}
	if cause == "" {
		b.WriteString("No failure excerpt could be extracted from the build log.\n")
		return b.String()
	}
	b.WriteString("```text\n")
	b.WriteString(cause)
	if !strings.HasSuffix(cause, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// firstLine returns the first line of s — used for the one-line result message.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
