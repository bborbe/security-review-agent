// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command maintainer-agent-pr-reviewer is the Kafka entry point for the PR-review
// agent — spawned as a K8s Job by task/executor with TASK_CONTENT +
// TASK_ID + PHASE + KAFKA_BROKERS env. For local CLI mode (file-based),
// see cmd/run-task/main.go.
package main

import (
	"context"
	"os"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	libmetrics "github.com/bborbe/agent/metrics"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/githubauth"
	libkafka "github.com/bborbe/kafka"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"

	githubapp "github.com/bborbe/maintainer/githubapp"
	repoallowlist "github.com/bborbe/maintainer/repoallowlist"
)

const agentName = "pr-reviewer-agent"

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

	// Workdir paths for bare-clone cache and per-task worktrees
	ReposPath string `required:"false" arg:"repos-path" env:"REPOS_PATH" usage:"Root path for bare-clone cache"   default:"/repos"`
	WorkPath  string `required:"false" arg:"work-path"  env:"WORK_PATH"  usage:"Root path for per-task worktrees" default:"/work"`

	// Review depth passed to /coding:pr-review (short | standard | full)
	ReviewMode string `required:"false" arg:"review-mode" env:"REVIEW_MODE" usage:"Review depth: short | standard | full" default:"standard"`

	// MaxReviewDuration is the soft time budget for each Claude phase run
	// (planning, execution, ai_review), applied below the K8s Job hard deadline.
	// REVIEW_MAX_DURATION is validated at startup: it must parse as a duration
	// and be >= 60s. The default 25m sits below the executor's default
	// ZombieJobTimeoutSeconds (1800s) with headroom for salvage + Kafka delivery;
	// operators who lower zombieJobTimeoutSeconds must keep this below it.
	MaxReviewDuration libtime.Duration `required:"false" arg:"review-max-duration" env:"REVIEW_MAX_DURATION" usage:"Soft time budget per Claude phase run; keep below the K8s Job ActiveDeadlineSeconds / ZombieJobTimeoutSeconds (default 1800s) with headroom for salvage + Kafka delivery; must be >= 60s" default:"25m"`

	// Task content from agent pipeline
	TaskContent string `required:"true" arg:"task-content" env:"TASK_CONTENT" usage:"Raw task markdown from vault"`

	// Branch for Kafka result delivery
	Branch base.Branch `required:"true" arg:"branch" env:"BRANCH" usage:"branch"`

	// TopicPrefix selects the Kafka topic prefix used for CQRS topic construction
	// (e.g. "develop" / "master"); independent of Branch, which remains the
	// stage/image-tag identifier. Empty means unprefixed topics.
	TopicPrefix base.TopicPrefix `required:"false" arg:"topic-prefix" env:"TOPIC_PREFIX" usage:"Kafka topic prefix for CQRS topic construction"`

	// Phase to run (framework requires explicit phase)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"execution"`

	// Kafka delivery (optional — only active when TASK_ID is set)
	KafkaBrokers libkafka.Brokers        `required:"false" arg:"kafka-brokers" env:"KAFKA_BROKERS" usage:"Comma separated list of Kafka brokers"`
	TaskID       agentlib.TaskIdentifier `required:"false" arg:"task-id"       env:"TASK_ID"       usage:"Agent task identifier for publishing results back to task controller"`

	// GitHub App authentication. The pod mints an installation access token at startup
	// and forwards it to the agent subprocess (gh CLI, git credential helper, repo manager,
	// and agent provider). App auth is the only supported auth path.
	AppID          int64  `required:"false" arg:"app-id"          env:"APP_ID"           usage:"GitHub App ID (numeric); required for App auth"`
	InstallationID int64  `required:"false" arg:"installation-id" env:"INSTALLATION_ID"  usage:"GitHub App Installation ID (numeric)"`
	PEMKeyFile     string `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"     usage:"Path to the GitHub App private key (PEM file mounted from k8s Secret)"`
	PEMKey         string `required:"false" arg:"pem-key"         env:"PEM_KEY"          usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE" display:"length"`
	BotLogin       string `required:"false" arg:"bot-login"       env:"BOT_GITHUB_LOGIN" usage:"Bot identity used by githubposter (e.g. ben-s-pull-request-reviewer[bot])"                              default:"ben-s-pull-request-reviewer[bot]"`

	// Anthropic-compatible provider routing. Setting AnthropicBaseURL + AnthropicAuthToken
	// routes the claude CLI to an alt-provider (e.g. MiniMax via https://api.minimax.io/anthropic).
	// AnthropicModel drives both the `--model` CLI flag and the ANTHROPIC_MODEL env var seen by
	// the claude subprocess.
	AnthropicBaseURL   string                `required:"false" arg:"anthropic-base-url"   env:"ANTHROPIC_BASE_URL"   usage:"Anthropic-compatible API base URL"`
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"length"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                  default:"sonnet"`

	// Alias→model overrides forwarded to the claude subprocess so its spawned sub-agents
	// (which request opus/sonnet/haiku) resolve to a real model. Needed against non-Anthropic
	// endpoints (e.g. DeepSeek/vLLM) where the default aliases 404; empty = unset (no-op on Anthropic).
	AnthropicDefaultOpusModel   string `required:"false" arg:"anthropic-default-opus-model"   env:"ANTHROPIC_DEFAULT_OPUS_MODEL"   usage:"Model the 'opus' alias maps to (forwarded to the claude subprocess)"`
	AnthropicDefaultSonnetModel string `required:"false" arg:"anthropic-default-sonnet-model" env:"ANTHROPIC_DEFAULT_SONNET_MODEL" usage:"Model the 'sonnet' alias maps to (forwarded to the claude subprocess)"`
	AnthropicDefaultHaikuModel  string `required:"false" arg:"anthropic-default-haiku-model"  env:"ANTHROPIC_DEFAULT_HAIKU_MODEL"  usage:"Model the 'haiku' alias maps to (forwarded to the claude subprocess)"`
	AnthropicDefaultFableModel  string `required:"false" arg:"anthropic-default-fable-model"  env:"ANTHROPIC_DEFAULT_FABLE_MODEL"  usage:"Model the 'fable' alias maps to (forwarded to the claude subprocess)"`

	// Repo allowlist — comma-separated host/owner/repo entries; empty means allow-all.
	RepoAllowlist string `required:"false" arg:"repo-allowlist" env:"REPO_ALLOWLIST" usage:"Comma-separated host-qualified repo allowlist (host/owner/repo); empty means allow-all"`

	PushgatewayURL string `required:"false" arg:"pushgateway-url" env:"PUSHGATEWAY_URL" usage:"Prometheus PushGateway URL"          default:"http://pushgateway:9090"`
	TaskType       string `required:"false" arg:"task-type"       env:"TASK_TYPE"       usage:"Task type label for metric grouping" default:"unknown"`
}

//nolint:funlen // wires Run from validated config into RunAgent — extracting any chunk hurts readability without reducing complexity. 82 lines, 2 over the 80-line cap.
func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	registry := prometheus.NewRegistry()
	jobMetrics := libmetrics.NewJobMetrics(registry, libtime.NewCurrentDateTime())
	pusher := push.New(a.PushgatewayURL, libmetrics.BuildJobMetricsName(agentName)).
		Grouping("agent", agentName).
		Grouping("task_type", a.TaskType).
		Collector(registry)
	defer func() {
		if err := pusher.PushContext(ctx); err != nil {
			glog.Warningf("prometheus push failed: %v", err)
			return
		}
		glog.V(2).Infof("prometheus push completed")
	}()
	start := libtime.NewCurrentDateTime().Now().Time()
	glog.V(2).Infof("maintainer-agent-pr-reviewer started phase=%s", a.Phase)

	if err := prpkg.ValidateReviewMaxDuration(ctx, a.MaxReviewDuration); err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return err
	}
	glog.V(2).Infof("review max duration=%s", a.MaxReviewDuration)

	resolvedToken, err := a.resolveAuth(ctx)
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return err
	}

	repoAllowlist, err := prpkg.ParseRepoAllowlist(ctx, a.RepoAllowlist)
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
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

	deliverer, cleanup, err := a.createDeliverer(ctx)
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return err
	}
	defer cleanup()

	agent, err := a.dispatchAgent(ctx, repoAllowlist, resolvedToken)
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return errors.Wrap(ctx, err, "task type dispatch")
	}
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
		ReposPath:                   a.ReposPath,
		WorkPath:                    a.WorkPath,
		ReviewMode:                  a.ReviewMode,
		MaxReviewDuration:           a.MaxReviewDuration,
		RepoAllowlist:               repoAllowlist,
		AuthSetup:                   githubauth.NewGhAuthSetupGit(resolvedToken),
		Phase:                       a.Phase,
		BotLogin:                    a.BotLogin,
		TaskContent:                 a.TaskContent,
		Deliverer:                   deliverer,
		Agent:                       agent,
		CurrentDateTime:             libtime.NewCurrentDateTime(),
	})
	if err != nil {
		jobMetrics.RecordRun(agentlib.AgentStatusFailed)
		jobMetrics.RecordDuration(time.Since(start))
		return errors.Wrap(ctx, err, "agent run failed")
	}
	jobMetrics.RecordRun(result.Status)
	jobMetrics.RecordDuration(time.Since(start))
	return agentlib.PrintResult(ctx, result)
}

// dispatchAgent builds the correct agent for the configured task type.
// resolvedToken is the GitHub App installation token minted in resolveAuth; it
// is forwarded to the agent subprocess (gh CLI, git credential helper, repo
// manager, and agent provider) — never read from a config input.
func (a *application) dispatchAgent(
	ctx context.Context,
	repoAllowlist []string,
	resolvedToken string,
) (*agentlib.Agent, error) {
	env := map[string]string{}
	if resolvedToken != "" {
		env["GH_TOKEN"] = resolvedToken
	}
	if a.AnthropicBaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = a.AnthropicBaseURL
	}
	if a.AnthropicAuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = a.AnthropicAuthToken
	}
	if a.AnthropicModel != "" {
		env["ANTHROPIC_MODEL"] = a.AnthropicModel.String()
	}
	if a.AnthropicDefaultOpusModel != "" {
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = a.AnthropicDefaultOpusModel
	}
	if a.AnthropicDefaultSonnetModel != "" {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = a.AnthropicDefaultSonnetModel
	}
	if a.AnthropicDefaultHaikuModel != "" {
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = a.AnthropicDefaultHaikuModel
	}
	if a.AnthropicDefaultFableModel != "" {
		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = a.AnthropicDefaultFableModel
	}
	if a.BotLogin != "" {
		env["BOT_GITHUB_LOGIN"] = a.BotLogin
	}
	repoManager := git.NewRepoManager(git.WorkdirConfig{
		ReposPath: a.ReposPath,
		WorkPath:  a.WorkPath,
	}, resolvedToken)
	provider := factory.CreateAgentProvider(
		a.ClaudeConfigDir,
		a.AgentDir,
		a.AnthropicModel,
		resolvedToken,
		env,
		repoManager,
		a.ReviewMode,
		repoAllowlist,
		libtime.NewCurrentDateTime(),
		a.MaxReviewDuration,
	)
	agent, err := provider.Get(ctx, agentlib.TaskType(a.TaskType))
	if err != nil {
		return nil, errors.Wrap(ctx, err, "select agent for task_type")
	}
	return agent, nil
}

// resolveAuth mints a GitHub App installation token and returns it. The token
// is a runtime value (not a config input), so it is returned to the caller
// rather than stored on the argument-parsed application struct — argument/v2
// panics when reflecting over unexported struct fields at startup.
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
	var iat string
	var err error
	if hasPEMFile {
		iat, err = githubapp.MintIAT(ctx, githubapp.Config{
			AppID:          a.AppID,
			InstallationID: a.InstallationID,
			PEMPath:        a.PEMKeyFile,
		})
	} else {
		iat, err = githubapp.MintIAT(ctx, githubapp.Config{
			AppID:          a.AppID,
			InstallationID: a.InstallationID,
			PEM:            []byte(a.PEMKey),
		})
	}
	if err != nil {
		return "", errors.Wrap(ctx, err, "mint github app iat")
	}
	glog.V(2).Infof(
		"pr-reviewer auth mode=github-app app_id=%d installation_id=%d",
		a.AppID, a.InstallationID,
	)
	return iat, nil
}

// createDeliverer builds the Kafka result deliverer when TASK_ID is set,
// otherwise returns a noop deliverer (for local-pod debugging without Kafka).
func (a *application) createDeliverer(
	ctx context.Context,
) (agentlib.ResultDeliverer, func(), error) {
	if a.TaskID == "" {
		glog.V(2).Infof("TASK_ID not set, skipping task result publishing")
		return delivery.NewNoopResultDeliverer(), func() {}, nil
	}
	if len(a.KafkaBrokers) == 0 {
		return nil, nil, errors.Errorf(ctx, "KAFKA_BROKERS must be set when TASK_ID is set")
	}
	syncProducer, err := libkafka.NewSyncProducerWithName(ctx, a.KafkaBrokers, "agent-pr-reviewer")
	if err != nil {
		return nil, nil, errors.Wrap(ctx, err, "create kafka sync producer")
	}
	cleanup := func() {
		if err := syncProducer.Close(); err != nil {
			glog.Warningf("close sync producer failed: %v", err)
		}
	}
	currentDateTime := libtime.NewCurrentDateTime()
	deliverer := factory.CreateDeliverer(
		syncProducer,
		a.TaskID,
		a.TopicPrefix,
		a.TaskContent,
		currentDateTime,
	)
	return deliverer, cleanup, nil
}
