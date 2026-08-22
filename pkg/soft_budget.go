// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	libtime "github.com/bborbe/time"
)

// runWithSoftBudget runs the claude runner under a context whose deadline is
// the soft REVIEW_MAX_DURATION budget. Returns the run result/error plus
// whether the budget expired — detected PRECISELY from the run context's own
// deadline (runCtx.Err() == context.DeadlineExceeded), never inferred from the
// returned error. A non-expired runner error (crash, network, or an error that
// merely wraps context.DeadlineExceeded without the deadline firing) keeps the
// existing failed/controller-retry path.
//
//nolint:revive // error-return: the (result, error, expired) triple is the deliberate budget contract.
func runWithSoftBudget(
	ctx context.Context,
	runner claudelib.ClaudeRunner,
	prompt string,
	maxDuration libtime.Duration,
) (*claudelib.ClaudeResult, error, bool) {
	runCtx, cancel := context.WithTimeout(ctx, maxDuration.Duration())
	defer cancel()
	result, err := runner.Run(runCtx, prompt)
	expired := runCtx.Err() == context.DeadlineExceeded
	return result, err, expired
}

// budgetExpiredResult builds the bounded-outcome routing for a budget-terminated
// claude run: Status done + NextPhase human_review with a message naming both
// the phase and the soft budget. Shared by all three phase steps so the
// budget-naming message and routing stay consistent; each step calls it only
// after its own runWithSoftBudget reported expired=true (a fired deadline).
func budgetExpiredResult(stepName string, maxDuration libtime.Duration) *agentlib.Result {
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "human_review",
		Message: fmt.Sprintf(
			"%s claude run exceeded the %s soft time budget — routed to human review",
			stepName,
			maxDuration,
		),
	}
}
