// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/githubauth"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
)

// RunConfig is the input to RunAgent — everything the orchestrator needs
// regardless of how the task is read or where the result is delivered.
type RunConfig struct {
	ClaudeConfigDir claudelib.ClaudeConfigDir
	AgentDir        claudelib.AgentDir
	Model           claudelib.ClaudeModel
	GHToken         string
	// Anthropic-compatible alt-provider routing (e.g. MiniMax). When BaseURL +
	// AuthToken are non-empty they are injected into the claude subprocess env;
	// Model is mirrored into ANTHROPIC_MODEL there for parity with the --model flag.
	AnthropicBaseURL   string
	AnthropicAuthToken string
	// Alias→model overrides forwarded to the claude subprocess so spawned
	// sub-agents (opus/sonnet/haiku/fable) resolve on non-Anthropic endpoints.
	AnthropicDefaultOpusModel   string
	AnthropicDefaultSonnetModel string
	AnthropicDefaultHaikuModel  string
	AnthropicDefaultFableModel  string
	ReposPath                   string
	WorkPath                    string
	ReviewMode                  string
	// MaxReviewDuration is the soft time budget for each Claude phase run,
	// applied below the K8s Job hard deadline. Default 25m (REVIEW_MAX_DURATION).
	MaxReviewDuration libtime.Duration
	RepoAllowlist     []string                // host-qualified repos the agent may clone
	AuthSetup         githubauth.Configurator // pod: real gh-auth-setup; local-CLI: noop
	Phase             domain.TaskPhase
	TaskContent       string
	Deliverer         agentlib.ResultDeliverer
	// BotLogin is the GitHub bot login used by githubposter. When non-empty it
	// is injected into the env map as BOT_GITHUB_LOGIN so ResolveBotLogin
	// picks it up instead of the DefaultBotLogin fallback.
	BotLogin string
	// Agent overrides the agent used for execution. If nil, CreateAgent is called.
	// Set by main.go after dispatching via CreateAgentProvider. cmd/run-task leaves
	// this nil so CreateAgent is used for backward compatibility.
	Agent *agentlib.Agent
	// CurrentDateTime is the time source injected into step structs and poster/verifier.
	CurrentDateTime libtime.CurrentDateTimeGetter
	// SkipPost suppresses all GitHub write calls. Intended for local runs against
	// repositories the operator does not own; review findings are still written
	// to the task file.
	SkipPost bool
}

// ResolvePosters returns nil interfaces when cfg.SkipPost is true, otherwise
// the result of CreatePrPoster and CreateReviewVerifier. Wiring selection only;
// no I/O is performed in this helper.
func ResolvePosters(cfg RunConfig, botLogin string) (prpkg.PrPoster, prpkg.ReviewVerifier) {
	if cfg.SkipPost {
		return nil, nil
	}
	poster := CreatePrPoster(cfg.GHToken, botLogin, cfg.CurrentDateTime)
	verifier := CreateReviewVerifier(cfg.GHToken, botLogin, cfg.CurrentDateTime)
	return poster, verifier
}

// RunAgent performs the shared startup + execution flow for the maintainer-agent-pr-reviewer binary.
// Both entry points (Kafka pod main.go and local CLI cmd/run-task) call this after
// resolving their I/O specifics — task content source and result deliverer.
//
// Steps performed in order:
//  1. Prune stale worktrees from any prior run
//  2. Ensure the bborbe/coding plugin is installed in CLAUDE_CONFIG_DIR
//     (defense-in-depth: pod boot would otherwise rely on an external installer
//     and local CLI runs would silently degrade reviews)
//  3. Build the per-phase agent
//  4. Run the requested phase against the supplied task content
func RunAgent(ctx context.Context, cfg RunConfig) (*agentlib.Result, error) {
	workdirCfg := git.WorkdirConfig{ReposPath: cfg.ReposPath, WorkPath: cfg.WorkPath}
	repoManager := git.NewRepoManager(workdirCfg, cfg.GHToken)
	if err := repoManager.PruneAllWorktrees(ctx); err != nil {
		glog.Warningf("startup worktree prune: %v", err)
	}

	installer := claudelib.NewPluginInstaller(claudelib.NewExecPluginCommander())
	if err := installer.EnsureInstalled(ctx, []claudelib.PluginSpec{
		{Marketplace: "bborbe/coding", Name: "coding"},
	}); err != nil {
		return nil, errors.Wrap(ctx, err, "ensure plugins installed")
	}

	if err := cfg.AuthSetup.Setup(ctx); err != nil {
		return nil, deliverStartupFailure(ctx, cfg.Deliverer, err, "github auth setup failed")
	}

	env := map[string]string{}
	if cfg.GHToken != "" {
		env["GH_TOKEN"] = cfg.GHToken
	}
	if cfg.AnthropicBaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = cfg.AnthropicBaseURL
	}
	if cfg.AnthropicAuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = cfg.AnthropicAuthToken
	}
	if cfg.Model != "" {
		env["ANTHROPIC_MODEL"] = cfg.Model.String()
	}
	if cfg.AnthropicDefaultOpusModel != "" {
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = cfg.AnthropicDefaultOpusModel
	}
	if cfg.AnthropicDefaultSonnetModel != "" {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = cfg.AnthropicDefaultSonnetModel
	}
	if cfg.AnthropicDefaultHaikuModel != "" {
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = cfg.AnthropicDefaultHaikuModel
	}
	if cfg.AnthropicDefaultFableModel != "" {
		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = cfg.AnthropicDefaultFableModel
	}
	if cfg.BotLogin != "" {
		env["BOT_GITHUB_LOGIN"] = cfg.BotLogin
	}

	agent := cfg.Agent
	if agent == nil {
		botLogin := ResolveBotLogin(env)
		poster, verifier := ResolvePosters(cfg, botLogin)
		agent = CreateAgent(
			cfg.ClaudeConfigDir,
			cfg.AgentDir,
			cfg.Model,
			cfg.GHToken,
			env,
			repoManager,
			cfg.ReviewMode,
			cfg.RepoAllowlist,
			poster,
			verifier,
			cfg.CurrentDateTime,
			cfg.MaxReviewDuration,
		)
	}
	return agent.Run(ctx, cfg.Phase, cfg.TaskContent, cfg.Deliverer)
}

// deliverStartupFailure wraps err with msg, publishes a Failed result via
// deliverer so the passthrough content generator splices the wrapped error
// into the task's ## Failure section, and returns the wrapped error so the
// caller can still propagate (process exit, metrics, etc.).
//
// Without the delivery step, early-startup errors (auth setup, plugin install)
// exit the pod non-zero and only "Job has reached the specified backoff limit"
// surfaces in the OpenClaw task body — operators cannot diagnose without
// racing pod TTL for `kubectl logs`.
//
// Delivery errors are logged but do NOT replace the original startup error in
// the returned chain; the original cause is what operators need to see.
func deliverStartupFailure(
	ctx context.Context,
	deliverer agentlib.ResultDeliverer,
	err error,
	msg string,
) error {
	wrapped := errors.Wrap(ctx, err, msg)
	if delivErr := deliverer.DeliverResult(ctx, agentlib.AgentResultInfo{
		Status:  agentlib.AgentStatusFailed,
		Message: wrapped.Error(),
	}); delivErr != nil {
		glog.Warningf("deliver startup failure %q: %v", msg, delivErr)
	}
	return wrapped
}
