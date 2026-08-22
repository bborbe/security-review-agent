// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command run-task is the local-CLI entry point for maintainer-agent-pr-reviewer.
//
// Reads a markdown task file from disk, runs the agent against it, and
// writes the updated content back to the same file. Mirrors the Kafka
// entry point (../../main.go) but uses file I/O instead of Kafka/CQRS.
package main

import (
	"context"
	"os"
	"path/filepath"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	"github.com/bborbe/github-pr-review-agent/pkg/githubauth"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	githubapp "github.com/bborbe/maintainer/githubapp"
	repoallowlist "github.com/bborbe/maintainer/repoallowlist"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	// Claude Code CLI configuration
	ClaudeConfigDir claudelib.ClaudeConfigDir `required:"false" arg:"claude-config-dir" env:"CLAUDE_CONFIG_DIR" usage:"Claude Code config directory" default:"~/.claude"`

	// Agent directory (contains .claude/ with CLAUDE.md and commands)
	AgentDir claudelib.AgentDir `required:"false" arg:"agent-dir" env:"AGENT_DIR" usage:"Agent directory with .claude/ config" default:"agent"`

	// Workdir paths for bare-clone cache and per-task worktrees (default: ~/.cache/maintainer/pr-reviewer/*)
	ReposPath string `required:"false" arg:"repos-path" env:"REPOS_PATH" usage:"Root path for bare-clone cache (default: ~/.cache/maintainer/pr-reviewer/repos)"`
	WorkPath  string `required:"false" arg:"work-path"  env:"WORK_PATH"  usage:"Root path for per-task worktrees (default: ~/.cache/maintainer/pr-reviewer/work)"`

	// Review depth passed to /coding:pr-review (short | standard | full)
	ReviewMode string `required:"false" arg:"review-mode" env:"REVIEW_MODE" usage:"Review depth: short | standard | full" default:"standard"`

	// MaxReviewDuration is the soft time budget for each Claude phase run
	// (planning, execution, ai_review), applied below the K8s Job hard deadline.
	// REVIEW_MAX_DURATION is validated at startup: it must parse as a duration
	// and be >= 60s. The default 25m sits below the executor's default
	// ZombieJobTimeoutSeconds (1800s) with headroom for salvage + Kafka delivery;
	// operators who lower zombieJobTimeoutSeconds must keep this below it.
	MaxReviewDuration libtime.Duration `required:"false" arg:"review-max-duration" env:"REVIEW_MAX_DURATION" usage:"Soft time budget per Claude phase run; keep below the K8s Job ActiveDeadlineSeconds / ZombieJobTimeoutSeconds (default 1800s) with headroom for salvage + Kafka delivery; must be >= 60s" default:"25m"`

	// Environment
	Branch base.Branch `required:"true" arg:"branch" env:"BRANCH" usage:"branch" default:"dev"`

	// Phase to run (framework requires explicit phase)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"execution"`

	// Task file for local development
	TaskFilePath string `required:"true" arg:"task-file" env:"TASK_FILE" usage:"Path to the markdown task file"`

	// GitHub App authentication. The pod mints an installation access token at startup
	// and forwards it to the agent subprocess. App auth is the only supported auth path.
	AppID          int64  `required:"false" arg:"app-id"          env:"APP_ID"           usage:"GitHub App ID (numeric); required for App auth"`
	InstallationID int64  `required:"false" arg:"installation-id" env:"INSTALLATION_ID"  usage:"GitHub App Installation ID (numeric)"`
	PEMKeyFile     string `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"     usage:"Path to the GitHub App private key (PEM file mounted from k8s Secret)"`
	PEMKey         string `required:"false" arg:"pem-key"         env:"PEM_KEY"          usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE" display:"length"`
	BotLogin       string `required:"false" arg:"bot-login"       env:"BOT_GITHUB_LOGIN" usage:"Bot identity used by githubposter (e.g. ben-s-pull-request-reviewer[bot])"                              default:"ben-s-pull-request-reviewer[bot]"`

	// Repo allowlist — comma-separated host/owner/repo entries; empty means allow-all.
	RepoAllowlist string `required:"false" arg:"repo-allowlist" env:"REPO_ALLOWLIST" usage:"Comma-separated host-qualified repo allowlist (host/owner/repo); empty means allow-all"`

	// SkipPost suppresses all GitHub write calls; review is written to the task file only.
	SkipPost bool `required:"false" arg:"skip-post" env:"SKIP_POST" usage:"Suppress all GitHub write calls; review is written to the task file only" default:"false"`

	// Anthropic-compatible provider routing. Setting AnthropicBaseURL + AnthropicAuthToken
	// routes the claude CLI to an alt-provider (e.g. MiniMax via https://api.minimax.io/anthropic).
	// AnthropicModel drives both the `--model` CLI flag and the ANTHROPIC_MODEL env var seen by
	// the claude subprocess.
	AnthropicBaseURL   string                `required:"false" arg:"anthropic-base-url"   env:"ANTHROPIC_BASE_URL"   usage:"Anthropic-compatible API base URL"`
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"length"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                  default:"sonnet"`

	// Alias→model overrides forwarded to the claude subprocess so spawned sub-agents
	// (opus/sonnet/haiku/fable) resolve on non-Anthropic endpoints (e.g. DeepSeek/vLLM).
	AnthropicDefaultOpusModel   string `required:"false" arg:"anthropic-default-opus-model"   env:"ANTHROPIC_DEFAULT_OPUS_MODEL"   usage:"Model the 'opus' alias maps to (forwarded to the claude subprocess)"`
	AnthropicDefaultSonnetModel string `required:"false" arg:"anthropic-default-sonnet-model" env:"ANTHROPIC_DEFAULT_SONNET_MODEL" usage:"Model the 'sonnet' alias maps to (forwarded to the claude subprocess)"`
	AnthropicDefaultHaikuModel  string `required:"false" arg:"anthropic-default-haiku-model"  env:"ANTHROPIC_DEFAULT_HAIKU_MODEL"  usage:"Model the 'haiku' alias maps to (forwarded to the claude subprocess)"`
	AnthropicDefaultFableModel  string `required:"false" arg:"anthropic-default-fable-model"  env:"ANTHROPIC_DEFAULT_FABLE_MODEL"  usage:"Model the 'fable' alias maps to (forwarded to the claude subprocess)"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	if err := prpkg.ValidateReviewMaxDuration(ctx, a.MaxReviewDuration); err != nil {
		return err
	}
	glog.V(2).Infof("review max duration=%s", a.MaxReviewDuration)

	repoAllowlist, err := prpkg.ParseRepoAllowlist(ctx, a.RepoAllowlist)
	if err != nil {
		return err
	}
	// Warn on malformed entries; allow-all and wildcard semantics handled by IsAllowed at match time.
	if validationErr := repoallowlist.Validate(ctx, repoAllowlist); validationErr != nil {
		glog.Warningf(
			"REPO_ALLOWLIST contains malformed entries (will be ignored at match time): %v",
			validationErr,
		)
	}
	glog.V(2).Infof("repo-allowlist count=%d", len(repoAllowlist))

	taskContent, err := os.ReadFile(
		a.TaskFilePath,
	) // #nosec G304 -- filePath from trusted CLI input
	if err != nil {
		return errors.Wrapf(ctx, err, "read task file: %s", a.TaskFilePath)
	}

	reposPath, workPath, err := a.resolveCachePaths(ctx)
	if err != nil {
		return err
	}

	deliverer := factory.CreateFileResultDeliverer(a.TaskFilePath)

	resolvedToken, err := a.resolveAuth(ctx)
	if err != nil {
		return err
	}
	if a.SkipPost {
		glog.V(2).Infof("pr-reviewer skip-post enabled — GitHub writes suppressed")
	}

	authSetup := githubauth.NewNoopAuthSetup()
	result, err := factory.RunAgent(ctx, factory.RunConfig{
		ClaudeConfigDir:             a.ClaudeConfigDir,
		AgentDir:                    a.AgentDir,
		Model:                       a.AnthropicModel,
		GHToken:                     resolvedToken,
		AnthropicBaseURL:            a.AnthropicBaseURL,
		AnthropicAuthToken:          a.AnthropicAuthToken,
		AnthropicDefaultOpusModel:   a.AnthropicDefaultOpusModel,
		AnthropicDefaultSonnetModel: a.AnthropicDefaultSonnetModel,
		AnthropicDefaultHaikuModel:  a.AnthropicDefaultHaikuModel,
		AnthropicDefaultFableModel:  a.AnthropicDefaultFableModel,
		ReposPath:                   reposPath,
		WorkPath:                    workPath,
		ReviewMode:                  a.ReviewMode,
		MaxReviewDuration:           a.MaxReviewDuration,
		RepoAllowlist:               repoAllowlist,
		AuthSetup:                   authSetup,
		Phase:                       a.Phase,
		TaskContent:                 string(taskContent),
		Deliverer:                   deliverer,
		BotLogin:                    a.BotLogin,
		SkipPost:                    a.SkipPost,
	})
	if err != nil {
		return errors.Wrap(ctx, err, "agent run failed")
	}
	return agentlib.PrintResult(ctx, result)
}

// resolveAuth mints a GitHub App installation token and returns it. The token
// is a runtime value (not a config input), so it is returned to the caller
// rather than stored on the argument-parsed application struct.
func (a *application) resolveAuth(ctx context.Context) (string, error) {
	hasPEMFile := a.PEMKeyFile != ""
	hasPEMContent := a.PEMKey != ""
	useGitHubApp := a.AppID != 0 && a.InstallationID != 0 && (hasPEMFile || hasPEMContent)
	if !useGitHubApp {
		return "", errors.Errorf(
			ctx,
			"pr-reviewer auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)",
		)
	}
	appCfg := githubapp.Config{AppID: a.AppID, InstallationID: a.InstallationID}
	if hasPEMFile {
		appCfg.PEMPath = a.PEMKeyFile
	} else {
		appCfg.PEM = []byte(a.PEMKey)
	}
	iat, err := githubapp.MintIAT(ctx, appCfg)
	if err != nil {
		return "", errors.Wrap(ctx, err, "mint github app iat")
	}
	glog.V(2).Infof(
		"pr-reviewer auth mode=github-app app_id=%d installation_id=%d",
		a.AppID, a.InstallationID,
	)
	return iat, nil
}

// resolveCachePaths fills in defaults for ReposPath/WorkPath when unset
// (~/.cache/maintainer/pr-reviewer/{repos,work}). The pod entry point requires
// explicit /repos and /work mounts, but local CLI usage benefits from a default.
func (a *application) resolveCachePaths(ctx context.Context) (string, string, error) {
	reposPath := a.ReposPath
	workPath := a.WorkPath
	if reposPath != "" && workPath != "" {
		return reposPath, workPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", errors.Wrap(ctx, err, "resolve user home dir")
	}
	if reposPath == "" {
		reposPath = filepath.Join(home, ".cache", "maintainer", "pr-reviewer", "repos")
	}
	if workPath == "" {
		workPath = filepath.Join(home, ".cache", "maintainer", "pr-reviewer", "work")
	}
	return reposPath, workPath, nil
}
