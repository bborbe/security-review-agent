// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

// This file protects the skip-post nil contract: ResolvePosters must return
// nil *interfaces* (not nil concrete pointers) so that the three guards that
// branch on a nil poster/verifier actually fire.  If ResolvePosters ever
// returned nil concrete pointers stored in interfaces, the interface value
// would be non-nil, all three guards would fall through, and a --skip-post
// run would either re-enable GitHub posting or issue a live API read --
// neither of which the flag is supposed to allow.
//
// The function under test (ResolvePosters) lives in
// pkg/factory; all three consumers live in pkg.  Because the seam is
// cross-package, pkg_test imports both pkg and pkg/factory and drives the
// helper's return values through the real consumer functions.

import (
	"context"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("skip-post nil contract", func() {
	const botLogin = "test-bot"

	Describe("case 1: checkoutExecutionStep.postAndRoute with nil poster", func() {
		// Drives ResolvePosters(SkipPost:true) return value through the first
		// consumer: checkoutExecutionStep.postAndRoute's `if s.prPoster == nil`
		// guard.  The nil branch returns AgentStatusDone + NextPhase=ai_review
		// before touching any other argument, so the markdown is intentionally
		// minimal.
		It("returns AgentStatusDone + ai_review without accessing other arguments", func() {
			ctx := context.Background()
			poster, _ := factory.ResolvePosters(factory.RunConfig{SkipPost: true}, botLogin)

			md, err := agentlib.ParseMarkdown(ctx, "---\nref: abc123\n---\n# PR Review\n")
			Expect(err).NotTo(HaveOccurred())

			result, err := pkg.PostAndRouteForTest(
				ctx,
				poster,
				md,
				"",          // prURLStr -- not read when poster is nil
				"",          // worktreePath -- not read when poster is nil
				time.Time{}, // jobRunTime -- not read when poster is nil
				false,       // funnelRan -- not read when poster is nil
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
		})
	})

	Describe("case 2: reviewStep with nil poster and fail+hallucinations verdict", func() {
		// Drives ResolvePosters(SkipPost:true) return value through the second
		// consumer: reviewStep.tryDismissHallucinated's `if s.poster == nil`
		// guard.  A nil poster must not cause a panic; the guard skips the
		// dismiss call and routes to human_review.
		It("does not panic and routes to human_review", func() {
			ctx := context.Background()
			poster, _ := factory.ResolvePosters(factory.RunConfig{SkipPost: true}, botLogin)

			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunReturns(
				&claudelib.ClaudeResult{
					Result: "{\"verdict\":\"fail\",\"reason\":\"lines not in diff\",\"hallucinations\":[{\"file\":\"pkg/foo.go\",\"line\":99,\"issue\":\"line 99 not in diff\"}]}",
				},
				nil,
			)

			step := pkg.NewReviewStep(
				fakeRunner,
				poster,
				claudelib.Instructions{},
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)

			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: abc123\n---\nReview the PR at https://github.com/bborbe/maintainer/pull/2\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))
		})
	})

	Describe("case 3: reviewStep with nil poster, non-nil verifier, and pass verdict", func() {
		// Drives the nil poster from ResolvePosters through the third consumer:
		// the `s.verifier != nil && shouldVerify` guard in reviewStep.Run that
		// gates callVerifier -- the one that would issue a live GitHub API read
		// if the guard ever fell through.  We use a real mock verifier so we
		// can assert on VerifyReviewCallCount; the poster is the one from
		// ResolvePosters to prove the nil-interface contract.
		//
		// Three short-circuits must all be cleared for this test to prove
		// anything meaningful:
		//   1. ## Review section must be present -- otherwise shouldVerifyPost
		//      returns false and the `s.verifier != nil && shouldVerify` guard
		//      is never evaluated.
		//   2. A GitHub PR URL in the preamble -- otherwise callVerifier returns
		//      early (before reaching s.verifier.VerifyReview).
		//   3. No ## Verdict section -- otherwise Run early-returns and skips the
		//      whole verify block.
		//
		// This fixture mirrors the existing "verification runs and succeeds"
		// case in steps_review_test.go, which already satisfies all three.
		It("clears all three short-circuits and calls the verifier", func() {
			ctx := context.Background()
			poster, _ := factory.ResolvePosters(factory.RunConfig{SkipPost: true}, botLogin)

			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunReturns(
				&claudelib.ClaudeResult{
					Result: "{\"verdict\":\"pass\",\"reason\":\"all checks pass\"}",
				},
				nil,
			)

			fakeVerifier := &mocks.ReviewVerifier{}
			fakeVerifier.VerifyReviewReturns(pkg.VerifyResult{
				Found:      true,
				Outcome:    "success",
				FoundState: "APPROVED",
			})

			// Markdown must have ## Review (short-circuit 1) and a GitHub PR URL
			// in the preamble (short-circuit 2).  No ## Verdict (short-circuit 3).
			diagBody := "```yaml\nclass: transient\n```\n"
			content := "---\nref: abc123\n---\n\n" +
				"Review the PR at https://github.com/bborbe/maintainer/pull/2\n\n" +
				"## Review\n\nsome content\n\n" +
				"## Diagnostics\n\n" + diagBody
			md, err := agentlib.ParseMarkdown(ctx, content)
			Expect(err).NotTo(HaveOccurred())

			step := pkg.NewReviewStep(
				fakeRunner,
				poster,
				claudelib.Instructions{},
				fakeVerifier,
				"test-token",
				botLogin,
				libtime.Duration(25*time.Minute),
			)

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			// callVerifier was called (verifier != nil && shouldVerify passed)
			Expect(fakeVerifier.VerifyReviewCallCount()).To(Equal(1))
			// pass verdict + verifier found -> done
			Expect(result.NextPhase).To(Equal("done"))
		})
	})

	Describe(
		"case 4: reviewStep with suppressed verifier (nil interface from ResolvePosters)",
		func() {
			// Protects against a regression where the nil concrete pointer stored
			// inside the ReviewVerifier interface satisfies `s.verifier != nil`,
			// reaches callVerifier, and turns a no-posting run into a live GitHub
			// read.  Both poster and verifier are the real values returned by
			// ResolvePosters so the interface-typed nil contract is exercised end-
			// to-end.
			//
			// Same three short-circuits as case 3 are cleared so the verify guard
			// is evaluated.  The pass verdict routes to done whether or not the
			// verify block ran, so the negative signal is: no ai_review verify line
			// appears in ## Diagnostics.
			It("completes without error and does not append a verify diagnostic", func() {
				ctx := context.Background()
				poster, verifier := factory.ResolvePosters(
					factory.RunConfig{SkipPost: true},
					botLogin,
				)

				fakeRunner := &mocks.ClaudeRunnerMock{}
				fakeRunner.RunReturns(
					&claudelib.ClaudeResult{
						Result: "{\"verdict\":\"pass\",\"reason\":\"all checks pass\"}",
					},
					nil,
				)

				diagBody := "```yaml\nclass: transient\n```\n"
				content := "---\nref: abc123\n---\n\n" +
					"Review the PR at https://github.com/bborbe/maintainer/pull/2\n\n" +
					"## Review\n\nsome content\n\n" +
					"## Diagnostics\n\n" + diagBody
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())

				step := pkg.NewReviewStep(
					fakeRunner,
					poster,
					claudelib.Instructions{},
					verifier,
					"test-token",
					botLogin,
					libtime.Duration(25*time.Minute),
				)

				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("done"))

				// No ai_review verify line must be present — if callVerifier were
				// reached despite the suppressed verifier, it would have appended one.
				diagSection, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeTrue())
				Expect(diagSection.Body).NotTo(ContainSubstring("ai_review verify:"))
			})
		},
	)
})
