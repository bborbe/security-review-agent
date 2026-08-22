---
status: rejected
originalStatus: failed
spec: [002-pr-reviewer-soft-time-budget-and-salvage]
execution_id: github-pr-review-agent-salvage-exec-008-spec-002-salvage
dark-factory-version: dev
created: "2026-08-21T08:07:20Z"
queued: "2026-08-21T10:14:18Z"
started: "2026-08-21T11:04:09Z"
completed: "2026-08-21T13:01:58Z"
branch: dark-factory/pr-reviewer-soft-time-budget-and-salvage
lastFailReason: 'setup workflow: git merge origin default branch: merge origin/master: : git merge origin/master failed: exit status 1'
rejected: "2026-08-21T13:04:49Z"
rejectedReason: 'Superseded: core write needs ExtractBudgetPartial from dep-bump (009) which was numbered after this prompt; idempotency lock already shipped as commit b870ccc. Rejected to unblock the dependency ordering; salvage will be re-created after 009.'
---

# Salvage the streamed partial review into a ## Salvage task section on budget expiry

<summary>
- When a phase run is terminated at the soft time budget, the streamed partial review text captured by the claude runner is now persisted into the task file under a `## Salvage` section
- The salvage section is clearly marked as incomplete and is deliberately distinct from `## Review`, so the `## Review`-present idempotency guard can never treat a salvaged partial as a completed review
- A budget-terminated run with nothing streamed writes no salvage section and still routes to `human_review` cleanly
- A salvaged partial never posts to GitHub — the budget path of the execution phase never reaches the poster
- Unit tests lock the behavior: a runner returning a partial capture plus a kill error produces a `## Salvage` section holding the partial text in every phase, and the advance-if-already-reviewed guard returns nil for a task holding only `## Salvage`
- A regression test asserts a completed `## Review` still advances to ai_review exactly as before
</summary>

<objective>
Persist the bounded streamed partial review text captured on budget expiry into the task file under `## Salvage` — a heading clearly marked incomplete and distinct from `## Review` — so a budget-terminated run never dies with a blank task, and a partial can never be mistaken for a completed review or posted to GitHub.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, test style).

Read `docs/architecture.md` — the "Three Phases" table (each phase writes its section) and the "Result Delivery" section (the framework serializes the mutated `Markdown` after each step's `Run`).

Read these files fully:
- `pkg/soft_budget.go` — `runWithSoftBudget` (created by the budget-routing prompt in this batch) returns `(result, err, budgetExpired)`; the budget branches now exist in all three steps and this prompt fills in their salvage write.
- `pkg/claude_partial.go` — `ExtractBudgetPartial(result *claudelib.ClaudeResult, runErr error) string` (created by the dependency-bump prompt in this batch). READ THIS FILE to see the exact capture shape it reads (field/error type), then construct the fake-runner returns in the tests to match it.
- `pkg/steps_planning.go` — the planning budget branch (add the salvage write before returning the budget result).
- `pkg/steps_checkout_execution.go` — `advanceIfAlreadyReviewed` (the `## Review`-present guard this prompt's AC 6 tests target) and the execution budget branch in `runClaude` (must write `## Salvage` and still never reach `postAndRoute`).
- `pkg/steps_review.go` — the ai_review budget branch.
- `pkg/export_test.go` — the test-only export pattern (e.g. `PostAndRouteForTest`, `ShouldVerifyPostForTest`) for the new `AdvanceIfAlreadyReviewedForTest`.
- `pkg/steps_planning_test.go`, `pkg/steps_checkout_execution_test.go`, `pkg/steps_review_test.go` — the soft-time-budget-expiry blocks added by the budget-routing prompt (the salvage tests extend them).
- `pkg/prompts/execution_test.go` — the `writePlugin` fake-plugin pattern the execution-step salvage test reuses.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — integration-style tests, Counterfeiter mocks
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Verified contracts (do not re-derive):
- `agentlib "github.com/bborbe/agent"` — `type Markdown struct { Frontmatter TaskFrontmatter; Preamble string; Sections []Section }`; `func (m *Markdown) FindSection(heading string) (*Section, bool)`; `func (m *Markdown) ReplaceSection(section Section)`; `type Section struct { Heading string; Body string }`.
- `ExtractBudgetPartial` may return `""` — the salvage write must be a no-op then.
- The budget branch messages already name the budget and route `done` + `NextPhase=human_review` (from the budget-routing prompt); this prompt does NOT change them.
</context>

<requirements>
1. **Add the shared salvage writer.** Create `pkg/salvage.go` (package `pkg`):
   ```go
   // SalvageHeading is the task-file section heading under which a budget-terminated
   // run's streamed partial review text is persisted. It is deliberately distinct
   // from "## Review" so the ## Review-present idempotency guard in the execution
   // step can never treat a salvaged partial as a completed review — a partial can
   // never advance into ai_review on a later trigger.
   const SalvageHeading = "## Salvage"

   // salvageMarker introduces a ## Salvage section as clearly incomplete, never a
   // verdict.
   const salvageMarker = "_Incomplete: this run was terminated at the soft time budget before producing a final result. Salvaged partial output below._\n\n"

   // writeSalvage persists partial under ## Salvage when it is non-empty. No-op for
   // an empty capture so a budget-terminated run with nothing streamed still routes
   // cleanly. Persisted verbatim as markdown text — never executed or interpreted.
   func writeSalvage(md *agentlib.Markdown, partial string)
   ```
   The body trims `partial`; if empty, return without touching `md`; otherwise `md.ReplaceSection(agentlib.Section{Heading: SalvageHeading, Body: salvageMarker + partial})`. Imports: `strings`, `agentlib "github.com/bborbe/agent"`.

2. **Salvage in the planning step.** In `pkg/steps_planning.go`, in the planning budget branch (the `if budgetExpired` block added by the budget-routing prompt), immediately before the `return &agentlib.Result{...}` line, add:
   ```go
   writeSalvage(md, ExtractBudgetPartial(runResult, runErr))
   ```
   The `md` parameter is in scope (`planningStep.Run(ctx, md *agentlib.Markdown)`). Do not write `## Plan` on the budget path.

3. **Salvage in the execution step.** In `pkg/steps_checkout_execution.go` `runClaude`, in the execution budget branch, add the same line before the return:
   ```go
   writeSalvage(md, ExtractBudgetPartial(runResult, runErr))
   ```
   The budget branch still returns BEFORE `md.ReplaceSection(## Review)` and BEFORE `s.postAndRoute` — a salvaged partial is never posted to GitHub. Do not write `## Review` on the budget path.

4. **Salvage in the ai_review step.** In `pkg/steps_review.go`, in the ai_review budget branch, add the same line before the return. Do not write `## Verdict` on the budget path.

5. **AC 5 tests — a partial capture plus a kill error persists `## Salvage` in every phase.** Extend each step's existing "soft time budget expiry" test block (from the budget-routing prompt) with a new `Context("salvage")` case whose fake runner BLOCKS until the deadline then returns a capture shape that `ExtractBudgetPartial` reads:
   ```go
   runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
   	<-runCtx.Done()
   	// Return the capture shape ExtractBudgetPartial reads — read pkg/claude_partial.go
   	// and construct its input exactly (the same shape the dependency-bump prompt
   	// implemented against the released agent API).
   	return <captureResult>, <killErr>
   }
   ```
   Use a short budget (e.g. `libtime.Duration(20 * time.Millisecond)`). After `step.Run(ctx, md)`, assert:
   - `section, exists := md.FindSection("## Salvage")` → `exists` true and `section.Body` contains the partial text and the incomplete marker (e.g. `ContainSubstring("Incomplete")`).
   - the step's normal section is absent (`## Plan` / `## Review` / `## Verdict` not found), and the result still routes `done` + `human_review`.
   - execution step: assert `poster.PostCallCount() == 0` (the budget path never posts — AC 6).
   The execution-step case needs the full-path setup from the budget-routing prompt (tmp `claudeConfigDir` with a fake `pr-review.md` plugin, `repoManager.EnsureWorktreeReturns("/work/test", nil)`, `funnelRunner` nil, injected fake runner, md with the four required frontmatter fields + a GitHub PR URL).

6. **AC 5 negative — no capture produces no `## Salvage`.** In each step's salvage context, one `It` with a fake runner that returns `nil, runCtx.Err()` (no capture) after blocking: assert `md.FindSection("## Salvage")` does NOT exist, yet the result still routes `done` + `human_review` with the budget-naming message. This keeps the salvage no-op for an empty capture.

7. **AC 6 tests — the idempotency guard never fires on a salvaged partial.** Add to `pkg/export_test.go`:
   ```go
   // AdvanceIfAlreadyReviewedForTest exposes advanceIfAlreadyReviewed for unit
   // testing (the ## Review-present idempotency guard).
   func AdvanceIfAlreadyReviewedForTest(md *agentlib.Markdown) *agentlib.Result {
   	s := &checkoutExecutionStep{}
   	return s.advanceIfAlreadyReviewed(md)
   }
   ```
   In `pkg/steps_checkout_execution_test.go`, add a `Describe("advanceIfAlreadyReviewed with a salvaged partial")` block:
   - A task holding ONLY a `## Salvage` section (no `## Review`) → returns `nil` (the guard does not fire — a partial can never advance to ai_review on a later trigger).
   - A task holding `## Review` → returns the non-nil `done`/`ai_review` result (existing behavior regression lock).
   - A task holding BOTH `## Salvage` and `## Review` → still returns the non-nil `done`/`ai_review` result (a stale salvage does not block a completed review).
   Build the markdowns with `agentlib.ParseMarkdown(ctx, "...")` (the parser splits at `## ` headings; `## Salvage` and `## Review` are distinct sections).

8. **Update `CHANGELOG.md`.** The `## Unreleased` section may or may not exist from a prior prompt in this batch. Create it if absent (below the preamble, above the newest `## vX.Y.Z`), else append. Add exactly one bullet:
   ```markdown
   - feat: persist a budget-terminated run's streamed partial review into a `## Salvage` task section (marked incomplete, distinct from `## Review`, never posted) so every budget overrun ends in a human-reviewable partial
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The `## Salvage` section heading is distinct from `## Review`. The `## Review`-present guard (`advanceIfAlreadyReviewed`) and the ai_review step's `## Verdict`-present guard must NEVER fire on a salvaged partial (spec Constraint).
- A salvaged partial is NEVER posted to GitHub as a review. Only a complete `## Review` with a parsed binary verdict posts; the binary verdict rule and the fail-closed parsing (`pkg/verdict.go`) are unchanged (spec Constraint).
- Salvage content is captured LLM streamed text persisted verbatim as markdown text under a controlled heading — never executed or interpreted; no new trust boundary, no new credentials (spec Security/Abuse).
- The salvage capture comes ONLY from the `ExtractBudgetPartial` helper (dependency-bump prompt) — do NOT re-parse stdout, do NOT vendor-patch, do NOT duplicate the claude-CLI invocation.
- No per-module chunking, no retry of budget-terminated runs, no opt-out flag (spec Non-goals).
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`.
- Existing tests must still pass (including the budget-routing prompt's budget-expiry and negative-detection tests).
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- `grep -n '## Salvage' pkg/salvage.go` — must return the `SalvageHeading` const (the distinct heading; AC 5 evidence).
- `grep -n 'writeSalvage\|ExtractBudgetPartial' pkg/steps_planning.go pkg/steps_checkout_execution.go pkg/steps_review.go` — must show the salvage write in all three phase steps.
- `grep -n 'AdvanceIfAlreadyReviewedForTest' pkg/export_test.go pkg/steps_checkout_execution_test.go` — must show the export and its guard tests.
- `grep -n 'PostCallCount' pkg/steps_checkout_execution_test.go pkg/steps_review_test.go` — must show the never-posts assertions on the budget path.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the three salvage cases, the no-capture negative, and the idempotency-guard tests.
</verification>

<!-- AUDITOR NOTES
1. AC 6's negative probe "grep -n 'postReview|posting' <budget-path run log> returns 0 lines" requires an actual run and is therefore the OPERATOR rung of the spec's Verification ladder (step 3 of the operator-executable replay) — it cannot run inside the YOLO container. The container-side equivalent is the `PostCallCount() == 0` assertion in the budget-path tests (requirement 5/6), which is what this prompt provides.
2. The fake-runner return shape for the AC 5 tests is defined by the released `bborbe/agent` capture API discovered by the dependency-bump prompt; requirement 5 tells the executor to construct it by reading `pkg/claude_partial.go`. If the capture lives on the error, the fake returns `nil, <error carrying the partial>`; if on the result, `<ClaudeResult with the partial>, <killErr>`. The executor must match the helper, not invent a shape.
3. The `writeSalvage` helper is package-private and covered through the step integration tests; if the auditor wants a direct unit test, add a `WriteSalvageForTest` export to `pkg/export_test.go` and assert empty → no-op / non-empty → section+marker.
-->
