// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

// This file locks the no-posting contract of the security agent: the factory
// wires a nil poster and a nil verifier into the 3-phase agent, so the run
// ends at the ## Review (and ## Verdict) write-back and nothing is ever
// published to a GitHub PR. The steps below are constructed exactly as
// pkg/factory.CreateAgent constructs them (nil poster / nil verifier) and
// driven through the full Run paths.

import (
	"context"
	"os"
	"path/filepath"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/security-review-agent/mocks"
	pkg "github.com/bborbe/security-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("security agent no-posting contract", func() {
	const (
		botLogin   = "test-bot"
		prURL      = "https://github.com/bborbe/maintainer/pull/14"
		reviewBody = "LGTM — no security concerns.\n\n{\"verdict\":\"approve\",\"reason\":\"no security concerns\"}"
	)

	// securityPluginDir builds a temp CLAUDE_CONFIG_DIR whose
	// plugins/marketplaces/coding/commands/pr-review.md declares --security.
	// The execution step's full Run path calls BuildExecutionInstructions,
	// which reads this file; the --security declaration is required by the
	// security-review contract (a later prompt probes it).
	securityPluginDir := func() string {
		tmpDir, err := os.MkdirTemp("", "security-nopost-*")
		Expect(err).NotTo(HaveOccurred())
		cmdDir := filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
		Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
		Expect(os.WriteFile(
			filepath.Join(cmdDir, "pr-review.md"),
			[]byte(
				"---\ndescription: Test plugin\nargument-hint: \"<target-branch> [short|full|selector] [--security]\"\nallowed-tools: Task\n---\n# PR Review\n\nReview the PR diff with `--security`.\n",
			),
			0600,
		)).To(Succeed())
		return tmpDir
	}

	Describe("execution phase with nil poster (AC4)", func() {
		// The security agent's execution step is built with a nil prPoster, so
		// postAndRoute's `if s.prPoster == nil` guard fires: ## Review is written
		// to the vault and the run routes to ai_review without any GitHub API
		// call. The step is constructed exactly as the factory does — nil
		// repoAllowlist, nil prPoster, nil funnelRunner, injected fake runner.
		It("writes ## Review and routes to ai_review without posting", func() {
			ctx := context.Background()
			repoManagerFake := &mocks.RepoManager{}
			repoManagerFake.EnsureWorktreeReturns("/work/test", nil)

			tmpDir := securityPluginDir()
			defer func() {
				Expect(os.RemoveAll(tmpDir)).To(Succeed())
			}()

			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunReturns(
				&claudelib.ClaudeResult{Result: reviewBody},
				nil,
			)

			step := pkg.NewCheckoutExecutionStep(
				repoManagerFake,
				claudelib.ClaudeConfigDir(tmpDir),
				"agent",
				"sonnet",
				map[string]string{},
				claudelib.AllowedTools{"Read"},
				"standard",
				nil, // repoAllowlist — the security agent reviews any allowlisted-elsewhere repo
				nil, // prPoster — the security agent wires no poster
				nil, // funnelRunner — the security agent runs the funnel itself
				libtime.NewCurrentDateTime(),
				fakeRunner,
				libtime.Duration(25*time.Minute),
			)

			md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

`+prURL+`
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))

			reviewSection, exists := md.FindSection("## Review")
			Expect(exists).To(BeTrue())
			Expect(reviewSection.Body).To(Equal(reviewBody))

			// No poster-verdict block: the nil-poster guard returns before any
			// diagnostics/posting block is appended.
			_, exists = md.FindSection("## Diagnostics")
			Expect(exists).To(BeFalse())
		})
	})

	Describe("ai_review phase with nil verifier (AC4)", func() {
		// The security agent's ai_review step is built with a nil verifier, so
		// the `s.verifier != nil && shouldVerify` guard never evaluates
		// callVerifier: ## Verdict is written and the run routes to done with no
		// GitHub verification read. The ## Diagnostics block carries class:
		// transient so shouldVerifyPost is genuinely evaluated (mirroring
		// skip_post_boundary_test.go case-4) — the negative signal is that no
		// `ai_review verify:` line is appended.
		It("writes ## Verdict and routes to done without verification", func() {
			ctx := context.Background()

			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunReturns(
				&claudelib.ClaudeResult{Result: `{"verdict":"pass","reason":"all checks pass"}`},
				nil,
			)

			// Held but never wired into the step — locks the "no verifier called"
			// behavior of the security agent's ai_review phase.
			fakeVerifier := &mocks.ReviewVerifier{}

			step := pkg.NewReviewStep(
				fakeRunner,
				nil, // poster — the security agent wires no poster
				claudelib.Instructions{},
				nil, // verifier — the security agent wires no verifier
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)

			diagBody := "```yaml\nclass: transient\n```\n"
			content := "---\nref: abc123\n---\n\n" +
				"Review the PR at " + prURL + "\n\n" +
				"## Review\n\nsome content\n\n" +
				"## Diagnostics\n\n" + diagBody
			md, err := agentlib.ParseMarkdown(ctx, content)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))

			// The verifier was never called and no verification diagnostic was
			// appended — the run ended at the ## Verdict write-back.
			Expect(fakeVerifier.VerifyReviewCallCount()).To(Equal(0))
			diagSection, exists := md.FindSection("## Diagnostics")
			Expect(exists).To(BeTrue())
			Expect(diagSection.Body).NotTo(ContainSubstring("ai_review verify:"))
		})
	})
})
