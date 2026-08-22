// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
)

// planningOutput is the parsed shape of the ## Plan JSON block.
type planningOutput struct {
	Concerns []struct{} `json:"concerns"`
}

// maxPlanningAttempts is the hardcoded cap on Claude planning calls per
// invocation. Malformed-JSON responses are retried up to this many times;
// AgentStatusFailed is returned only after all attempts fail. Not configurable.
const maxPlanningAttempts = 3

// planningStep runs Claude to produce the ## Plan section, then branches:
// Planning is pure triage: it produces the ## Plan section and always advances
// to the execution phase, where the real (checkout + deep) review runs and posts
// an earned APPROVE/REQUEST_CHANGES verdict. There is deliberately no "no
// concerns → post LGTM" shortcut — a shallow planning pass must never
// rubber-stamp a positive review without the execution review running.
type planningStep struct {
	runner       claudelib.ClaudeRunner
	instructions claudelib.Instructions
	maxDuration  libtime.Duration
}

// NewPlanningStep constructs the planning-phase step. maxDuration is the soft
// REVIEW_MAX_DURATION budget enforced on each claude run; expiry routes to
// human_review instead of the controller retry path.
func NewPlanningStep(
	runner claudelib.ClaudeRunner,
	instructions claudelib.Instructions,
	maxDuration libtime.Duration,
) agentlib.Step {
	return &planningStep{
		runner:       runner,
		instructions: instructions,
		maxDuration:  maxDuration,
	}
}

// Name implements agentlib.Step.
func (s *planningStep) Name() string { return "pr-plan" }

// ShouldRun always returns true. Idempotency is handled inside Run: if a
// ## Plan section already exists in the body (e.g. left over from a previous
// trigger whose execution phase failed and got retriggered), the step skips
// the claude call but still parses the existing plan and publishes the
// routing decision (Done + NextPhase=execution|done|human_review). Returning
// false here would skip the routing too and the phase would silently
// short-circuit to done — see the trading#136 retrigger incident
// (2026-05-25), where the controller reset trigger_count, the stale ## Plan
// remained, the planning step was skipped, no execution ran, and the task
// got marked phase: done without any review posted.
func (s *planningStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run handles two paths:
//   - ## Plan already present → re-parse and re-route from the existing body
//     without re-calling claude (preserves idempotency for cost reasons).
//   - ## Plan missing → call claude with the planning prompt, write ## Plan,
//     parse concerns, route.
//
// Routes: empty concerns → LGTM POST → done; non-empty → execution phase.
func (s *planningStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	if section, exists := md.FindSection("## Plan"); exists {
		glog.V(2).Infof("planning: ## Plan already present — re-routing without claude")
		return s.routeFromPlan(ctx, md, section.Body)
	}

	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "planning marshal task")
	}

	prURL := ExtractPRURL(md)
	ref, _ := md.Frontmatter.String("ref")
	glog.V(2).Infof("planning: starting pr_url=%q ref=%s", prURL, ref)

	prompt := claudelib.BuildPrompt(s.instructions.String(), nil, taskContent)

	var lastParseErr error
	for attempt := 1; attempt <= maxPlanningAttempts; attempt++ {
		runResult, runErr, budgetExpired := runWithSoftBudget(ctx, s.runner, prompt, s.maxDuration)
		if runErr != nil {
			// Budget expiry (a FIRED run-context deadline) routes to human_review
			// and is never retried, never written to ## Plan. The streamed partial
			// (if any) is salvaged under ## Salvage before the budget result returns.
			if budgetExpired {
				glog.V(2).
					Infof("planning: soft time budget %s exceeded nextPhase=human_review", s.maxDuration)
				writeSalvage(md, ExtractBudgetPartial(runResult, runErr))
				return budgetExpiredResult("planning", s.maxDuration), nil
			}
			// Transport error (nil result + err) is NOT retried — controller territory.
			glog.V(2).Infof("planning: claude failed nextPhase=human_review err=%v", runErr)
			return &agentlib.Result{
				Status:  agentlib.AgentStatusFailed,
				Message: fmt.Sprintf("planning claude run failed: %v", runErr),
			}, nil
		}

		if _, parseErr := parsePlanningConcerns(ctx, runResult.Result); parseErr != nil {
			lastParseErr = parseErr
			if attempt < maxPlanningAttempts {
				glog.V(2).Infof(
					"planning: attempt %d/%d malformed JSON, retrying err=%v",
					attempt, maxPlanningAttempts, parseErr,
				)
				continue
			}
			// Exhausted all attempts.
			glog.V(2).Infof(
				"planning: malformed JSON after %d attempts, not persisting err=%v",
				maxPlanningAttempts, parseErr,
			)
			return &agentlib.Result{
				Status: agentlib.AgentStatusFailed,
				Message: fmt.Sprintf(
					"planning: malformed JSON after %d attempts: %v",
					maxPlanningAttempts,
					lastParseErr,
				),
			}, nil
		}

		// Parseable — persist this response and route.
		md.ReplaceSection(agentlib.Section{
			Heading: "## Plan",
			Body:    runResult.Result,
		})
		return s.routeFromPlan(ctx, md, runResult.Result)
	}

	// Unreachable — the loop returns on every path. Kept for compiler completeness.
	return &agentlib.Result{
		Status: agentlib.AgentStatusFailed,
		Message: fmt.Sprintf(
			"planning: malformed JSON after %d attempts: %v",
			maxPlanningAttempts,
			lastParseErr,
		),
	}, nil
}

// routeFromPlan parses concerns from a ## Plan body (freshly produced by
// claude or read back from the vault on retrigger) and returns the routing
// decision. Centralised so the "## Plan exists" and "claude just produced
// ## Plan" paths produce identical Result values.
func (s *planningStep) routeFromPlan(
	ctx context.Context,
	md *agentlib.Markdown,
	planBody string,
) (*agentlib.Result, error) {
	concerns, parseErr := parsePlanningConcerns(ctx, planBody)
	if parseErr != nil {
		// Malformed JSON in ## Plan is a planning failure — escalate.
		glog.V(2).Infof("planning: parse failed nextPhase=human_review err=%v", parseErr)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   fmt.Sprintf("planning: failed to parse ## Plan JSON: %v", parseErr),
		}, nil
	}

	// Only a GitHub PR can run the execution (checkout + review) flow. A task
	// with no GitHub PR URL is not reviewable here — escalate rather than advance
	// to a checkout that would fail. (In production the github-pr watcher only
	// emits GitHub PRs, so this is a defensive guard; it also subsumes the old
	// "non-GitHub platform" case — anything not a GitHub PR goes to human_review.)
	if prURLStr := ExtractPRURL(md); prURLStr == "" {
		glog.V(2).Infof("planning: no GitHub PR URL nextPhase=human_review")
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   "planning: no GitHub PR URL found — cannot run review",
		}, nil
	}

	// GitHub PR → execution phase for a real (checkout + deep) review that posts
	// an earned APPROVE/REQUEST_CHANGES verdict. Concerns are a hint only — there
	// is deliberately no "no concerns → LGTM" shortcut, so a shallow planning pass
	// can never rubber-stamp a positive review. (Canonical phase name per spec 032;
	// do NOT revert to "in_progress" — the agentlib frontmatter validator rejects
	// that stale literal and the task short-circuits to done.)
	glog.V(2).Infof("planning: %d concern hints nextPhase=execution", len(concerns))
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: string(domain.TaskPhaseExecution),
	}, nil
}

// fencedJSONPattern matches the first ```json … ``` (or bare ``` … ```) fenced
// block anywhere in the text. (?s) lets . span newlines; .*? is non-greedy so
// only the first block is captured.
var fencedJSONPattern = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// extractJSONObject pulls the JSON object out of a Claude planning response
// that may be surrounded by conversational prose and/or ```json fences.
// DeepSeek (vLLM) narrates before the fence ("Now I have the full picture…"),
// whereas real Anthropic emits clean JSON — so a plain fence-trim isn't enough;
// the JSON must be located regardless of surrounding text. Strategy, in order:
//  1. first ```json/``` fenced block's contents,
//  2. first '{' … last '}' span (fence-less prose-wrapped output),
//  3. the trimmed input unchanged (let json.Unmarshal surface the error).
func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	if m := fencedJSONPattern.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			return s[start : end+1]
		}
	}
	return s
}

// parsePlanningConcerns extracts the concerns array from the ## Plan JSON body.
// The JSON may be bare, wrapped in ```json ... ``` fences, or embedded in
// conversational prose (see extractJSONObject). Returns an error if the JSON
// cannot be parsed or the concerns field is absent.
func parsePlanningConcerns(ctx context.Context, body string) ([]struct{}, error) {
	var p planningOutput
	if err := json.Unmarshal([]byte(extractJSONObject(body)), &p); err != nil {
		return nil, errors.Wrapf(ctx, err, "parse ## Plan JSON")
	}
	return p.Concerns, nil
}
