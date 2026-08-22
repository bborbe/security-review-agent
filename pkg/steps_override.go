// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"

	agentlib "github.com/bborbe/agent"
	"github.com/golang/glog"

	prurl "github.com/bborbe/maintainer/prurl"
)

// TaskTypePROverride is the task type for label-triggered override tasks. The
// github-pr-watcher emits it (as a plain string) for a trusted-author PR
// carrying the override label; the agent routes it to the code-only override
// step below. Kept as a local constant (not in the agent lib) so no lib release
// is needed — the watcher and this agent agree on the literal "pr-override".
const TaskTypePROverride agentlib.TaskType = "pr-override"

// overrideStep is the single, code-only step of the pr-override task type. It
// posts an APPROVE at the PR head SHA as the bot — a fresh bot APPROVE
// supersedes the bot's own false-positive CHANGES_REQUESTED for reviewDecision,
// so a trusted author who applied the override label can merge without admin.
// No Claude, no clone, no container: pure GitHub-API action reusing the poster.
type overrideStep struct {
	prPoster PrPoster
	botLogin string
}

// NewOverrideStep constructs the override-phase step.
func NewOverrideStep(prPoster PrPoster, botLogin string) agentlib.Step {
	return &overrideStep{prPoster: prPoster, botLogin: botLogin}
}

// Name implements agentlib.Step.
func (s *overrideStep) Name() string { return "pr-override" }

// ShouldRun always returns true — the override action is idempotent (a repeat
// APPROVE at the same head SHA is harmless).
func (s *overrideStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run posts an APPROVE at the PR head SHA and writes an ## Override diagnostics
// section. It routes to human_review (a human still triggers the actual merge),
// matching the approve-verdict terminal of the review flow.
func (s *overrideStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	prURLStr := ExtractPRURL(md)
	if prURLStr == "" {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   "override: no GitHub PR URL found — cannot post approve",
		}, nil
	}
	prInfo, err := prurl.ParsePRURL(ctx, prURLStr)
	if err != nil {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   fmt.Sprintf("override: parse PR URL failed: %v", err),
		}, nil
	}
	if prInfo.Platform != prurl.PlatformGitHub {
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   "override: non-GitHub PR — override not supported",
		}, nil
	}
	ref, _ := md.Frontmatter.String("ref")
	if ref == "" {
		return &agentlib.Result{
			Status:  agentlib.AgentStatusFailed,
			Message: "override: ref (head SHA) missing from task frontmatter",
		}, nil
	}

	body := fmt.Sprintf(
		"Override APPROVE by %s — the `override-review` label was applied by a trusted "+
			"author. Posting APPROVE so the earlier false-positive review no longer blocks "+
			"merge. CI status checks still apply.",
		s.botLogin,
	)

	glog.V(2).Infof(
		"override: posting APPROVE pr=%s/%s#%d sha=%s",
		prInfo.Owner, prInfo.Repo, prInfo.Number, ref,
	)
	result := s.prPoster.PostOverrideApprove(ctx, *prInfo, ref, body)

	md.ReplaceSection(agentlib.Section{
		Heading: "## Override",
		Body:    formatOverrideResult(result),
	})

	if result.Outcome != "success" {
		return &agentlib.Result{
			Status: agentlib.AgentStatusFailed,
			Message: fmt.Sprintf(
				"override: post APPROVE failed step=%s http=%d err=%s",
				result.FailureStep, result.HTTPStatus, result.ErrorMessage,
			),
		}, nil
	}
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "human_review",
		Message:   fmt.Sprintf("override: APPROVE posted review_id=%d", result.ReviewID),
	}, nil
}

// formatOverrideResult renders the PostResult as a small YAML-ish block for the
// task's ## Override section, mirroring the diagnostics style of other steps.
func formatOverrideResult(r PostResult) string {
	return fmt.Sprintf(
		"outcome: %s\nposted_event: %s\nreview_id: %d\nhttp_status: %d\nfailure_step: %s\nerror: %s\n",
		r.Outcome,
		r.PostedEvent,
		r.ReviewID,
		r.HTTPStatus,
		r.FailureStep,
		r.ErrorMessage,
	)
}
