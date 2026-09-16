// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the maintainer-agent-security-reviewer binary.
//
// All factory functions follow the Create* prefix convention and contain
// zero business logic — they compose constructors with config.
package factory

import (
	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	"github.com/bborbe/agent/healthcheck"
	"github.com/bborbe/cqrs/base"
	libkafka "github.com/bborbe/kafka"
	prpkg "github.com/bborbe/security-review-agent/pkg"
	"github.com/bborbe/security-review-agent/pkg/git"
	"github.com/bborbe/security-review-agent/pkg/prompts"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
)

const serviceName = "maintainer-agent-security-reviewer"

// Per-phase tool scopes. Principle: each phase gets the smallest set that
// lets it do its job. Planning + Review are read-only inspection. Execution
// gets broader git access for cross-file reads. The security agent wires no
// poster and no verifier: the execution phase writes ## Review to the vault
// and routes to ai_review without any GitHub posting, and the ai_review phase
// writes ## Verdict without post-verification — the run ends at the vault
// write-back, nothing is ever posted to a PR.
var (
	planningTools = claudelib.AllowedTools{
		"Read", "Grep", "Glob",
		"Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)",
		"Bash(gh pr view:*)", "Bash(gh pr diff:*)", "Bash(gh pr list:*)",
	}
	// Execution-phase allowlist for the inlined /coding:pr-review procedure.
	//
	// Selector mode (the plugin default since coding v0.22.0, commit 5ac8a60
	// "feat!: selector mode is the default dispatcher; remove standard-mode
	// per-owner dispatch") does classify+adjudicate IN-SESSION with zero
	// sub-agent spawns — so the parent execution phase itself must Read files,
	// run the ast-grep mechanical funnel, and shell jq/git rev-parse. The older
	// per-owner-dispatch model needed only Task+git (the parent just fanned out
	// to sub-agents, which did the reading); when the baked plugin flipped to
	// selector-default, that allowlist stopped covering the review and the bot
	// stalled asking for permission it can't get in a non-interactive container.
	//
	// Boundary preserved: everything below is read-only inspection. The ast-grep
	// mechanical funnel is NO LONGER on this allowlist — the agent runs it itself
	// in Go (pkg.FunnelRunner) and injects the findings into the execution prompt.
	// A weak review model wrapped the runner in forms the allowlist could not
	// match (`> redirect`, `bash -c`, `$RUNNER`), got denied, and silently
	// dropped the MUST-tier pass; running the funnel in Go removes both that
	// invocation-form fragility and the model's ability to skip it. No Write/Edit,
	// no network tools (curl/wget/nc), no arbitrary Bash — a malicious PR still
	// cannot exfiltrate the GitHub App token.
	executionTools = claudelib.AllowedTools{
		"Task",
		"Read", "Grep", "Glob",
		"Bash(git diff:*)",
		"Bash(git log:*)",
		"Bash(git show:*)",
		"Bash(git status:*)",
		"Bash(git ls-files:*)",
		"Bash(git fetch:*)",
		"Bash(git worktree:*)",
		"Bash(git branch:*)",
		"Bash(git rev-parse:*)",
		"Bash(git -C * diff *)",
		"Bash(git -C * log *)",
		"Bash(git -C * show *)",
		"Bash(git -C * status *)",
		"Bash(git -C * ls-files *)",
		"Bash(git -C * fetch *)",
		"Bash(git -C * worktree *)",
		"Bash(git -C * branch *)",
		"Bash(git -C * rev-parse *)",
		"Bash(git -C * diff)",
		"Bash(git -C * log)",
		"Bash(git -C * show)",
		"Bash(git -C * status)",
		"Bash(git -C * ls-files)",
		"Bash(git -C * fetch)",
		"Bash(git -C * worktree)",
		"Bash(git -C * branch)",
		"Bash(git -C * rev-parse)",
		"Bash(command -v:*)",
		"Bash(jq:*)",
		"Bash(rm -rf:*)",
	}
	reviewTools = claudelib.AllowedTools{
		"Read", "Grep",
		"Bash(gh pr view:*)", "Bash(gh pr diff:*)",
	}
)

// CreateClaudeRunner constructs a ClaudeRunner pre-configured with tools,
// model, working directory, and CLI environment. env is forwarded as-is
// into the Claude CLI subprocess env (caller builds it, e.g. with GH_TOKEN).
func CreateClaudeRunner(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	env map[string]string,
	allowedTools claudelib.AllowedTools,
) claudelib.ClaudeRunner {
	return claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
		ClaudeConfigDir:  claudeConfigDir,
		AllowedTools:     allowedTools,
		Model:            model,
		WorkingDirectory: agentDir,
		Env:              env,
	})
}

// CreateKafkaResultDeliverer creates a ResultDeliverer that publishes task
// updates to Kafka via CQRS commands. Uses the passthrough content generator
// — the agent framework's StepRunner already produces the full marshaled
// task in result.Output; the deliverer publishes it as-is.
func CreateKafkaResultDeliverer(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
	taskID agentlib.TaskIdentifier,
	originalContent string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.ResultDeliverer {
	return delivery.NewKafkaResultDeliverer(
		syncProducer,
		topicPrefix,
		taskID,
		originalContent,
		delivery.NewPassthroughContentGenerator(),
		currentDateTime,
	)
}

// CreateFileResultDeliverer creates a ResultDeliverer that writes the agent's
// output back to a markdown file (local CLI mode).
func CreateFileResultDeliverer(filePath string) agentlib.ResultDeliverer {
	return delivery.NewFileResultDeliverer(
		delivery.NewPassthroughContentGenerator(),
		filePath,
	)
}

// CreateAgent assembles the full 3-phase security-reviewer agent with per-phase
// tool scopes and per-phase prompts:
//
//   - planning: read-only diff inspection → ## Plan (JSON)
//   - execution: read + cross-file inspection → ## Review (JSON). With a nil
//     prPoster (the security agent wires no poster) the step writes ## Review to
//     the vault and routes to ai_review without any GitHub posting.
//   - ai_review: minimal read-only fresh-context verifier → ## Verdict (JSON);
//     verdict=pass → done, otherwise → human_review. With a nil verifier (the
//     security agent wires no verifier) the step writes ## Verdict without any
//     post-verification.
//
// maxDuration is the soft REVIEW_MAX_DURATION budget threaded into every phase
// step. The execution step gets a nil runner (runClaude builds a fresh one).
func CreateAgent(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	reviewMode string,
	repoAllowlist []string,
	prPoster prpkg.PrPoster,
	verifier prpkg.ReviewVerifier,
	currentDateTime libtime.CurrentDateTimeGetter,
	maxDuration libtime.Duration,
) *agentlib.Agent {
	botLogin := ResolveBotLogin(env)
	tokenCheck := prpkg.NewGHTokenCheckStep(ghToken)
	planningPhase := agentlib.NewPhase("planning", tokenCheck, prpkg.NewPlanningStep(
		CreateClaudeRunner(claudeConfigDir, agentDir, model, env, planningTools),
		prompts.BuildPlanningInstructions(),
		maxDuration,
	))
	executionStep := prpkg.NewCheckoutExecutionStep(
		repoManager,
		claudeConfigDir,
		agentDir,
		model,
		env,
		executionTools,
		reviewMode,
		repoAllowlist,
		prPoster,
		prpkg.NewFunnelRunner(claudeConfigDir),
		currentDateTime,
		nil, // runner — production builds a fresh ClaudeRunner in runClaude
		maxDuration,
	)
	reviewStep := prpkg.NewReviewStep(
		CreateClaudeRunner(claudeConfigDir, agentDir, model, env, reviewTools),
		prPoster,
		prompts.BuildReviewInstructions(),
		verifier,
		ghToken,
		botLogin,
		maxDuration,
	)
	return agentlib.NewAgent(
		planningPhase,
		agentlib.NewPhase(domain.TaskPhaseExecution, tokenCheck, executionStep),
		agentlib.NewPhase("ai_review", tokenCheck, reviewStep),
	)
}

// CreateAgentProvider wires the per-task-type dispatch table for
// maintainer-agent-security-reviewer. TaskTypeSecurityReview routes to the
// 3-phase security agent built by CreateAgent with nil poster + nil verifier
// (no GitHub posting — the run ends at the ## Review write-back).
// TaskTypeHealthcheck routes to the liveness agent. TaskTypeBuildFix routes to
// the single-phase code-only build-fix agent, which reads the failed Cloud
// Build's log over the Cloud Build REST API, writes the failure analysis into
// the task in place, and unassigns it for a human — analysis only, no Claude,
// no clone, no auto-fix. pr-review and pr-override are deliberately NOT
// registered: a task mis-routed to this binary is refused by the
// unknown-task-type rejection and never run as a plain review.
// Pure plumbing; no conditional, no error.
func CreateAgentProvider(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	reviewMode string,
	repoAllowlist []string,
	currentDateTime libtime.CurrentDateTimeGetter,
	maxDuration libtime.Duration,
	cloudBuildProject string,
	cloudBuildRegion string,
) agentlib.AgentProvider {
	domainAgent := CreateAgent(
		claudeConfigDir,
		agentDir,
		model,
		ghToken,
		env,
		repoManager,
		reviewMode,
		repoAllowlist,
		nil, // prPoster — the security agent wires no poster
		nil, // verifier — the security agent wires no verifier
		currentDateTime,
		maxDuration,
	)
	healthcheckRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		model,
		env,
		claudelib.AllowedTools{},
	)
	livenessAgent := healthcheck.NewAgent(healthcheck.NewClaudeStep(healthcheckRunner))
	buildFixAgent := agentlib.NewAgent(
		agentlib.NewPhase(
			domain.TaskPhaseExecution,
			prpkg.NewBuildFixStep(cloudBuildProject, cloudBuildRegion),
		),
	)
	return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
		prpkg.TaskTypeSecurityReview: domainAgent,
		agentlib.TaskTypeHealthcheck: livenessAgent,
		prpkg.TaskTypeBuildFix:       buildFixAgent,
	})
}

// CreateDeliverer builds the Kafka result deliverer used by the Kafka
// entry point. The caller owns the SyncProducer lifecycle and must close it
// after the deliverer is no longer needed.
func CreateDeliverer(
	syncProducer libkafka.SyncProducer,
	taskID agentlib.TaskIdentifier,
	topicPrefix base.TopicPrefix,
	originalContent string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.ResultDeliverer {
	return CreateKafkaResultDeliverer(
		syncProducer,
		topicPrefix,
		taskID,
		originalContent,
		currentDateTime,
	)
}
