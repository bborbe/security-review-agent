---
status: completed
spec: [002-pr-reviewer-soft-time-budget-and-salvage]
summary: 'Added the time-budget wrap-up contract to the assembled execution prompt (stop at the budget, disposition every ## Plan concern with a not-verified flag) and fail-closed any approve carrying an unverified concern to request-changes in postAndRoute, with full unit and posting-boundary test coverage'
execution_id: github-pr-review-agent-salvage-exec-007-spec-002-execution-wrap-up-contract
dark-factory-version: dev
created: "2026-08-21T08:07:20Z"
queued: "2026-08-21T10:14:18Z"
started: "2026-08-21T10:30:56Z"
completed: "2026-08-21T10:35:21Z"
branch: dark-factory/pr-reviewer-soft-time-budget-and-salvage
---

# Add the time-budget wrap-up contract to the execution prompt and fail-close an approve that flags unverified concerns

<summary>
- The assembled execution prompt now tells the model its soft time budget and to stop investigating when it is reached, writing the verdict from what it already knows
- The output format gives every `## Plan` concern an explicit disposition — addressed, not an issue, or flagged as not verified — so no concern is silently dropped when investigation ends
- A complete review that flags any concern as not verified yet still emits `approve` is fail-closed to `request-changes` in the posting step, mirroring the existing funnel-failed gate
- A review with no flagged concerns is unaffected: its `approve` posts unchanged (the gate does not over-trigger)
- The wrap-up wording is wired through the prompt assembly (not a comment), and a unit test asserts the assembled prompt the runner receives contains it
- The change is confined to the execution-phase prompt and the execution posting step; planning, ai_review, the verdict rubric, and the idempotency guards are untouched
</summary>

<objective>
Make a budget-bounded execution phase produce either a complete verdict or an honestly-flagged partial: the assembled prompt stops the model at the budget and forces every `## Plan` concern to be dispositioned (including "not verified"), and the posting step fail-closes any `approve` that carries an unverified concern to `request-changes` so an incomplete review can never green-light a PR.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, prompt assembly, test style).

Read `docs/architecture.md` — "The Verdict Rubric" (the Go-embedded `verdictTranslationFooter` string in `pkg/prompts/execution.go` is the source of truth; the JSON schema is `pkg/prompts/execution_output-format.md`) and "Verdict Parsing" (the fail-closed `request-changes` default in `pkg/verdict.go`).

Read these files fully:
- `pkg/prompts/execution.go` — `BuildExecutionInstructions` (the assembled prompt = `header + steer + stripFrontmatter(raw) + verdictTranslationFooter`), the `verdictTranslationFooter` const (the append point), and the existing `prefilledArgsHeaderTemplate`/`funnel*SteerTemplate` consts for the Go-string style to follow.
- `pkg/prompts/execution_output-format.md` — the `concerns_addressed` field rule (lines 27-28) to extend.
- `pkg/prompts/execution_test.go` — the NINE existing `BuildExecutionInstructions` call sites (happy path, worktree cwd, funnel injected, funnel failed, frontmatter stripping, no frontmatter, plugin missing, empty baseRef, empty reviewMode) that all gain the new argument, plus the `writePlugin` helper.
- `pkg/steps_checkout_execution.go` — `postAndRoute` (the existing funnel fail-closed gate `if !funnelRan && verdict.Verdict == VerdictApprove { ... ReasonFunnelDidNotRun }` at the same location the new gate goes), and the `BuildExecutionInstructions(...)` call in `Run` (passes `s.maxDuration`, which the previous prompt in this batch added to the step).
- `pkg/verdict.go` — `ParseVerdict`, `findVerdictBlock`, the `Result` type, `VerdictApprove`/`VerdictRequestChanges`, `ReasonFunnelDidNotRun`, and `isFailClosedReason` (the new reason constant must be added there).
- `pkg/steps_checkout_execution_test.go` — the `Describe("posting behavior")` block: `buildMD`, `fixedTime`, and the two funnel-gate `It`s (lines ~458-492) that show the exact `fakePoster.PostArgsForCall(0)` assertion pattern the new gate tests mirror.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, table tests
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Verified contracts (do not re-derive):
- `libtime "github.com/bborbe/time"` — `type Duration stdtime.Duration` with `func (d Duration) String() string` (e.g. `25m`); `pkg/prompts` does not import it yet — add the import.
- `BuildExecutionInstructions` currently: `func BuildExecutionInstructions(ctx context.Context, claudeConfigDir claudelib.ClaudeConfigDir, reviewMode string, baseRef string, funnelRan bool, funnelFindings string, funnelFailDetail string) (claudelib.Instructions, error)` — it gains one trailing `maxDuration libtime.Duration` parameter.
- The `verdictTranslationFooter` const contains the "Final step — emit verdict JSON" footer; the existing happy-path test asserts the assembled workflow contains "Final step — emit verdict JSON", "Severity map", and "Verdict roll-up" — appending after it preserves those assertions.
</context>

<requirements>
1. **Add the time-budget footer to the execution prompt.** In `pkg/prompts/execution.go`, add this const (place it after `verdictTranslationFooter`):
   ```go
   // timeBudgetFooter is appended to the assembled execution instructions when the
   // agent enforces the soft REVIEW_MAX_DURATION budget. It tells the model to stop
   // investigating at the budget and write the verdict from what it already knows,
   // explicitly flagging every ## Plan concern it could not examine. %s = budget.
   const timeBudgetFooter = "---\n\n" +
   	"## Time budget\n\n" +
   	"This run has a soft time budget of %s. When the budget is reached, STOP " +
   	"investigating immediately and write the verdict from what you already know — " +
   	"do not keep going until the job kills the run.\n\n" +
   	"Disposition EVERY concern in `## Plan` inside `concerns_addressed` with one of:\n" +
   	"- `addressed` — a code change or a comment resolves it\n" +
   	"- `not an issue` — you examined it and confirmed it is a non-issue\n" +
   	"- `not verified` — the time budget stopped you before you could examine it\n\n" +
   	"A concern listed as `not verified` means the review is incomplete: the verdict " +
   	"will be fail-closed to request-changes and the partial output is salvaged for a " +
   	"human. Never silently drop a concern because investigation ended.\n\n"
   ```
   CRITICAL constraints on the added string: it must contain exactly ONE `%s` (the budget) and NO other `%` character; it must not contain `description:` or `allowed-tools:` (the frontmatter-stripping test asserts those are absent); it must contain the literal `not verified` and `time budget` (AC 3 evidence).

2. **Thread the budget through `BuildExecutionInstructions`.** In `pkg/prompts/execution.go`, change the signature to add a trailing parameter:
   ```go
   func BuildExecutionInstructions(
   	ctx context.Context,
   	claudeConfigDir claudelib.ClaudeConfigDir,
   	reviewMode string,
   	baseRef string,
   	funnelRan bool,
   	funnelFindings string,
   	funnelFailDetail string,
   	maxDuration libtime.Duration,
   ) (claudelib.Instructions, error)
   ```
   and change the assembly line to append the new footer:
   ```go
   assembled := header + steer + stripFrontmatter(string(raw)) + verdictTranslationFooter + fmt.Sprintf(timeBudgetFooter, maxDuration)
   ```
   Add the import `libtime "github.com/bborbe/time"`. (The wrap-up wording is part of the assembled instructions the runner receives — this is what makes AC 3's "assembly is wired, not a comment" test possible.)

3. **Update the execution-step call site.** In `pkg/steps_checkout_execution.go` `Run`, add `s.maxDuration,` as the trailing argument to the `prompts.BuildExecutionInstructions(...)` call. (`s.maxDuration` was added by the previous prompt in this batch.)

4. **Update every test call site of `BuildExecutionInstructions`.** In `pkg/prompts/execution_test.go`, add a trailing `libtime.Duration(25 * time.Minute)` argument to ALL NINE existing calls (happy path, worktree cwd, funnel injected, funnel failed, frontmatter stripping, no frontmatter, plugin missing, empty baseRef, empty reviewMode). Add the import `libtime "github.com/bborbe/time"` to the test file. Do not change any existing assertion.

5. **AC 3 test — the assembled prompt carries the wrap-up contract.** In `pkg/prompts/execution_test.go`, add a new `Describe("time budget wrap-up contract")` with one `It` that calls `writePlugin(fakePlugin)` then `prompts.BuildExecutionInstructions(ctx, claudelib.ClaudeConfigDir(tmpDir), "standard", "main", true, sampleFindings, "", libtime.Duration(25*time.Minute))` and asserts `workflow := instructions[0].Content` contains:
   - `"25m"` (the budget reaches the prompt),
   - `"not verified"` (the wrap-up wording),
   - `"STOP"` and `"time budget"`.
   This test fails on a comment-only insertion — it asserts the assembled string content.

6. **Add the "not verified" disposition to the output format.** In `pkg/prompts/execution_output-format.md`, replace the `concerns_addressed` field rule (currently "lists each concern from `## Plan` with resolution status (addressed by code change OR raised as comment)") with:
   ```markdown
   - `concerns_addressed`: required, lists each concern from `## Plan` with
     resolution status: `addressed` (a code change or comment resolves it),
     `not an issue` (examined and confirmed), or `not verified` (investigation
     stopped at the time budget before the concern could be examined). A concern
     listed as `not verified` makes the review incomplete: an `approve` verdict
     carrying any `not verified` concern is fail-closed to `request-changes`.
   ```
   The word `not verified` MUST appear (AC 4 evidence grep).

7. **Add the unverified-concern detector to `pkg/verdict.go`.** Add:
   ```go
   // unverifiedConcernPattern matches a ## Plan concern the model flagged as
   // "not verified" in the execution output format's concerns_addressed list.
   var unverifiedConcernPattern = regexp.MustCompile(`(?i)not verified|unverified`)

   // HasUnverifiedConcerns reports whether the review body's verdict JSON flags
   // any ## Plan concern as "not verified" — i.e. the model stopped investigating
   // at the time budget before examining it. An approve that flags any concern
   // must be fail-closed to request-changes (see postAndRoute). Returns false for
   // a missing/malformed verdict block or an empty concerns list (no over-trigger).
   func HasUnverifiedConcerns(reviewText string) bool
   ```
   Implementation: extract the verdict block with `findVerdictBlock`; if not found return false; `json.Unmarshal` into a local struct `{ ConcernsAddressed []string \`json:"concerns_addressed"\` }` (return false on unmarshal error); return true iff any entry matches `unverifiedConcernPattern`. `encoding/json` and `regexp` are already imported in `pkg/verdict.go`.

8. **Add the fail-closed reason constant.** In `pkg/verdict.go`, add:
   ```go
   // ReasonConcernsNotVerified is the fail-closed Result.Reason set when the review
   // flags one or more ## Plan concerns as "not verified" (unexamined at the time
   // budget) yet still emits approve. The verdict is overridden to request-changes
   // so an incomplete review can never green-light a PR.
   const ReasonConcernsNotVerified = "one or more ## Plan concerns not verified"
   ```
   and add `reason == ReasonConcernsNotVerified ||` to the `isFailClosedReason` predicate so the existing fail-closed diagnostic log in `postAndRoute` fires for it.

9. **Wire the fail-closed gate into the posting step.** In `pkg/steps_checkout_execution.go` `postAndRoute`, IMMEDIATELY AFTER the existing funnel gate (`if !funnelRan && verdict.Verdict == VerdictApprove { verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonFunnelDidNotRun} }`), add:
   ```go
   if verdict.Verdict == VerdictApprove && HasUnverifiedConcerns(reviewBody) {
   	verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonConcernsNotVerified}
   }
   ```
   `reviewBody` is already in scope at that point (extracted from the `## Review` section earlier in the function). If both gates fire, the unverified-concerns reason wins — it is the more specific diagnosis. No other logic in `postAndRoute` changes.

10. **AC 4 tests — the gate fail-closes approve with unverified concerns and does not over-trigger.** In `pkg/steps_checkout_execution_test.go`, inside `Describe("posting behavior")`, add a new `Context("fail-closed gate when a concern is flagged not verified")` mirroring the two funnel-gate `It`s (same `buildMD`, `fixedTime`, `PostReturns(pkg.PostResult{Outcome: "success", ReviewID: N})`, and `PostArgsForCall(0)` assertion pattern). Use `funnelRan: true` so the funnel gate does not interfere:
    - Positive row: a `## Review` body whose verdict JSON is `{"verdict":"approve","reason":"looks ok","concerns_addressed":["security: rate-limit not verified — stopped at the time budget"]}` → `fakePoster.PostArgsForCall(0)` `req.Verdict == pkg.VerdictRequestChanges`.
    - Negative row: `{"verdict":"approve","reason":"clean","concerns_addressed":["security: rate-limit addressed in handler.go:45"]}` → `req.Verdict == pkg.VerdictApprove` (the gate does not over-trigger).
    Also add a `DescribeTable` (or separate `It`s) for `pkg.HasUnverifiedConcerns` in a new `pkg/verdict_unverified_test.go` (external `pkg_test`): flagged lowercase → true; flagged uppercase `NOT VERIFIED` → true (case-insensitive); `unverified` variant → true; no flags → false; empty `concerns_addressed` → false; no `concerns_addressed` key → false; prose without a verdict block → false; malformed JSON → false.

11. **Update `CHANGELOG.md`.** The `## Unreleased` section may or may not exist from a prior prompt in this batch. Create it if absent (below the preamble, above the newest `## vX.Y.Z`), else append. Add exactly one bullet:
    ```markdown
    - feat: add the time-budget wrap-up contract to the execution prompt (stop at the budget, disposition every `## Plan` concern, flag unexamined ones as `not verified`) and fail-close an `approve` that carries any unverified concern to `request-changes`
    ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The wrap-up contract is added to the assembled instructions in `pkg/prompts/execution.go` — do NOT edit the `/coding:pr-review` plugin file in the coding plugin repo (spec Constraint).
- Do NOT change the verdict rubric or the binary verdict values for COMPLETE reviews — `approve`/`request-changes` parsing (`pkg/verdict.go`) is unchanged; the new gate only demotes an `approve` when an unverified concern is present (spec Non-goal + Constraint).
- The gate must not over-trigger: a review with no flagged concerns and verdict `approve` posts as `approve` (AC 4 negative row).
- Do NOT touch planning/ai_review prompts, the `## Review`/`## Verdict` idempotency guards, or the `## Salvage` mechanics (a later prompt in this batch).
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- `grep -n -i 'unverified\|not verified\|time budget' pkg/prompts/execution.go` — must return line ≥ 1 (AC 3 evidence).
- `grep -n -i 'flagged\|not verified' pkg/prompts/execution_output-format.md` — must return line ≥ 1 (AC 4 evidence).
- `grep -n 'HasUnverifiedConcerns' pkg/verdict.go pkg/steps_checkout_execution.go pkg/verdict_unverified_test.go` — must show the detector, its use in `postAndRoute`, and its tests.
- `grep -n 'ReasonConcernsNotVerified' pkg/verdict.go pkg/steps_checkout_execution.go` — must show the constant and the gate.
- `grep -n 'timeBudgetFooter\|maxDuration' pkg/prompts/execution.go` — must show the footer const and the new parameter.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the wrap-up-contract assembly test, the two gate tests, and the `HasUnverifiedConcerns` table.
</verification>

<!-- AUDITOR NOTES
1. AC 4's negative row ("the gate does not over-trigger") is covered both at the unit level (`HasUnverifiedConcerns` returns false for un-flagged/missing/malformed input) and at the posting boundary (PostAndRouteForTest with a clean approve stays approve). AC 3's "assembly is wired, not a comment" is covered by asserting the assembled `instructions[0].Content` string.
2. The `HasUnverifiedConcerns` marker regex matches both `not verified` and `unverified` (case-insensitive) to be robust to model phrasing; the output format prescribes `not verified` as canonical. Matching `unverified` widens the fail-close trigger slightly — an approve with ANY concern containing the word "unverified" is demoted, which is safe (request-changes is the conservative direction). If the auditor wants strictly one marker, narrow the regex to `(?i)not verified`.
-->
