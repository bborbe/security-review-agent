// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	prurl "github.com/bborbe/maintainer/prurl"
)

// verdictPayload is the parsed shape of the ## Verdict JSON the ai_review
// step writes. Only the fields needed for next-phase routing are typed
// here; the full payload stays in the markdown body for humans.
type verdictPayload struct {
	Verdict        string          `json:"verdict"`
	Reason         string          `json:"reason"`
	Hallucinations []Hallucination `json:"hallucinations"`
}

// reviewStep runs Claude on the task with the review-phase prompt, writes
// the LLM's response under ## Verdict, optionally verifies the in_progress
// post persisted on GitHub, parses verdict, and routes the next phase:
// pass → done, fail (or unparseable) → human_review.
type reviewStep struct {
	runner       claudelib.ClaudeRunner
	poster       PrPoster // nil = skip posting
	instructions claudelib.Instructions
	verifier     ReviewVerifier // nil = skip verification
	ghToken      string
	botLogin     string
	maxDuration  libtime.Duration // soft REVIEW_MAX_DURATION budget per claude run
}

// NewReviewStep constructs the ai_review-phase step. maxDuration is the soft
// REVIEW_MAX_DURATION budget enforced on the claude run; expiry routes to
// human_review without writing ## Verdict.
func NewReviewStep(
	runner claudelib.ClaudeRunner,
	poster PrPoster,
	instructions claudelib.Instructions,
	verifier ReviewVerifier,
	ghToken string,
	botLogin string,
	maxDuration libtime.Duration,
) agentlib.Step {
	return &reviewStep{
		runner:       runner,
		poster:       poster,
		instructions: instructions,
		verifier:     verifier,
		ghToken:      ghToken,
		botLogin:     botLogin,
		maxDuration:  maxDuration,
	}
}

// Name implements agentlib.Step.
func (s *reviewStep) Name() string { return "pr-ai-review" }

// ShouldRun always returns true. Idempotency for the "## Verdict already
// present" case is enforced inside Run (skip claude, publish NextPhase=done).
// Returning false here would skip the routing too and the phase would
// silently short-circuit — same failure mode as the trading#136 incident
// in planning.
func (s *reviewStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run handles two paths:
//   - ## Verdict already present → publish NextPhase=done without re-calling
//     claude (the previous trigger already produced the verdict; just close
//     out the task).
//   - ## Verdict missing → call claude with planning + review context,
//     write ## Verdict, verify the in_progress post, parse + route.
//
//nolint:funlen // the soft-budget expiry branch (incl. the salvage write) is required per-phase routing; extracting it hides the human_review-before-##-Verdict ordering AC 2 asserts. 83 lines, 3 over the cap.
func (s *reviewStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	if _, exists := md.FindSection("## Verdict"); exists {
		glog.V(2).Infof("ai-review: ## Verdict already present — advancing to done")
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "done",
		}, nil
	}

	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "ai-review marshal task")
	}

	prompt := claudelib.BuildPrompt(s.instructions.String(), nil, taskContent)

	runResult, runErr, budgetExpired := runWithSoftBudget(ctx, s.runner, prompt, s.maxDuration)
	if runErr != nil {
		// Budget expiry (fired deadline) → human_review before ## Verdict/post; never retried.
		// The streamed partial (if any) is salvaged under ## Salvage; never written to ## Verdict.
		if budgetExpired {
			glog.V(2).
				Infof("ai-review: soft time budget %s exceeded nextPhase=human_review", s.maxDuration)
			writeSalvage(md, ExtractBudgetPartial(runResult, runErr))
			return budgetExpiredResult("ai-review", s.maxDuration), nil
		}
		return &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: fmt.Sprintf("ai-review claude run failed: %v", runErr),
		}, nil
	}

	md.ReplaceSection(agentlib.Section{
		Heading: "## Verdict",
		Body:    runResult.Result,
	})

	// Post-verification: confirm the in_progress review persisted on GitHub.
	shouldVerify, skipCheckErr := s.shouldVerifyPost(ctx, md)
	if skipCheckErr != nil {
		glog.Warningf(
			"ai_review: skip-condition check failed err=%v; skipping verification",
			skipCheckErr,
		)
		shouldVerify = false
	}
	if s.verifier != nil && shouldVerify {
		verifyResult := s.callVerifier(ctx, md)
		if verifyResult != nil {
			appendVerifyDiagnostic(ctx, md, *verifyResult)
			return &agentlib.Result{
				Status: agentlib.AgentStatusFailed,
				Message: fmt.Sprintf(
					"ai_review: post verification failed: %s",
					verifyResult.ErrorMessage,
				),
			}, nil
		}
	}

	verdict, err := extractVerdict(ctx, runResult.Result)
	if err != nil {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   fmt.Sprintf("ai-review wrote ## Verdict but verdict unparseable: %v", err),
		}, nil
	}

	// Dismiss-and-comment is intentionally fire-and-forget: the dismissal
	// outcome is recorded in ## Diagnostics by tryDismissHallucinated, but
	// the next-phase routing below still falls through unchanged. A human
	// owns the final call on every fail verdict — the dismissal only
	// unblocks the GitHub merge gate, it does not auto-merge or change
	// where the task lands.
	if verdict.Verdict == "fail" && len(verdict.Hallucinations) > 0 {
		s.tryDismissHallucinated(ctx, md, verdict.Hallucinations)
	}

	if verdict.Verdict == "pass" {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "done",
			Message:   verdict.Reason,
		}, nil
	}

	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "human_review",
		Message:   fmt.Sprintf("ai-review verdict=%s: %s", verdict.Verdict, verdict.Reason),
	}, nil
}

// shouldVerifyPost returns true if post-verification should run.
// Returns false when ## Review is absent (no post was attempted) or the
// last diagnostics YAML block shows class: permanent or class: unknown.
func (s *reviewStep) shouldVerifyPost(_ context.Context, md *agentlib.Markdown) (bool, error) {
	if _, exists := md.FindSection("## Review"); !exists {
		return false, nil
	}

	diagSection, exists := md.FindSection("## Diagnostics")
	if !exists || diagSection == nil {
		return true, nil
	}

	lastBlock := lastYAMLBlock(diagSection.Body)
	if lastBlock == "" {
		return true, nil
	}

	for _, line := range strings.Split(lastBlock, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "class: "+string(ErrorClassPermanent) ||
			trimmed == "class: "+string(ErrorClassUnknown) {
			return false, nil
		}
	}

	return true, nil
}

// lastYAMLBlock returns the body of the last ```yaml...``` block in s.
// Returns empty string if no such block exists.
func lastYAMLBlock(s string) string {
	const fence = "```yaml"
	parts := strings.Split(s, fence)
	if len(parts) < 2 {
		return ""
	}
	lastPart := parts[len(parts)-1]
	closeIdx := strings.Index(lastPart, "```")
	if closeIdx < 0 {
		return ""
	}
	return lastPart[:closeIdx]
}

// callVerifier calls the verifier for the current PR. Returns nil when
// verification passes or is skipped; returns a non-nil VerifyResult when
// verification fails.
func (s *reviewStep) callVerifier(ctx context.Context, md *agentlib.Markdown) *VerifyResult {
	prURLStr := githubPRURLPattern.FindString(md.Preamble)
	if prURLStr == "" {
		glog.Warningf("ai_review verify: no GitHub PR URL in preamble — skipping")
		return nil
	}

	prInfo, err := prurl.ParsePRURL(ctx, prURLStr)
	if err != nil {
		glog.Warningf("ai_review verify: failed to parse PR URL %q: %v — skipping", prURLStr, err)
		return nil
	}

	if prInfo.Platform != prurl.PlatformGitHub {
		glog.Warningf("ai_review verify: non-GitHub platform %q — skipping", prInfo.Platform)
		return nil
	}

	headSHA, _ := md.Frontmatter.String("ref")
	glog.V(3).Infof(
		"ai_review verify: PR=%s bot=%s sha=%s token_set=%v",
		prURLStr, s.botLogin, headSHA, s.ghToken != "",
	)

	result := s.verifier.VerifyReview(ctx, VerifyRequest{
		PR:             *prInfo,
		HeadSHA:        headSHA,
		ExpectedStates: []string{"APPROVED", "CHANGES_REQUESTED"},
	})

	if result.Found {
		return nil
	}
	return &result
}

// appendVerifyDiagnostic appends a one-line ai_review verification failure
// entry to ## Diagnostics. Format is distinct from in_progress's fenced YAML
// blocks so operators can grep "ai_review verify:" specifically.
func appendVerifyDiagnostic(_ context.Context, md *agentlib.Markdown, result VerifyResult) {
	line := fmt.Sprintf(
		"ai_review verify: outcome=failed class=%s escalate_hint=%v http_status=%d error=%s\n",
		result.Class,
		result.EscalateHint,
		result.HTTPStatus,
		result.ErrorMessage,
	)
	var existingBody string
	if existing, ok := md.FindSection("## Diagnostics"); ok && existing != nil {
		existingBody = existing.Body
	}
	newBody := strings.TrimLeft(existingBody+"\n"+line, "\n")
	md.ReplaceSection(agentlib.Section{Heading: "## Diagnostics", Body: newBody})
}

// tryDismissHallucinated dismisses the bot's hallucinated review on
// the current head SHA and posts a follow-up COMMENT. Routing to
// human_review still happens unconditionally in the caller — this
// helper only mutates ## Diagnostics with the dismiss outcome.
func (s *reviewStep) tryDismissHallucinated(
	ctx context.Context,
	md *agentlib.Markdown,
	hallucinations []Hallucination,
) {
	if s.poster == nil {
		glog.V(2).Infof("ai_review dismiss: poster is nil — skipping")
		return
	}
	prURLStr := githubPRURLPattern.FindString(md.Preamble)
	if prURLStr == "" {
		glog.V(2).Infof("ai_review dismiss: no GitHub PR URL — skipping")
		return
	}
	prInfo, err := prurl.ParsePRURL(ctx, prURLStr)
	if err != nil {
		glog.Warningf("ai_review dismiss: failed to parse PR URL %q: %v — skipping", prURLStr, err)
		return
	}
	if prInfo.Platform != prurl.PlatformGitHub {
		glog.V(2).Infof("ai_review dismiss: non-GitHub platform %q — skipping", prInfo.Platform)
		return
	}
	headSHA, _ := md.Frontmatter.String("ref")
	if headSHA == "" {
		glog.Warningf("ai_review dismiss: empty ref in frontmatter — skipping")
		return
	}
	result := s.poster.DismissCurrentReview(ctx, *prInfo, headSHA, hallucinations)
	appendDismissDiagnostic(md, result)
}

// appendDismissDiagnostic appends a YAML block describing the dismiss
// attempt to ## Diagnostics. Called on every attempt (success and
// failure) so AC verification can grep for the step + http_status.
func appendDismissDiagnostic(md *agentlib.Markdown, result PostResult) {
	block := fmt.Sprintf(
		"ai_review dismiss:\n  outcome: %q\n  step: %q\n  http_status: %d\n  error: %q\n",
		result.Outcome,
		result.FailureStep,
		result.HTTPStatus,
		result.ErrorMessage,
	)
	var existingBody string
	if existing, ok := md.FindSection("## Diagnostics"); ok && existing != nil {
		existingBody = existing.Body
	}
	newBody := strings.TrimLeft(existingBody+"\n"+block, "\n")
	md.ReplaceSection(agentlib.Section{Heading: "## Diagnostics", Body: newBody})
}

// extractVerdict parses the verdict from the LLM's response. The prompt
// asks for raw JSON only, but Claude sometimes prefixes the JSON with
// prose explanation. To be tolerant, we (1) try direct unmarshal of the
// trimmed response, then (2) strip ```json fences if present, then
// (3) extract the last balanced {...} block from the response.
func extractVerdict(ctx context.Context, raw string) (verdictPayload, error) {
	trimmed := strings.TrimSpace(raw)

	// 1. Direct attempt.
	var v verdictPayload
	if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
		return v, nil
	}

	// 2. Strip code fences.
	stripped := strings.TrimSpace(strings.TrimSuffix(
		strings.TrimPrefix(strings.TrimPrefix(trimmed, "```json"), "```"),
		"```",
	))
	if err := json.Unmarshal([]byte(stripped), &v); err == nil {
		return v, nil
	}

	// 3. Extract last balanced {...} block.
	block, ok := lastJSONBlock(ctx, trimmed)
	if !ok {
		return verdictPayload{}, errors.Errorf(ctx, "no JSON object found in response")
	}
	if err := json.Unmarshal([]byte(block), &v); err != nil {
		return verdictPayload{}, errors.Wrapf(ctx, err, "extract last JSON block")
	}
	return v, nil
}

// lastJSONBlock returns the last balanced {...} substring in s, or
// "", false if none exists. Walks from the end finding the closing
// brace, then walks back tracking brace depth to find the matching open.
func lastJSONBlock(_ context.Context, s string) (string, bool) {
	end := strings.LastIndex(s, "}")
	if end < 0 {
		return "", false
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return s[i : end+1], true
			}
		}
	}
	return "", false
}
