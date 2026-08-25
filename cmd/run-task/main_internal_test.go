// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"

	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the task-type dispatch on the local CLI
// entry point. They exercise the real dispatch path (CreateAgentProvider is
// pure composition — no claude run). They run under the Ginkgo suite
// bootstrapped in main_test.go, the same pattern as the repo-root
// main_internal_test.go.
var _ = Describe("run-task task type dispatch", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("routes a security-review task to the security agent", func() {
		app := &application{}
		agent, err := app.dispatchAgent(
			ctx,
			"---\ntask_type: security-review\nassignee: security-reviewer-agent\n---\n# Security Review\n",
			nil,
			"token",
			"/tmp/repos",
			"/tmp/work",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).NotTo(BeNil())
	})

	It("defaults a task without task_type to the security agent", func() {
		app := &application{}
		agent, err := app.dispatchAgent(
			ctx,
			"---\nclone_url: git@github.com:owner/repo.git\n---\n# Security Review\n",
			nil, "token", "/tmp/repos", "/tmp/work",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).NotTo(BeNil())
	})

	It("refuses a pr-review task (never run as a plain review)", func() {
		app := &application{}
		agent, err := app.dispatchAgent(
			ctx,
			"---\ntask_type: pr-review\n---\n# PR Review\n",
			nil, "token", "/tmp/repos", "/tmp/work",
		)
		Expect(err).To(HaveOccurred())
		Expect(agent).To(BeNil())
	})

	It("refuses a pr-override task", func() {
		app := &application{}
		agent, err := app.dispatchAgent(
			ctx,
			"---\ntask_type: pr-override\n---\n# PR Override\n",
			nil, "token", "/tmp/repos", "/tmp/work",
		)
		Expect(err).To(HaveOccurred())
		Expect(agent).To(BeNil())
	})

	It("forwards provider env configuration when set", func() {
		app := &application{
			AnthropicBaseURL:            "https://api.example.com/anthropic",
			AnthropicAuthToken:          "secret-token",
			AnthropicModel:              claudelib.ClaudeModel("opus"),
			AnthropicDefaultOpusModel:   "opus-1",
			AnthropicDefaultSonnetModel: "sonnet-1",
			AnthropicDefaultHaikuModel:  "haiku-1",
			AnthropicDefaultFableModel:  "fable-1",
			BotLogin:                    "bot[bot]",
		}
		agent, err := app.dispatchAgent(
			ctx,
			"---\ntask_type: security-review\n---\n# Security Review\n",
			nil, "token", "/tmp/repos", "/tmp/work",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).NotTo(BeNil())
	})
})
