// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("planningStep", func() {
	var (
		ctx    context.Context
		runner *mocks.ClaudeRunnerMock
		step   agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		step = pkg.NewPlanningStep(
			runner,
			claudelib.Instructions{},
			libtime.Duration(25*time.Minute),
		)
	})

	Describe("Name", func() {
		It("returns pr-plan", func() {
			Expect(step.Name()).To(Equal("pr-plan"))
		})
	})

	Describe("ShouldRun", func() {
		// ShouldRun now always returns true. Idempotency for the "## Plan
		// already present" case is enforced inside Run (skip claude, re-route
		// from existing body). The previous "skip step if ## Plan present"
		// behaviour silently dropped the routing decision on retrigger —
		// see the trading#136 incident (2026-05-25).
		DescribeTable("always returns true so the routing decision is never skipped",
			func(content string) {
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.ShouldRun(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			},
			Entry("no plan section", "# PR Review\n\nsome text"),
			Entry("plan section present", "# PR Review\n\n## Plan\n\n{}"),
			Entry("empty content", ""),
		)
	})

	Describe("Run — empty concerns path (execution, no LGTM shortcut)", func() {
		var md *agentlib.Markdown

		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())
			planBody, _ := json.Marshal(map[string]interface{}{
				"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
				"pr_title":      "test PR",
				"base_branch":   "main",
				"head_branch":   "feat/test",
				"files_changed": []string{"README.md"},
				"scope":         "docs",
				"focus_areas":   []string{"docs"},
				"concerns":      []interface{}{},
			})
			runner.RunReturns(&claudelib.ClaudeResult{
				Result: "```json\n" + string(planBody) + "\n```",
			}, nil)
		})

		// The LGTM shortcut is gone: a "no concerns" planning pass must NOT
		// rubber-stamp a positive review. Empty concerns routes to the execution
		// phase (real checkout + deep review) exactly like non-empty concerns.
		It("routes empty concerns to execution (no LGTM shortcut)", func() {
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal(string(domain.TaskPhaseExecution)))
		})

		It("writes ## Plan section with the LLM output", func() {
			_, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			planSection, exists := md.FindSection("## Plan")
			Expect(exists).To(BeTrue())
			Expect(planSection.Body).To(ContainSubstring("concerns"))
		})

		It("does NOT write a ## Verdict section (planning never posts)", func() {
			_, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			_, exists := md.FindSection("## Verdict")
			Expect(exists).To(BeFalse())
		})
	})

	Describe("Run — non-empty concerns path (execution)", func() {
		var md *agentlib.Markdown

		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when ## Plan has non-empty concerns", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"pkg/auth/handler.go"},
					"scope":         "feature",
					"focus_areas":   []string{"security"},
					"concerns": []map[string]string{
						{
							"area": "security",
							"file": "pkg/auth/handler.go",
							"note": "missing rate limit",
						},
					},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			It(
				"returns status done with NextPhase execution (canonical phase per spec 032)",
				func() {
					result, err := step.Run(ctx, md)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
					// Boundary contract: the value emitted here must equal the canonical
					// domain.TaskPhase constant — string-typed because agentlib.Result.NextPhase
					// is plain string. Reverting to "in_progress" causes the agentlib frontmatter
					// validator to reject the write at delivery time (spec 035 root cause).
					Expect(result.NextPhase).To(Equal(string(domain.TaskPhaseExecution)))
				},
			)

			It("does NOT write ## Verdict section", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				_, exists := md.FindSection("## Verdict")
				Expect(exists).To(BeFalse())
			})

			It("does NOT append diagnostics", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				_, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeFalse())
			})
		})
	})

	Describe("Run — retrigger with existing ## Plan (re-route without claude)", func() {
		// Reproduces the trading#136 incident: a previous trigger wrote
		// ## Plan with concerns then execution failed → controller reset
		// trigger_count → new pod runs planning → with the old skip-via-
		// ShouldRun behaviour the step was skipped entirely, NextPhase
		// ended up empty, and the task short-circuited to phase: done
		// without any review running. The fix is to always run the step
		// but skip the claude call when ## Plan is already present.

		buildMarkdownWithExistingPlan := func(concerns []map[string]string) *agentlib.Markdown {
			planJSON, _ := json.Marshal(map[string]interface{}{
				"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
				"pr_title":      "test PR",
				"base_branch":   "main",
				"head_branch":   "feat/test",
				"files_changed": []string{"pkg/x.go"},
				"scope":         "feature",
				"focus_areas":   []string{"tests"},
				"concerns":      concerns,
			})
			content := "---\nref: abc123\ntask_identifier: 00000000-0000-0000-0000-000000000001\n---\n" +
				"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n\n" +
				"## Plan\n\n```json\n" + string(
				planJSON,
			) + "\n```\n"
			md, err := agentlib.ParseMarkdown(ctx, content)
			Expect(err).NotTo(HaveOccurred())
			return md
		}

		It("does NOT call the claude runner when ## Plan already exists", func() {
			md := buildMarkdownWithExistingPlan([]map[string]string{
				{"area": "tests", "file": "pkg/x.go", "note": "missing coverage"},
			})
			_, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(runner.RunCallCount()).To(Equal(0))
		})

		It("routes non-empty concerns to execution from existing plan", func() {
			md := buildMarkdownWithExistingPlan([]map[string]string{
				{"area": "tests", "file": "pkg/x.go", "note": "missing coverage"},
			})
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))
		})

		It("routes empty concerns to execution from existing plan (no LGTM shortcut)", func() {
			md := buildMarkdownWithExistingPlan(nil)
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))
		})
	})

	Describe("Run — in-agent retry on malformed JSON", func() {
		// happy markdown with valid PR URL — reused across all sub-cases
		buildBaseMarkdown := func() *agentlib.Markdown {
			md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())
			return md
		}

		goodPlanBody := func() string {
			planBody, _ := json.Marshal(map[string]interface{}{
				"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
				"pr_title":      "test PR",
				"base_branch":   "main",
				"head_branch":   "feat/test",
				"files_changed": []string{"README.md"},
				"scope":         "docs",
				"focus_areas":   []string{"docs"},
				"concerns":      []interface{}{},
			})
			return "```json\n" + string(planBody) + "\n```"
		}

		Context("attempt 1 succeeds", func() {
			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{Result: goodPlanBody()}, nil)
			})

			It("calls runner exactly once and returns done", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(runner.RunCallCount()).To(Equal(1))
			})

			It("writes ## Plan section with the valid response", func() {
				md := buildBaseMarkdown()
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				planSection, exists := md.FindSection("## Plan")
				Expect(exists).To(BeTrue())
				Expect(planSection.Body).To(ContainSubstring("concerns"))
			})
		})

		Context("attempt 2 succeeds", func() {
			BeforeEach(func() {
				// First call: malformed (no JSON)
				runner.RunReturnsOnCall(0, &claudelib.ClaudeResult{
					Result: "Based on the diff, here is the plan...",
				}, nil)
				// Second call: valid JSON
				runner.RunReturnsOnCall(1, &claudelib.ClaudeResult{Result: goodPlanBody()}, nil)
			})

			It("retries and persists the second (valid) response", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).NotTo(Equal(agentlib.AgentStatusFailed))
				Expect(runner.RunCallCount()).To(Equal(2))
				planSection, exists := md.FindSection("## Plan")
				Expect(exists).To(BeTrue())
				Expect(planSection.Body).To(ContainSubstring("concerns"))
			})
		})

		Context("all 3 attempts fail", func() {
			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "Based on the diff, here is the plan...",
				}, nil)
			})

			It("returns AgentStatusFailed after exhausting all attempts", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("malformed JSON after 3 attempts"))
				Expect(runner.RunCallCount()).To(Equal(3))
				_, exists := md.FindSection("## Plan")
				Expect(exists).To(BeFalse())
			})
		})

		Context("runner transport error not retried", func() {
			BeforeEach(func() {
				runner.RunReturns(nil, context.DeadlineExceeded)
			})

			It("fails immediately without retrying", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(runner.RunCallCount()).To(Equal(1))
			})
		})

		Context("idempotent re-entry — runner not called", func() {
			buildMarkdownWithExistingPlan := func(concerns []map[string]string) *agentlib.Markdown {
				planJSON, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"pkg/x.go"},
					"scope":         "feature",
					"focus_areas":   []string{"tests"},
					"concerns":      concerns,
				})
				content := "---\nref: abc123\ntask_identifier: 00000000-0000-0000-0000-000000000001\n---\n" +
					"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n\n" +
					"## Plan\n\n```json\n" + string(
					planJSON,
				) + "\n```\n"
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				return md
			}

			It("skips the runner entirely when ## Plan already exists", func() {
				md := buildMarkdownWithExistingPlan(nil)
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(runner.RunCallCount()).To(Equal(0))
			})
		})
	})

	Describe("Run — error cases", func() {
		Context("when ## Plan JSON is malformed", func() {
			var md *agentlib.Markdown

			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "not valid json at all",
				}, nil)
				var err error
				md, err = agentlib.ParseMarkdown(
					ctx,
					"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n",
				)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns AgentStatusFailed without writing ## Plan", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("planning: malformed JSON"))
				_, exists := md.FindSection("## Plan")
				Expect(exists).To(BeFalse())
			})
		})

		Context(
			"when Claude returns a ## Plan body containing unescaped double quotes (live bug vault-cli#27)",
			func() {
				BeforeEach(func() {
					// The exact class of failure observed on 2026-06-26: a Go zero-string-check
					// `name != ""` embedded literally inside a JSON string value. The outer
					// quote parser closes the string at the first `"`, leaving `!= ` as
					// unexpected tokens.
					liveSample := "```json\n" +
						"{\"pr_url\":\"https://github.com/bborbe/vault-cli/pull/27\",\"pr_title\":\"fix\",\"base_branch\":\"main\",\"head_branch\":\"fix/args\",\"files_changed\":[\"cmd/main.go\"],\"scope\":\"bugfix\",\"focus_areas\":[\"correctness\"],\"concerns\":[{\"area\":\"correctness\",\"file\":\"cmd/main.go\",\"note\":\"Arg order matters: name != \"\" must appear after --print\"}]}" +
						"\n```"
					runner.RunReturns(&claudelib.ClaudeResult{Result: liveSample}, nil)
				})

				It("returns AgentStatusFailed and does not write ## Plan", func() {
					md, err := agentlib.ParseMarkdown(
						ctx,
						"---\nref: abc123\ntask_identifier: 00000000-0000-0000-0000-000000000001\n---\n# PR Review\n\nhttps://github.com/bborbe/vault-cli/pull/27\n",
					)
					Expect(err).NotTo(HaveOccurred())
					result, err := step.Run(ctx, md)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
					Expect(result.Message).To(ContainSubstring("planning: malformed JSON"))
					_, exists := md.FindSection("## Plan")
					Expect(exists).To(BeFalse())
				})
			},
		)

		Context("when Claude runner returns an error", func() {
			BeforeEach(func() {
				runner.RunReturns(nil, context.DeadlineExceeded)
			})

			It("returns AgentStatusFailed", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			})
		})

		Context("when PR URL is absent from task", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			It("returns human_review when PR URL missing", func() {
				md, err := agentlib.ParseMarkdown(ctx, "# PR Review\n")
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("human_review"))
				Expect(result.Message).To(ContainSubstring("no GitHub PR URL"))
			})
		})

		Context("when non-GitHub platform (no GitHub PR URL)", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://bitbucket.org/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			// A non-GitHub URL yields no GitHub PR URL, so the review can't run —
			// escalate to human_review (no LGTM shortcut, no silent done).
			It("escalates to human_review (not reviewable)", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"# PR Review\n\nhttps://bitbucket.org/bborbe/maintainer/pull/14\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("human_review"))
			})
		})
	})

	Describe("soft time budget expiry", func() {
		// AC 2: a budget-terminated run routes to human_review with a
		// budget-naming message, never to the failed/controller-retry path,
		// is never retried, and never writes ## Plan.
		It("routes to human_review with a budget-naming message", func() {
			runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
				<-runCtx.Done() // block until the soft budget deadline fires
				return nil, runCtx.Err()
			}
			budgetStep := pkg.NewPlanningStep(
				runner,
				claudelib.Instructions{},
				libtime.Duration(20*time.Millisecond),
			)
			md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := budgetStep.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(result.Message).To(ContainSubstring("soft time budget"))
			Expect(result.Message).To(ContainSubstring("20ms"))
			// Budget-terminated runs are not retried and never write ## Plan.
			Expect(runner.RunCallCount()).To(Equal(1))
			_, exists := md.FindSection("## Plan")
			Expect(exists).To(BeFalse())
		})

		It("routes a run that expires mid-retry-loop immediately (no retry)", func() {
			runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
				<-runCtx.Done() // block until the soft budget deadline fires
				return nil, runCtx.Err()
			}
			budgetStep := pkg.NewPlanningStep(
				runner,
				claudelib.Instructions{},
				libtime.Duration(20*time.Millisecond),
			)
			md, err := agentlib.ParseMarkdown(
				ctx,
				"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n",
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := budgetStep.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
			// The retry loop must NOT retry a budget-terminated run.
			Expect(runner.RunCallCount()).To(Equal(1))
		})

		Context("salvage", func() {
			// AC 5: a budget-terminated run with a captured streamed partial
			// persists it under the distinct ## Salvage heading (never ## Plan),
			// still routing done + human_review.
			It(
				"persists the captured partial under ## Salvage when the run is killed at the budget",
				func() {
					runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
						<-runCtx.Done() // block until the soft budget deadline fires
						// A killed run returns the bounded streamed partial alongside the
						// fired-deadline error — the capture shape ExtractBudgetPartial reads.
						return &claudelib.ClaudeResult{
							Partial: "partial planning output",
						}, runCtx.Err()
					}
					budgetStep := pkg.NewPlanningStep(
						runner,
						claudelib.Instructions{},
						libtime.Duration(20*time.Millisecond),
					)
					md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
					Expect(err).NotTo(HaveOccurred())

					result, err := budgetStep.Run(ctx, md)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
					Expect(result.NextPhase).To(Equal("human_review"))
					// The partial is persisted under ## Salvage, clearly marked incomplete.
					section, exists := md.FindSection("## Salvage")
					Expect(exists).To(BeTrue())
					Expect(section.Body).To(ContainSubstring("Incomplete"))
					Expect(section.Body).To(ContainSubstring("partial planning output"))
					// The normal section is never written on the budget path.
					_, exists = md.FindSection("## Plan")
					Expect(exists).To(BeFalse())
				},
			)

			// AC 5 negative: an empty capture must not produce a ## Salvage section —
			// the salvage write is a no-op, yet the run still routes to human_review.
			It("writes no ## Salvage when the run captured nothing", func() {
				runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
					<-runCtx.Done() // block until the soft budget deadline fires
					return nil, runCtx.Err()
				}
				budgetStep := pkg.NewPlanningStep(
					runner,
					claudelib.Instructions{},
					libtime.Duration(20*time.Millisecond),
				)
				md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
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

		Context("negative row — non-budget failure keeps the failed path", func() {
			// Precise-detection contract: only a FIRED run-context deadline
			// routes to human_review. A runner that crashes immediately (deadline
			// not fired) keeps the existing failed/controller-retry path.
			It(
				"returns AgentStatusFailed (not human_review) when the deadline never fired",
				func() {
					runner.RunReturns(nil, stderrors.New("claude CLI crashed"))
					md, err := agentlib.ParseMarkdown(
						ctx,
						"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n",
					)
					Expect(err).NotTo(HaveOccurred())
					result, err := step.Run(ctx, md)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
					Expect(result.NextPhase).To(BeEmpty())
					Expect(result.Message).To(ContainSubstring("claude CLI crashed"))
					_, exists := md.FindSection("## Plan")
					Expect(exists).To(BeFalse())
				},
			)
		})
	})

	Describe("parsePlanningConcerns", func() {
		DescribeTable(
			"extracts concerns array from various JSON wrapping",
			func(body, want string) {
				concerns, err := pkg.ParsePlanningConcernsForTest(body)
				if want == "error" {
					Expect(err).To(HaveOccurred())
					return
				}
				Expect(err).NotTo(HaveOccurred())
				if want == "empty" {
					Expect(concerns).To(BeEmpty())
				} else {
					Expect(concerns).NotTo(BeEmpty())
				}
			},
			Entry("bare JSON array", `{"concerns":[]}`, "empty"),
			Entry("json fence", "```json\n{\"concerns\":[]}\n```", "empty"),
			Entry(
				"non-empty concerns",
				"```json\n{\"concerns\":[{\"area\":\"security\"}]}\n```",
				"non-empty",
			),
			Entry("malformed JSON", "not json at all", "error"),
			// DeepSeek (vLLM) prepends conversational prose before the ```json
			// fence; real Anthropic emits clean JSON. The extractor must find the
			// JSON regardless of surrounding narration. See octopus runbook Gate 7.
			Entry(
				"prose before json fence (DeepSeek)",
				"Now I have the full picture. Let me assemble the plan.\n\n```json\n{\"concerns\":[{\"area\":\"correctness\"}]}\n```",
				"non-empty",
			),
			Entry(
				"prose before json fence, empty concerns",
				"This is a documentation-only PR. Let me assemble the plan.\n\n```json\n{\"concerns\":[]}\n```",
				"empty",
			),
			Entry(
				"prose before bare json object (no fence)",
				"Here is the plan:\n{\"concerns\":[]}",
				"empty",
			),
			Entry(
				"prose before and after json fence",
				"Analysis complete.\n```json\n{\"concerns\":[]}\n```\nThat's my assessment.",
				"empty",
			),
		)
	})
})
