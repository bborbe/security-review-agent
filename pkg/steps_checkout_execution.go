// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/prompts"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	prurl "github.com/bborbe/maintainer/prurl"
	repoallowlist "github.com/bborbe/maintainer/repoallowlist"
)

// githubPRURLPattern matches a GitHub PR URL in arbitrary text.
var githubPRURLPattern = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/pull/\d+`)

// checkoutExecutionStep is the execution phase step that checks out the
// target ref as an on-disk worktree and runs Claude against the real files.
type checkoutExecutionStep struct {
	repoManager     git.RepoManager
	claudeConfigDir claudelib.ClaudeConfigDir
	agentDir        claudelib.AgentDir
	model           claudelib.ClaudeModel
	env             map[string]string
	allowedTools    claudelib.AllowedTools
	reviewMode      string
	repoAllowlist   []string
	prPoster        PrPoster // nil = skip posting
	funnelRunner    FunnelRunner
	currentDateTime libtime.CurrentDateTimeGetter
	runner          claudelib.ClaudeRunner // nil = build a fresh runner in runClaude (production)
	maxDuration     libtime.Duration       // soft REVIEW_MAX_DURATION budget per claude run
}

// NewCheckoutExecutionStep constructs the execution-phase step that wires
// RepoManager checkout into the Claude runner working directory. runner is
// nil in production (runClaude builds a fresh ClaudeRunner); tests inject a
// fake. maxDuration is the soft REVIEW_MAX_DURATION budget enforced on the
// claude run; expiry routes to human_review.
func NewCheckoutExecutionStep(
	repoManager git.RepoManager,
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	env map[string]string,
	allowedTools claudelib.AllowedTools,
	reviewMode string,
	repoAllowlist []string,
	prPoster PrPoster,
	funnelRunner FunnelRunner,
	currentDateTime libtime.CurrentDateTimeGetter,
	runner claudelib.ClaudeRunner,
	maxDuration libtime.Duration,
) agentlib.Step {
	return &checkoutExecutionStep{
		repoManager:     repoManager,
		claudeConfigDir: claudeConfigDir,
		agentDir:        agentDir,
		model:           model,
		env:             env,
		allowedTools:    allowedTools,
		reviewMode:      reviewMode,
		repoAllowlist:   repoAllowlist,
		prPoster:        prPoster,
		funnelRunner:    funnelRunner,
		currentDateTime: currentDateTime,
		runner:          runner,
		maxDuration:     maxDuration,
	}
}

// Name implements agentlib.Step.
func (s *checkoutExecutionStep) Name() string { return "pr-execute" }

// ShouldRun always returns true. Idempotency for the "## Review already
// present" case is enforced inside Run (skip clone+claude, publish
// NextPhase=ai_review). Returning false here would skip the routing too and
// the phase would silently short-circuit to done — same failure mode as the
// trading#136 incident in planning.
func (s *checkoutExecutionStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// advanceIfAlreadyReviewed returns a Done+NextPhase=ai_review result when a
// previous trigger already produced ## Review (typical retrigger case after
// the controller resets trigger_count). nil means "no shortcut — do the full
// clone+claude+post flow".
func (s *checkoutExecutionStep) advanceIfAlreadyReviewed(md *agentlib.Markdown) *agentlib.Result {
	if _, exists := md.FindSection("## Review"); !exists {
		return nil
	}
	glog.V(2).Infof("execution: ## Review already present — advancing to ai_review")
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "ai_review",
	}
}

// Run handles two paths:
//   - ## Review already present → publish NextPhase=ai_review without
//     re-cloning or re-running claude (the previous trigger already produced
//     the review body; advance the phase so ai_review consumes it).
//   - ## Review missing → clone the worktree, run claude in it, write
//     ## Review, post the review comment, route.
func (s *checkoutExecutionStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	if result := s.advanceIfAlreadyReviewed(md); result != nil {
		return result, nil
	}

	cloneURL, ref, taskID, baseRef, missingResult := extractRequiredFrontmatter(md)
	if missingResult != nil {
		return missingResult, nil
	}

	// Pre-parse clone_url to extract host/owner/repo for allowlist and
	// auth-failure diagnostics. A parse failure is a hard error — the URL is
	// malformed and no clone can proceed.
	parts, parseErr := git.ParseCloneURLParts(ctx, cloneURL)
	if parseErr != nil {
		return &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: fmt.Sprintf("execution step: failed to parse clone_url: %v", parseErr),
		}, nil
	}
	repoKey := parts.Host + "/" + parts.Owner + "/" + parts.Repo

	if result := s.checkAllowlist(ctx, cloneURL); result != nil {
		return result, nil
	}

	worktreePath, err := s.repoManager.EnsureWorktree(ctx, cloneURL, ref, taskID)
	if err != nil {
		if git.IsGitAuthFailure(err) {
			// Underlying git error is intentionally NOT included in the diagnostic
			// (it could in theory echo credential-bearing strings). Operators dig
			// into pod logs at glog v(2) for the raw git stderr.
			glog.V(2).
				Infof("clone auth failure repo=%s ref=%s task_id=%s err=%v", repoKey, ref, taskID, err)
			return &agentlib.Result{
				Status: agentlib.AgentStatusNeedsInput,
				Message: fmt.Sprintf(
					"execution step: clone failed for %s: authentication required (set GH_TOKEN and re-trigger)",
					repoKey,
				),
			}, nil
		}
		return nil, errors.Wrapf(
			ctx,
			err,
			"ensure worktree repo=%s ref=%s task_id=%s",
			repoKey,
			ref,
			taskID,
		)
	}

	// Run the deterministic mechanical funnel ourselves and inject its findings
	// into the execution prompt. The review model must never be relied on to
	// invoke the funnel — under the fixed allowlist it wraps the runner in
	// non-matching forms, gets denied, and silently drops the MUST-tier pass.
	var funnel FunnelResult
	if s.funnelRunner != nil {
		funnel, err = s.funnelRunner.Run(ctx, worktreePath, baseRef)
		if err != nil {
			return nil, errors.Wrapf(ctx, err, "run mechanical funnel base_ref=%s", baseRef)
		}
	}

	instructions, err := prompts.BuildExecutionInstructions(
		ctx,
		s.claudeConfigDir,
		s.reviewMode,
		baseRef,
		funnel.Ran,
		funnel.FindingsJSON,
		funnel.FailDetail,
		s.maxDuration,
	)
	if err != nil {
		return nil, errors.Wrapf(
			ctx,
			err,
			"build execution instructions base_ref=%s mode=%s",
			baseRef,
			s.reviewMode,
		)
	}

	return s.runClaude(ctx, md, worktreePath, instructions, funnel.Ran)
}

// checkAllowlist returns a non-nil Result if the cloneURL is blocked by the
// allowlist or fails to parse. Returns nil when the clone is permitted.
// Must be called before EnsureWorktree — cloning is the trust boundary.
func (s *checkoutExecutionStep) checkAllowlist(
	ctx context.Context,
	cloneURL string,
) *agentlib.Result {
	if len(s.repoAllowlist) == 0 {
		return nil
	}
	parts, parseErr := git.ParseCloneURLParts(ctx, cloneURL)
	if parseErr != nil {
		return &agentlib.Result{
			Status: agentlib.AgentStatusFailed,
			Message: fmt.Sprintf(
				"execution step: failed to parse clone_url for allowlist check: %v",
				parseErr,
			),
		}
	}
	repoKey := parts.Host + "/" + parts.Owner + "/" + parts.Repo
	if repoallowlist.IsAllowed(s.repoAllowlist, repoKey) {
		return nil
	}
	return &agentlib.Result{
		Status: agentlib.AgentStatusNeedsInput,
		Message: fmt.Sprintf(
			"execution step: repo %q is not on the allowlist (%d entries); task routed to human review without clone",
			repoKey,
			len(s.repoAllowlist),
		),
	}
}

// ExtractPRURL scans md.Preamble plus every section before the first "## "
// (H2) heading for a GitHub PR URL. agentlib splits sections at both "# " (H1)
// and "## " (H2), so when the watcher writes the task body as:
//
//	# PR Review: <title>
//	<PR URL>
//	## Plan
//
// md.Preamble is always empty and the URL sits inside the H1 section body.
// Stopping at the first H2 prevents matching URLs Claude writes inside ## Review.
func ExtractPRURL(md *agentlib.Markdown) string {
	if u := githubPRURLPattern.FindString(md.Preamble); u != "" {
		return u
	}
	for _, sec := range md.Sections {
		if strings.HasPrefix(sec.Heading, "## ") {
			break // stop at first H2 — Claude-authored body starts here
		}
		if u := githubPRURLPattern.FindString(sec.Heading + "\n" + sec.Body); u != "" {
			return u
		}
	}
	return ""
}

func (s *checkoutExecutionStep) runClaude(
	ctx context.Context,
	md *agentlib.Markdown,
	worktreePath string,
	instructions claudelib.Instructions,
	funnelRan bool,
) (*agentlib.Result, error) {
	// Cache PR URL BEFORE any md mutations to avoid matching URLs that Claude
	// writes inside the ## Review section body.
	prURLStr := ExtractPRURL(md)

	runner := s.runner
	if runner == nil {
		runner = claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
			ClaudeConfigDir:  s.claudeConfigDir,
			AllowedTools:     s.allowedTools,
			Model:            s.model,
			WorkingDirectory: claudelib.AgentDir(worktreePath),
			Env:              s.env,
		})
	}

	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "execution marshal task")
	}

	prompt := claudelib.BuildPrompt(instructions.String(), nil, taskContent)
	runResult, runErr, budgetExpired := runWithSoftBudget(ctx, runner, prompt, s.maxDuration)
	if runErr != nil {
		// Budget expiry (a FIRED run-context deadline) routes to human_review
		// BEFORE ## Review is written or the review posted — never retried here.
		// The streamed partial (if any) is salvaged under ## Salvage so the run
		// never dies with a blank task; a salvaged partial is never posted.
		if budgetExpired {
			glog.V(2).
				Infof("execution: soft time budget %s exceeded nextPhase=human_review", s.maxDuration)
			writeSalvage(md, ExtractBudgetPartial(runResult, runErr))
			return budgetExpiredResult("execution", s.maxDuration), nil
		}
		return &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: fmt.Sprintf("execution claude run failed: %v", runErr),
		}, nil
	}

	// Vault-first invariant: write ## Review BEFORE any API call.
	md.ReplaceSection(agentlib.Section{
		Heading: "## Review",
		Body:    runResult.Result,
	})

	return s.postAndRoute(
		ctx,
		md,
		prURLStr,
		worktreePath,
		time.Time(s.currentDateTime.Now()),
		funnelRan,
	)
}

// postAndRoute handles the posting sequence after ## Review has been written to
// the vault. Extracted for testability — tests call this directly without Claude.
func (s *checkoutExecutionStep) postAndRoute(
	ctx context.Context,
	md *agentlib.Markdown,
	prURLStr string,
	worktreePath string,
	jobRunTime time.Time,
	funnelRan bool,
) (*agentlib.Result, error) {
	// nil poster = skip posting (cmd/run-task with --skip-post).
	if s.prPoster == nil {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "ai_review",
		}, nil
	}

	// Extract verdict + summary from ## Review (already written to vault).
	var reviewBody string
	if reviewSection, ok := md.FindSection("## Review"); ok && reviewSection != nil {
		reviewBody = reviewSection.Body
	}
	verdict := ParseVerdict(reviewBody)
	summary := StripJSONVerdict(reviewBody)

	// Fail-closed gate: the Go-side mechanical funnel could not run, so the model
	// had NO machine-verified MUST-tier result. An `approve` here is unverified —
	// override it to request-changes in code rather than trust the model to have
	// self-gated per the funnel-failed prompt (self-gating on a weak model is the
	// exact silent-approve this agent's Go-side funnel exists to prevent). Mirrors
	// the unparseable-verdict fail-close below; request-changes verdicts already
	// produced by the model are left untouched.
	if !funnelRan && verdict.Verdict == VerdictApprove {
		verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonFunnelDidNotRun}
	}
	// Fail-closed gate: the review flags one or more ## Plan concerns as
	// `not verified` — the model stopped investigating at the soft time budget
	// before examining them — yet still emits approve. Override to
	// request-changes so an incomplete review can never green-light a PR; the
	// partial output is salvaged for a human. Fires only when the funnel gate
	// above did not already demote the verdict (i.e. the funnel ran) — an
	// unverified concern is the more specific diagnosis and keeps its reason.
	if verdict.Verdict == VerdictApprove && HasUnverifiedConcerns(reviewBody) {
		verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonConcernsNotVerified}
	}

	// Diagnostic for the recurring false-CHANGES_REQUESTED symptom: a
	// request-changes verdict produced by ParseVerdict fail-closing (empty /
	// unparseable / no-verdict-block) on a review whose posted body looks like a
	// clean approve. The raw reviewBody the parser saw is lost once this Job's
	// pod is GC'd, so surface the reason + length + tail here — the next
	// recurrence is then self-diagnosing from pod logs alone. Legitimate
	// request-changes verdicts (model-authored reason) do NOT log.
	if verdict.Verdict == VerdictRequestChanges && isFailClosedReason(verdict.Reason) {
		glog.Warningf(
			"pr-reviewer fail-closed to request-changes: reason=%q reviewBody_len=%d reviewBody_tail=%q",
			verdict.Reason,
			len(reviewBody),
			lastChars(reviewBody, 300),
		)
	}

	prInfo, earlyResult := s.resolvePRInfo(ctx, md, prURLStr, jobRunTime)
	if earlyResult != nil {
		return earlyResult, nil
	}

	// Get head SHA from frontmatter.
	ref, _ := md.Frontmatter.String("ref")

	// Post the review.
	result := s.prPoster.Post(ctx, PostRequest{
		PR:      *prInfo,
		HeadSHA: ref,
		Verdict: verdict.Verdict,
		Summary: summary,
		WorkDir: worktreePath,
	})

	// Always append diagnostic block — one entry per Job run, append-only.
	appendDiagnosticsSection(
		md,
		buildDiagnosticBlock(jobRunTime, md.Frontmatter.TriggerCount(), result),
	)

	// Route based on outcome.
	if result.Outcome == "success" || result.Class == ErrorClassNotAFailure {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "ai_review",
		}, nil
	}

	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "human_review",
		Message:   "posting failed: " + result.ErrorMessage,
	}, nil
}

// resolvePRInfo validates and parses the PR URL, writes diagnostics on failure,
// and returns either a parsed PRInfo or an early result to return from postAndRoute.
func (s *checkoutExecutionStep) resolvePRInfo(
	ctx context.Context,
	md *agentlib.Markdown,
	prURLStr string,
	jobRunTime time.Time,
) (*prurl.PRInfo, *agentlib.Result) {
	tc := md.Frontmatter.TriggerCount()
	if prURLStr == "" {
		appendDiagnosticsSection(md, buildDiagnosticBlock(jobRunTime, tc, PostResult{
			Outcome:      "failed",
			FailureStep:  "pr_url_extraction",
			Class:        ErrorClassPermanent,
			ErrorMessage: "no GitHub PR URL found in task preamble",
		}))
		return nil, &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: "posting skipped: no GitHub PR URL found in task preamble",
		}
	}

	prInfo, parseErr := prurl.ParsePRURL(ctx, prURLStr)
	if parseErr != nil {
		appendDiagnosticsSection(md, buildDiagnosticBlock(jobRunTime, tc, PostResult{
			Outcome:      "failed",
			FailureStep:  "pr_url_parse",
			Class:        ErrorClassPermanent,
			ErrorMessage: fmt.Sprintf("failed to parse PR URL %q: %v", prURLStr, parseErr),
		}))
		return nil, &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: fmt.Sprintf("posting skipped: failed to parse PR URL: %v", parseErr),
		}
	}

	if prInfo.Platform != prurl.PlatformGitHub {
		glog.Warningf(
			"posting skipped: non-GitHub platform %q for URL %q",
			prInfo.Platform,
			prURLStr,
		)
		appendDiagnosticsSection(md, buildDiagnosticBlock(jobRunTime, tc, PostResult{
			Outcome:     "failed",
			FailureStep: "pr_url_platform",
			Class:       ErrorClassPermanent,
			ErrorMessage: fmt.Sprintf(
				"non-GitHub platform %q is out of scope for posting",
				prInfo.Platform,
			),
		}))
		return nil, &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "ai_review",
		}
	}

	return prInfo, nil
}

// appendDiagnosticsSection appends block to the ## Diagnostics section (creates
// it if absent). Append-only — one block per Job run preserves history.
func appendDiagnosticsSection(md *agentlib.Markdown, block string) {
	var existingBody string
	if existing, ok := md.FindSection("## Diagnostics"); ok && existing != nil {
		existingBody = existing.Body
	}
	newBody := strings.TrimLeft(existingBody+"\n"+block, "\n")
	md.ReplaceSection(agentlib.Section{Heading: "## Diagnostics", Body: newBody})
}

// buildDiagnosticBlock formats one posting-attempt entry for ## Diagnostics.
// Success emits a compact one-liner; failure emits a fenced YAML block.
func buildDiagnosticBlock(jobRunTime time.Time, triggerCount int, result PostResult) string {
	if result.Outcome == "success" {
		return fmt.Sprintf("job_run: %s outcome: success review_id: %d\n",
			jobRunTime.UTC().Format(time.RFC3339), result.ReviewID)
	}
	httpStatusStr := "null"
	if result.HTTPStatus != 0 {
		httpStatusStr = fmt.Sprintf("%d", result.HTTPStatus)
	}
	respBody := result.ResponseBody
	if respBody == "" {
		respBody = "<empty>"
	}
	return fmt.Sprintf(
		"```yaml\njob_run: %s\ntrigger_count: %d\noutcome: failed\nfailure_step: %s\nclass: %s\nescalate_hint: %v\nattempt: %d\nhttp_status: %s\nerror_message: %q\nresponse_body: %q\nelapsed_ms: %d\n```\n",
		jobRunTime.UTC().Format(time.RFC3339),
		triggerCount,
		result.FailureStep,
		result.Class,
		result.EscalateHint,
		result.Attempt,
		httpStatusStr,
		result.ErrorMessage,
		respBody,
		result.ElapsedMs,
	)
}

// extractRequiredFrontmatter pulls the four required frontmatter fields for
// the execution phase and returns a Failed result when any is missing. The
// caller short-circuits with the result; nil means all fields are present.
func extractRequiredFrontmatter(
	md *agentlib.Markdown,
) (cloneURL, ref, taskID, baseRef string, missing *agentlib.Result) {
	cloneURL, _ = md.Frontmatter.String("clone_url")
	ref, _ = md.Frontmatter.String("ref")
	taskID, _ = md.Frontmatter.String("task_identifier")
	baseRef, _ = md.Frontmatter.String("base_ref")
	switch {
	case cloneURL == "":
		missing = &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: "execution step: clone_url is missing from task frontmatter",
		}
	case ref == "":
		missing = &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: "execution step: ref is missing from task frontmatter",
		}
	case baseRef == "":
		missing = &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: "execution step: base_ref is missing from task frontmatter",
		}
	}
	return cloneURL, ref, taskID, baseRef, missing
}
