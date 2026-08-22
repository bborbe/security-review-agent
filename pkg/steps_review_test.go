// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"fmt"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExtractVerdict", func() {
	DescribeTable("parses verdict from various LLM response shapes",
		func(input, wantVerdict, wantReason string, wantOK bool) {
			got, err := pkg.ExtractVerdictForTest(input)
			if !wantOK {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Verdict).To(Equal(wantVerdict))
			Expect(got.Reason).To(Equal(wantReason))
		},

		Entry("raw JSON object",
			`{"verdict":"pass","reason":"all good"}`,
			"pass", "all good", true),

		Entry("JSON with leading + trailing whitespace",
			"\n\n  {\"verdict\":\"fail\",\"reason\":\"bad\"}  \n",
			"fail", "bad", true),

		Entry("JSON wrapped in ```json fence",
			"```json\n{\"verdict\":\"pass\",\"reason\":\"x\"}\n```",
			"pass", "x", true),

		Entry("JSON wrapped in plain ``` fence",
			"```\n{\"verdict\":\"fail\",\"reason\":\"y\"}\n```",
			"fail", "y", true),

		Entry(
			"prose before JSON (Claude commentary)",
			"All three checks pass:\n\n1. Concerns addressed\n2. No hallucinations\n3. Consistent\n\n{\"verdict\":\"pass\",\"reason\":\"all good\"}",
			"pass",
			"all good",
			true,
		),

		Entry("prose before AND after JSON",
			"Reasoning here.\n\n{\"verdict\":\"pass\",\"reason\":\"ok\"}\n\nFurther explanation.",
			"pass", "ok", true),

		Entry("multiple JSON-like fragments — picks the last balanced block",
			"Ignored: {\"foo\":\"bar\"}\n\nFinal: {\"verdict\":\"fail\",\"reason\":\"z\"}",
			"fail", "z", true),

		Entry("nested objects in the verdict JSON are preserved",
			"```json\n{\"verdict\":\"fail\",\"reason\":\"nested\",\"detail\":{\"a\":1}}\n```",
			"fail", "nested", true),

		Entry("empty string fails",
			"", "", "", false),

		Entry("prose only without any JSON fails",
			"This is just prose with no braces.", "", "", false),

		Entry("malformed JSON with unbalanced braces fails",
			"oops {{{", "", "", false),
	)

	It("populates Hallucinations from the JSON 'hallucinations' key", func() {
		input := `{"verdict":"fail","reason":"line not in diff","hallucinations":[` +
			`{"file":"pkg/foo.go","line":99,"issue":"line 99 not in diff"},` +
			`{"file":"pkg/bar.go","line":7,"issue":"line 7 not in diff"}` +
			`]}`
		got, err := pkg.ExtractVerdictForTest(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Verdict).To(Equal("fail"))
		Expect(got.Hallucinations).To(HaveLen(2))
		Expect(got.Hallucinations[0].File).To(Equal("pkg/foo.go"))
		Expect(got.Hallucinations[0].Line).To(Equal(99))
		Expect(got.Hallucinations[0].Issue).To(Equal("line 99 not in diff"))
		Expect(got.Hallucinations[1].File).To(Equal("pkg/bar.go"))
	})

	It("leaves Hallucinations empty when the JSON omits the key", func() {
		got, err := pkg.ExtractVerdictForTest(`{"verdict":"pass","reason":"ok"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Hallucinations).To(BeEmpty())
	})
})

var _ = Describe("reviewStep", func() {
	var (
		ctx          context.Context
		runner       *mocks.ClaudeRunnerMock
		poster       *mocks.PrPoster
		step         agentlib.Step
		instructions claudelib.Instructions
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		poster = &mocks.PrPoster{}
		instructions = claudelib.Instructions{}
		step = pkg.NewReviewStep(
			runner,
			poster,
			instructions,
			nil,
			"",
			"",
			libtime.Duration(25*time.Minute),
		)
	})

	Describe("Name", func() {
		It("returns the step name", func() {
			Expect(step.Name()).To(Equal("pr-ai-review"))
		})
	})

	Describe("ShouldRun", func() {
		// ShouldRun always returns true. Idempotency for the "## Verdict
		// already present" case is enforced inside Run (skip claude, publish
		// NextPhase=done). The previous skip-via-ShouldRun guard silently
		// dropped the routing decision on retrigger.
		DescribeTable("always returns true so the routing decision is never skipped",
			func(content string) {
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.ShouldRun(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			},
			Entry("no verdict section", "# PR Review\n\nsome text"),
			Entry("verdict section present", "# PR Review\n\n## Verdict\n\npass"),
			Entry("empty content", ""),
		)
	})

	Describe("Run — retrigger with existing ## Verdict (advance without claude)", func() {
		// Same pattern as planning/execution: skip the claude call but still
		// publish NextPhase=done so the controller closes out the task.
		It("publishes NextPhase=done without invoking the runner", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

## Verdict

prior verdict body
`)
			Expect(err).NotTo(HaveOccurred())
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			Expect(runner.RunCallCount()).To(Equal(0))
		})
	})

	Describe("Run", func() {
		var md *agentlib.Markdown

		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, "# Task\n\nsome content")
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when Claude runner returns an error", func() {
			BeforeEach(func() {
				runner.RunReturns(nil, fmt.Errorf("claude CLI failed"))
			})

			It("returns AgentStatusFailed result without propagating the error", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			})
		})

		Context("when Claude runner returns unparseable output", func() {
			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{Result: "this is not json at all"}, nil)
			})

			It("returns AgentStatusDone with NextPhase human_review", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
			})
		})

		Context("when Claude runner returns verdict: pass", func() {
			BeforeEach(func() {
				runner.RunReturns(
					&claudelib.ClaudeResult{Result: `{"verdict":"pass","reason":"looks good"}`},
					nil,
				)
			})

			It("returns AgentStatusDone with NextPhase done", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("done"))
				Expect(result.Message).To(Equal("looks good"))
			})
		})

		Context("when Claude runner returns verdict: fail", func() {
			BeforeEach(func() {
				runner.RunReturns(
					&claudelib.ClaudeResult{Result: `{"verdict":"fail","reason":"issues found"}`},
					nil,
				)
			})

			It("returns AgentStatusDone with NextPhase human_review", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
			})
		})
	})

	Describe("verification behavior", func() {
		const prURL = "https://github.com/bborbe/maintainer/pull/2"
		const passVerdict = `{"verdict":"pass","reason":"all checks pass"}`

		var verifier *mocks.ReviewVerifier

		BeforeEach(func() {
			verifier = &mocks.ReviewVerifier{}
			poster = &mocks.PrPoster{}
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				verifier,
				"test-token",
				"test-bot",
				libtime.Duration(25*time.Minute),
			)
			runner.RunReturns(&claudelib.ClaudeResult{Result: passVerdict}, nil)
		})

		Context("skip verification when ## Review is absent", func() {
			It("does not call verifier; meta-verdict routes normally", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nref: abc123\n---\n\nReview the PR at "+prURL+"\n\nsome content",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(verifier.VerifyReviewCallCount()).To(Equal(0))
			})
		})

		Context("skip verification when Diagnostics shows class: permanent", func() {
			It("does not call verifier", func() {
				diagBody := "```yaml\nclass: permanent\n```\n"
				content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n" +
					"## Review\n\nsome content\n\n" +
					"## Diagnostics\n\n" + diagBody
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				_, err = step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(verifier.VerifyReviewCallCount()).To(Equal(0))
			})
		})

		Context("skip verification when Diagnostics shows class: unknown", func() {
			It("does not call verifier", func() {
				diagBody := "```yaml\nclass: unknown\n```\n"
				content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n" +
					"## Review\n\nsome content\n\n" +
					"## Diagnostics\n\n" + diagBody
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				_, err = step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(verifier.VerifyReviewCallCount()).To(Equal(0))
			})
		})

		Context("verification runs and succeeds", func() {
			BeforeEach(func() {
				verifier.VerifyReviewReturns(pkg.VerifyResult{
					Found:      true,
					Outcome:    "success",
					FoundState: "APPROVED",
				})
			})

			It("calls verifier once and routes based on meta-verdict", func() {
				diagBody := "```yaml\nclass: transient\n```\n"
				content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n" +
					"## Review\n\nsome content\n\n" +
					"## Diagnostics\n\n" + diagBody
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(verifier.VerifyReviewCallCount()).To(Equal(1))
				// meta-verdict is "pass" → done
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("done"))
			})

			It("passes ExpectedStates without COMMENTED (spec 060 regression guard)", func() {
				content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n" +
					"## Review\n\nsome content\n\n" +
					"## Diagnostics\n\n" + "```yaml\nclass: transient\n```\n"
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				_, err = step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(verifier.VerifyReviewCallCount()).To(Equal(1))
				_, verifyReq := verifier.VerifyReviewArgsForCall(0)
				Expect(verifyReq.ExpectedStates).To(ConsistOf("APPROVED", "CHANGES_REQUESTED"))
				Expect(verifyReq.ExpectedStates).NotTo(ContainElement("COMMENTED"))
			})
		})

		Context("verification runs and fails", func() {
			BeforeEach(func() {
				verifier.VerifyReviewReturns(pkg.VerifyResult{
					Found:        false,
					Outcome:      "failed",
					Class:        pkg.ErrorClassTransient,
					EscalateHint: false,
					HTTPStatus:   0,
					ErrorMessage: "review not found",
				})
			})

			It("exits with AgentStatusFailed and writes ai_review verify diagnostic", func() {
				content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n" +
					"## Review\n\nsome content\n"
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("post verification failed"))
				// Diagnostics section should contain the ai_review verify line
				diagSection, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeTrue())
				Expect(diagSection).NotTo(BeNil())
				Expect(diagSection.Body).To(ContainSubstring("ai_review verify:"))
				Expect(diagSection.Body).To(ContainSubstring("review not found"))
			})
		})

		Context("nil verifier skips verification without panic", func() {
			It("routes normally", func() {
				step = pkg.NewReviewStep(
					runner,
					poster,
					instructions,
					nil,
					"",
					"",
					libtime.Duration(25*time.Minute),
				)
				content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n" +
					"## Review\n\nsome content\n"
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			})
		})
	})

	Describe("soft time budget expiry", func() {
		// AC 2: a budget-terminated ai_review run routes to human_review with a
		// budget-naming message BEFORE writing ## Verdict or posting — never to
		// the failed/controller-retry path.
		It("routes to human_review without writing ## Verdict or posting", func() {
			runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
				<-runCtx.Done() // block until the soft budget deadline fires
				return nil, runCtx.Err()
			}
			budgetStep := pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				"",
				libtime.Duration(20*time.Millisecond),
			)
			md, err := agentlib.ParseMarkdown(ctx, "# Task\n\nsome content")
			Expect(err).NotTo(HaveOccurred())

			result, err := budgetStep.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(result.Message).To(ContainSubstring("soft time budget"))
			Expect(result.Message).To(ContainSubstring("20ms"))
			// Budget-terminated runs never write ## Verdict and never post.
			_, exists := md.FindSection("## Verdict")
			Expect(exists).To(BeFalse())
			Expect(poster.PostCallCount()).To(Equal(0))
		})

		Context("salvage", func() {
			// AC 5: a budget-terminated ai_review run with a captured streamed
			// partial persists it under the distinct ## Salvage heading (never
			// ## Verdict) and never posts.
			It("persists the captured partial under ## Salvage and never posts", func() {
				runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
					<-runCtx.Done() // block until the soft budget deadline fires
					// A killed run returns the bounded streamed partial alongside the
					// fired-deadline error — the capture shape ExtractBudgetPartial reads.
					return &claudelib.ClaudeResult{Partial: "partial verdict output"}, runCtx.Err()
				}
				budgetStep := pkg.NewReviewStep(
					runner,
					poster,
					instructions,
					nil,
					"",
					"",
					libtime.Duration(20*time.Millisecond),
				)
				md, err := agentlib.ParseMarkdown(ctx, "# Task\n\nsome content")
				Expect(err).NotTo(HaveOccurred())

				result, err := budgetStep.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
				// The partial is persisted under ## Salvage, clearly marked incomplete.
				section, exists := md.FindSection("## Salvage")
				Expect(exists).To(BeTrue())
				Expect(section.Body).To(ContainSubstring("Incomplete"))
				Expect(section.Body).To(ContainSubstring("partial verdict output"))
				// ## Verdict is never written and nothing is ever posted on the budget path.
				_, exists = md.FindSection("## Verdict")
				Expect(exists).To(BeFalse())
				Expect(poster.PostCallCount()).To(Equal(0))
			})

			// AC 5 negative: an empty capture must not produce a ## Salvage section —
			// the salvage write is a no-op, yet the run still routes to human_review.
			It("writes no ## Salvage when the run captured nothing", func() {
				runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
					<-runCtx.Done() // block until the soft budget deadline fires
					return nil, runCtx.Err()
				}
				budgetStep := pkg.NewReviewStep(
					runner,
					poster,
					instructions,
					nil,
					"",
					"",
					libtime.Duration(20*time.Millisecond),
				)
				md, err := agentlib.ParseMarkdown(ctx, "# Task\n\nsome content")
				Expect(err).NotTo(HaveOccurred())

				result, err := budgetStep.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
				Expect(result.Message).To(ContainSubstring("soft time budget"))
				_, exists := md.FindSection("## Salvage")
				Expect(exists).To(BeFalse())
			})
		})
	})
})

var _ = Describe("dismiss-and-comment routing", func() {
	var (
		ctx          context.Context
		runner       *mocks.ClaudeRunnerMock
		poster       *mocks.PrPoster
		step         agentlib.Step
		instructions claudelib.Instructions
	)

	const (
		prURL    = "https://github.com/bborbe/maintainer/pull/2"
		headSHA  = "abc123def456abc123def456abc123def456abc1"
		botLogin = "ben-s-pull-request-reviewer[bot]"
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		poster = &mocks.PrPoster{}
		instructions = claudelib.Instructions{}
	})

	Describe(
		"case (a): verdict=fail with hallucinations → dismiss called, routes to human_review",
		func() {
			It("calls DismissCurrentReview with hallucinations and routes to human_review", func() {
				verdictJSON := `{"verdict":"fail","reason":"line 99 not in diff","hallucinations":[{"file":"pkg/foo.go","line":99,"issue":"line 99 not in diff"}]}`
				runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
				poster.DismissCurrentReviewReturns(pkg.PostResult{
					Outcome:     "success",
					FailureStep: "",
					HTTPStatus:  200,
				})
				step = pkg.NewReviewStep(
					runner,
					poster,
					instructions,
					nil,
					"",
					botLogin,
					libtime.Duration(25*time.Minute),
				)
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content",
				)
				Expect(err).NotTo(HaveOccurred())

				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))

				Expect(poster.DismissCurrentReviewCallCount()).To(Equal(1))
				_, _, _, gotHallucinations := poster.DismissCurrentReviewArgsForCall(0)
				Expect(gotHallucinations).To(HaveLen(1))
				Expect(gotHallucinations[0].File).To(Equal("pkg/foo.go"))
				Expect(gotHallucinations[0].Line).To(Equal(99))
				// Diagnostics contains dismiss outcome
				diagSec, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeTrue())
				Expect(diagSec.Body).To(ContainSubstring(`outcome: "success"`))
			})
		},
	)

	Describe("case (b): verdict=fail with hallucinations + dismiss returns 404", func() {
		It("routes to human_review with 404 in diagnostics", func() {
			verdictJSON := `{"verdict":"fail","reason":"issues","hallucinations":[{"file":"pkg/foo.go","line":1,"issue":"nothere"}]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			poster.DismissCurrentReviewReturns(pkg.PostResult{
				Outcome:     "failed",
				FailureStep: "PUT /pulls/2/reviews/77/dismissals",
				HTTPStatus:  404,
			})
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))

			diagSec, _ := md.FindSection("## Diagnostics")
			Expect(diagSec.Body).To(ContainSubstring("PUT /pulls/2/reviews/77/dismissals"))
			Expect(diagSec.Body).To(ContainSubstring("http_status: 404"))
		})
	})

	Describe("case (c): verdict=fail with hallucinations + dismiss returns 422", func() {
		It("routes to human_review with 422 in diagnostics", func() {
			verdictJSON := `{"verdict":"fail","reason":"issues","hallucinations":[{"file":"x.go","line":1,"issue":"a"}]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			poster.DismissCurrentReviewReturns(pkg.PostResult{
				Outcome:     "failed",
				FailureStep: "PUT /pulls/2/reviews/77/dismissals",
				HTTPStatus:  422,
			})
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))

			diagSec, _ := md.FindSection("## Diagnostics")
			Expect(diagSec.Body).To(ContainSubstring("http_status: 422"))
		})
	})

	Describe(
		"case (d): verdict=fail with hallucinations + dismiss success + COMMENT POST fails (partial)",
		func() {
			It("routes to human_review with comment-after-dismiss step in diagnostics", func() {
				verdictJSON := `{"verdict":"fail","reason":"issues","hallucinations":[{"file":"x.go","line":1,"issue":"a"}]}`
				runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
				poster.DismissCurrentReviewReturns(pkg.PostResult{
					Outcome:     "success",
					FailureStep: "POST /pulls/2/reviews (comment-after-dismiss)",
					HTTPStatus:  500,
				})
				step = pkg.NewReviewStep(
					runner,
					poster,
					instructions,
					nil,
					"",
					botLogin,
					libtime.Duration(25*time.Minute),
				)
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content",
				)
				Expect(err).NotTo(HaveOccurred())

				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("human_review"))

				diagSec, _ := md.FindSection("## Diagnostics")
				Expect(diagSec.Body).To(ContainSubstring("comment-after-dismiss"))
			})
		},
	)

	Describe("case (e): verdict=fail with empty hallucinations → dismiss NOT called", func() {
		It("does not call DismissCurrentReview and routes to human_review", func() {
			verdictJSON := `{"verdict":"fail","reason":"inconsistent","hallucinations":[]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(poster.DismissCurrentReviewCallCount()).To(Equal(0))

			_, exists := md.FindSection("## Diagnostics")
			Expect(exists).To(BeFalse())
		})
	})

	Describe("case (f): verdict=pass → poster not called", func() {
		It("routes to done, poster never called", func() {
			verdictJSON := `{"verdict":"pass","reason":"looks good","hallucinations":[]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("done"))
			Expect(poster.DismissCurrentReviewCallCount()).To(Equal(0))
		})
	})

	Describe("case (g): non-GitHub PR URL → dismiss skipped", func() {
		It("does not call DismissCurrentReview", func() {
			verdictJSON := `{"verdict":"fail","reason":"issues","hallucinations":[{"file":"x.go","line":1,"issue":"a"}]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			// Bitbucket URL in preamble
			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: "+headSHA+"\n---\n\nReview the PR at https://bitbucket.org/org/repo/pull-requests/1\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(poster.DismissCurrentReviewCallCount()).To(Equal(0))
		})
	})

	Describe("case (h): empty ref frontmatter → dismiss skipped", func() {
		It("does not call DismissCurrentReview", func() {
			verdictJSON := `{"verdict":"fail","reason":"issues","hallucinations":[{"file":"x.go","line":1,"issue":"a"}]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			md, err := agentlib.ParseMarkdown(ctx,
				"---\n---\n\nReview the PR at "+prURL+"\n\nsome content")
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(poster.DismissCurrentReviewCallCount()).To(Equal(0))
		})
	})

	Describe("case (i): no PR URL in preamble → dismiss skipped", func() {
		It("does not call DismissCurrentReview", func() {
			verdictJSON := `{"verdict":"fail","reason":"issues","hallucinations":[{"file":"x.go","line":1,"issue":"a"}]}`
			runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
			step = pkg.NewReviewStep(
				runner,
				poster,
				instructions,
				nil,
				"",
				botLogin,
				libtime.Duration(25*time.Minute),
			)
			// Preamble has no GitHub PR URL at all — neither GitHub nor Bitbucket
			md, err := agentlib.ParseMarkdown(ctx,
				"---\nref: "+headSHA+"\n---\n\nReview this PR — link missing\n\nsome content")
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(poster.DismissCurrentReviewCallCount()).To(Equal(0))
		})
	})

	Describe(
		"case (j): nil poster + verdict=fail with hallucinations → routes to human_review without panic",
		func() {
			It("does not call DismissCurrentReview and routes to human_review", func() {
				verdictJSON := `{"verdict":"fail","reason":"hallucinated","hallucinations":[{"file":"x.go","line":1,"issue":"nothere"}]}`
				runner.RunReturns(&claudelib.ClaudeResult{Result: verdictJSON}, nil)
				// nil poster — SkipPost mode
				step = pkg.NewReviewStep(
					runner,
					nil,
					instructions,
					nil,
					"",
					botLogin,
					libtime.Duration(25*time.Minute),
				)
				md, err := agentlib.ParseMarkdown(ctx,
					"---\nref: "+headSHA+"\n---\n\nReview the PR at "+prURL+"\n\nsome content")
				Expect(err).NotTo(HaveOccurred())

				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("human_review"))
			})
		},
	)

	// Note: the "regex matches but ParsePRURL fails" branch in tryDismissHallucinated
	// is defensive — any string that matches githubPRURLPattern
	// (`https://github\.com/[^/\s]+/[^/\s]+/pull/\d+`) is by construction parseable
	// by parseGitHub (4 path segments, non-empty owner/repo, "pull" keyword, numeric ID).
	// No realistic input can reach the parse-error branch, so it stays uncovered by
	// design — kept as a belt-and-suspenders guard for future regex changes.
})

var _ = Describe("shouldVerifyPost", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	const prURL = "https://github.com/bborbe/maintainer/pull/2"

	taskWithReview := func(diagBody string) *agentlib.Markdown {
		content := "---\nref: abc123\n---\n\nReview the PR at " + prURL + "\n\n## Review\n\nsome content\n"
		if diagBody != "" {
			content += "\n## Diagnostics\n\n" + diagBody
		}
		md, err := agentlib.ParseMarkdown(context.Background(), content)
		Expect(err).NotTo(HaveOccurred())
		return md
	}

	Describe("handles ## Diagnostics absent", func() {
		It("returns true — verification should run", func() {
			md := taskWithReview("")
			result, err := pkg.ShouldVerifyPostForTest(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})

	Describe("selects the MOST RECENT diagnostic block when multiple exist", func() {
		It(
			"returns true when last block has class: transient despite older class: permanent",
			func() {
				diagBody := "```yaml\ntrigger_count: 0\nclass: permanent\n```\n\n" +
					"```yaml\ntrigger_count: 1\nclass: transient\n```\n"
				md := taskWithReview(diagBody)
				result, err := pkg.ShouldVerifyPostForTest(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			},
		)
	})

	Describe("diagnostic format round-trip with prompt 2's exact output", func() {
		It(
			"parses class: permanent from buildDiagnosticBlock output and skips verification",
			func() {
				// Exact format produced by buildDiagnosticBlock for a failure:
				// fmt.Sprintf("```yaml\njob_run: %s\ntrigger_count: %d\n...class: %s\n...```\n", ...)
				diagBody := "```yaml\n" +
					"job_run: 2026-01-01T00:00:00Z\n" +
					"trigger_count: 1\n" +
					"outcome: failed\n" +
					"failure_step: POST /pulls/2/reviews\n" +
					"class: permanent\n" +
					"escalate_hint: true\n" +
					"attempt: 1\n" +
					"http_status: 403\n" +
					"error_message: \"forbidden\"\n" +
					"response_body: \"{}\"\n" +
					"elapsed_ms: 100\n" +
					"```\n"
				md := taskWithReview(diagBody)
				result, err := pkg.ShouldVerifyPostForTest(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeFalse())
			},
		)
	})

	Describe("returns false when ## Review is absent", func() {
		It("skips verification without error", func() {
			md, err := agentlib.ParseMarkdown(ctx, "# Task\n\nsome content")
			Expect(err).NotTo(HaveOccurred())
			result, err := pkg.ShouldVerifyPostForTest(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})
	})

	Describe("returns false when last block has class: unknown", func() {
		It("skips verification", func() {
			diagBody := "```yaml\nclass: unknown\n```\n"
			md := taskWithReview(diagBody)
			result, err := pkg.ShouldVerifyPostForTest(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})
	})

	Describe("returns true when Diagnostics has only success one-liners", func() {
		It("runs verification (no yaml block means no skip condition)", func() {
			diagBody := "job_run: 2026-01-01T00:00:00Z outcome: success review_id: 12345\n"
			md := taskWithReview(diagBody)
			result, err := pkg.ShouldVerifyPostForTest(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})
})

var _ = Describe("appendVerifyDiagnostic", func() {
	It("appends diagnostic line to ## Diagnostics section", func() {
		md, err := agentlib.ParseMarkdown(
			context.Background(),
			"---\nref: abc\n---\n\n## Review\n\nsome content\n\n## Diagnostics\n\nexisting line\n",
		)
		Expect(err).NotTo(HaveOccurred())
		result := pkg.VerifyResult{
			Class:        pkg.ErrorClassTransient,
			EscalateHint: true,
			HTTPStatus:   429,
			ErrorMessage: "rate limited",
		}
		pkg.AppendVerifyDiagnosticForTest(context.Background(), md, result)
		sec, exists := md.FindSection("## Diagnostics")
		Expect(exists).To(BeTrue())
		Expect(sec).NotTo(BeNil())
		Expect(sec.Body).To(ContainSubstring("ai_review verify:"))
		Expect(sec.Body).To(ContainSubstring("class=transient"))
		Expect(sec.Body).To(ContainSubstring("http_status=429"))
		Expect(sec.Body).To(ContainSubstring("rate limited"))
	})
})
