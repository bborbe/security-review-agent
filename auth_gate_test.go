// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"

	agentlib "github.com/bborbe/agent"
	prpkg "github.com/bborbe/security-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the task-type gate on GitHub App
// credential resolution. They run under the Ginkgo suite bootstrapped in
// main_test.go (same pattern as auth_resolve_test.go): Ginkgo v2.32.1 rejects a
// second RunSpecs call in one binary, so no suite is registered here.
//
// Every spec is hermetic. The application literals carry no App credentials, so
// resolveAuth fails before githubapp.MintIAT (a live HTTP mint) is reached; the
// positive GH_TOKEN direction uses a literal token that is never minted.
var _ = Describe("application.resolveAuth task-type gate", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns an empty token and no error for build-fix with no App credentials", func() {
		app := &application{TaskType: "build-fix"}
		token, err := app.resolveAuthIfRequired(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(BeEmpty())
	})

	It("returns an empty token and no error for healthcheck with no App credentials", func() {
		app := &application{TaskType: "healthcheck"}
		token, err := app.resolveAuthIfRequired(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(BeEmpty())
	})

	It("still fails fast for security-review with no App credentials", func() {
		app := &application{TaskType: "security-review"}
		token, err := app.resolveAuthIfRequired(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(token).To(BeEmpty())
	})

	It("still fails fast when the task type is unset", func() {
		app := &application{TaskType: ""}
		token, err := app.resolveAuthIfRequired(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(token).To(BeEmpty())
	})

	It("still fails fast when the task type is unknown", func() {
		app := &application{TaskType: "unknown"}
		token, err := app.resolveAuthIfRequired(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(token).To(BeEmpty())
	})

	DescribeTable(
		"requiresRepositoryAccess",
		func(taskType agentlib.TaskType, expected bool) {
			Expect(requiresRepositoryAccess(taskType)).To(Equal(expected))
		},
		Entry("security-review acts on a repository", prpkg.TaskTypeSecurityReview, true),
		Entry("healthcheck does not", agentlib.TaskTypeHealthcheck, false),
		Entry("build-fix does not", prpkg.TaskTypeBuildFix, false),
		Entry("an unset task type takes the stricter branch", agentlib.TaskType(""), true),
		Entry("an unknown task type takes the stricter branch", agentlib.TaskType("unknown"), true),
		Entry(
			"a refused task type takes the stricter branch",
			agentlib.TaskType("pr-review"),
			true,
		),
	)

	It("omits GH_TOKEN from the agent env when no token was resolved", func() {
		app := &application{TaskType: "build-fix"}
		token, err := app.resolveAuthIfRequired(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(BeEmpty())
		agent, err := app.dispatchAgent(ctx, nil, token)
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).NotTo(BeNil())
		Expect(app.agentEnv(token)).NotTo(HaveKey("GH_TOKEN"))
	})

	It("forwards the Anthropic and bot-login settings into the agent env", func() {
		//nolint:gosec // G101: test-only sentinel values, not real credentials
		app := &application{
			TaskType:                    "build-fix",
			AnthropicBaseURL:            "https://example.invalid/anthropic",
			AnthropicAuthToken:          "literal-auth-token",
			AnthropicModel:              "sonnet",
			AnthropicDefaultOpusModel:   "opus-model",
			AnthropicDefaultSonnetModel: "sonnet-model",
			AnthropicDefaultHaikuModel:  "haiku-model",
			AnthropicDefaultFableModel:  "fable-model",
			BotLogin:                    "ben-s-pull-request-reviewer[bot]",
		}
		env := app.agentEnv("")
		Expect(env).NotTo(HaveKey("GH_TOKEN"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_BASE_URL", "https://example.invalid/anthropic"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_AUTH_TOKEN", "literal-auth-token"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_MODEL", "sonnet"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_DEFAULT_OPUS_MODEL", "opus-model"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_DEFAULT_SONNET_MODEL", "sonnet-model"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_DEFAULT_HAIKU_MODEL", "haiku-model"))
		Expect(env).To(HaveKeyWithValue("ANTHROPIC_DEFAULT_FABLE_MODEL", "fable-model"))
		Expect(env).To(HaveKeyWithValue("BOT_GITHUB_LOGIN", "ben-s-pull-request-reviewer[bot]"))
	})

	It("sets GH_TOKEN in the agent env when a token was resolved", func() {
		app := &application{TaskType: "build-fix"}
		Expect(app.agentEnv("literal-token")).To(HaveKeyWithValue("GH_TOKEN", "literal-token"))
		agent, err := app.dispatchAgent(ctx, nil, "literal-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).NotTo(BeNil())
	})
})
