---
status: completed
spec: [002-pr-reviewer-soft-time-budget-and-salvage]
summary: Enforced the REVIEW_MAX_DURATION soft time budget on all three Claude phase steps via a shared runWithSoftBudget helper with precise fired-deadline detection, routing budget overruns to human_review with a budget-naming message (never a retry or partial post), threading the budget through factory/main wiring, and adding per-phase budget-expiry tests plus a negative detection test
execution_id: github-pr-review-agent-salvage-exec-006-spec-002-budget-kill-routing
dark-factory-version: dev
created: "2026-08-21T08:07:20Z"
queued: "2026-08-21T10:14:18Z"
started: "2026-08-21T10:21:32Z"
completed: "2026-08-21T10:30:55Z"
branch: dark-factory/pr-reviewer-soft-time-budget-and-salvage
---

# Enforce the soft time budget on every Claude phase run and route budget overruns to human_review

<summary>
- Every Claude run in all three phases now runs under a context whose deadline is the configured soft time budget
- When the deadline fires before a final result arrives, the phase stops and routes to `human_review` with `Status=done` and a message that names the soft budget — it is no longer treated as a generic claude failure
- Budget expiry is detected precisely from the run context's own deadline, so a claude failure that merely RETURNS `context.DeadlineExceeded` (transport error, deadline not fired) still keeps the existing `failed` / controller-retry path
- A budget-terminated run is never retried inside the agent — no partial is posted, no `## Review` or `## Verdict` is written
- The execution phase step can now accept an injected claude runner (production builds its own as before), which is what makes its budget path testable
- The soft-budget value is threaded into all three phase-step constructors and through the factory wiring, updating every call site
- Unit tests drive each phase with a fake runner that blocks until the deadline fires, asserting the routing decision and the budget-naming message
</summary>

<objective>
Enforce the `REVIEW_MAX_DURATION` soft budget on every Claude phase run and make a budget overrun a distinct, bounded outcome: `done` + `NextPhase=human_review` with a message naming the budget, never a blank `needs_input`, never a generic claude failure, never a retry. Unrelated claude failures keep the existing `failed`/controller-retry path unchanged.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, factory purity, test style).

Read `docs/architecture.md` — the "Three Phases" section (each phase is a fresh claude run; the step returns a `Result` the framework publishes) and the "Result Delivery" section (a `Status: done` + `NextPhase` result advances the controller's phase; `failed` goes to the controller retry path).

Read the three phase steps fully:
- `pkg/steps_planning.go` — `planningStep.Run`'s retry loop (each attempt calls `s.runner.Run(ctx, prompt)`; a transport error returns `AgentStatusFailed`). Note the existing test at `pkg/steps_planning_test.go` (`runner.RunReturns(nil, context.DeadlineExceeded)` → "runner transport error not retried" → `AgentStatusFailed`) — that test is the PRECISE-DETECTION negative guard and must keep passing.
- `pkg/steps_checkout_execution.go` — `runClaude` constructs a fresh runner via `claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{...})` and calls `runner.Run(ctx, prompt)`; on error it returns `AgentStatusFailed`. The budget branch must return BEFORE `md.ReplaceSection(## Review)` and BEFORE `postAndRoute`.
- `pkg/steps_review.go` — `reviewStep.Run` calls `s.runner.Run(ctx, prompt)`; on error returns `AgentStatusFailed`. The budget branch must return BEFORE `md.ReplaceSection(## Verdict)` and before any verifier call.

Read the wiring:
- `pkg/factory/runner.go` — `RunConfig.MaxReviewDuration` (added by the previous prompt) and `RunAgent`'s `CreateAgent(...)` call.
- `pkg/factory/factory.go` — `CreateAgent` (constructs `NewPlanningStep`, `NewCheckoutExecutionStep`, `NewReviewStep`) and `CreateAgentProvider` (calls `CreateAgent`; the `pr-override` overrideAgent has no Claude and does NOT get the budget).
- `main.go` `dispatchAgent` — the `CreateAgentProvider(...)` call site.
- `pkg/factory/runner_test.go`, `pkg/factory/factory_test.go` — the `CreateAgent` / `CreateAgentProvider` tests.
- `pkg/steps_planning_test.go`, `pkg/steps_checkout_execution_test.go`, `pkg/steps_review_test.go`, `pkg/skip_post_boundary_test.go` — the step-constructor call sites.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — context-deadline handling in long-running work
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors` / `fmt.Sprintf` conventions
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — fake runners, Counterfeiter mocks
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — `Create*` prefix, pure composition
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Verified contracts (do not re-derive):
- `libtime "github.com/bborbe/time"` — `Duration` has `func (d Duration) Duration() stdtime.Duration` and `func (d Duration) String() string` (e.g. `25m`).
- `claudelib "github.com/bborbe/agent/claude"` — `type ClaudeRunner interface { Run(ctx context.Context, prompt string) (*ClaudeResult, error) }`; `NewClaudeRunner(ClaudeRunnerConfig) ClaudeRunner`.
- `mocks.ClaudeRunnerMock` (Counterfeiter) supports `RunStub func(context.Context, string) (*claudelib.ClaudeResult, error)`, `RunReturns`, `RunCallCount()`.
- `agentlib.Result{Status, NextPhase, Message}`, `agentlib.AgentStatusDone/Failed/NeedsInput`.
- `pkg.PostAndRouteForTest` and the existing `## Review`-absent paths are untouched by this prompt.
</context>

<requirements>
1. **Add the shared soft-budget runner helper.** Create `pkg/soft_budget.go` (package `pkg`):
   ```go
   // runWithSoftBudget runs the claude runner under a context whose deadline is
   // the soft REVIEW_MAX_DURATION budget. Returns the run result/error plus
   // whether the budget expired — detected PRECISELY from the run context's own
   // deadline (runCtx.Err() == context.DeadlineExceeded), never inferred from the
   // returned error. A non-expired runner error (crash, network, or an error that
   // merely wraps context.DeadlineExceeded without the deadline firing) keeps the
   // existing failed/controller-retry path.
   func runWithSoftBudget(
   	ctx context.Context,
   	runner claudelib.ClaudeRunner,
   	prompt string,
   	maxDuration libtime.Duration,
   ) (*claudelib.ClaudeResult, error, bool) {
   	runCtx, cancel := context.WithTimeout(ctx, maxDuration.Duration())
   	defer cancel()
   	result, err := runner.Run(runCtx, prompt)
   	expired := runCtx.Err() == context.DeadlineExceeded
   	return result, err, expired
   }
   ```
   Compute `expired` in the body BEFORE the deferred `cancel()` runs (as above) so a normally-completed run reports `false`. Imports: `context`, `claudelib "github.com/bborbe/agent/claude"`, `libtime "github.com/bborbe/time"`.

2. **Thread the budget into the planning step.** In `pkg/steps_planning.go`:
   - Change the constructor to `func NewPlanningStep(runner claudelib.ClaudeRunner, instructions claudelib.Instructions, maxDuration libtime.Duration) agentlib.Step` and store it as a new `maxDuration libtime.Duration` field on `planningStep`.
   - In `Run`'s retry loop, replace `runResult, runErr := s.runner.Run(ctx, prompt)` with `runResult, runErr, budgetExpired := runWithSoftBudget(ctx, s.runner, prompt, s.maxDuration)`.
   - In the `if runErr != nil` branch, BEFORE the existing failed-path return, add:
     ```go
     if budgetExpired {
     	glog.V(2).Infof("planning: soft time budget %s exceeded nextPhase=human_review", s.maxDuration)
     	return &agentlib.Result{
     		Status:    agentlib.AgentStatusDone,
     		NextPhase: "human_review",
     		Message:   fmt.Sprintf("planning claude run exceeded the %s soft time budget — routed to human review", s.maxDuration),
     	}, nil
     }
     ```
   - Keep the existing failed-path return byte-identical. Budget expiry returns on the attempt it fires — the retry loop must NOT retry a budget-terminated run. Do NOT write `## Plan` on the budget path.
   - The existing "runner transport error not retried" test (`runner.RunReturns(nil, context.DeadlineExceeded)`) must keep returning `AgentStatusFailed` — with the new code the fake returns immediately and the run context's deadline has NOT fired, so `budgetExpired` is false. Do not "fix" that test.

3. **Thread the budget + injectable runner into the execution step.** In `pkg/steps_checkout_execution.go`:
   - Add two fields to `checkoutExecutionStep`: `runner claudelib.ClaudeRunner` (nil = build a fresh runner, production behavior) and `maxDuration libtime.Duration`.
   - Change the constructor to append them at the end:
     ```go
     func NewCheckoutExecutionStep(
     	repoManager git.RepoManager,
     	claudeConfigDir claudelib.ClaudeConfigDir,
     	agentDir claudelib.AgentDir,
     	model claudelib.ClaudeModel,
     	env map[string]string,
     	allowedTools claudelib.AllowedTools,
     	reviewMode string,
     	repoAllowlist []string,
     	prPoster PrPoster,
     	funnelRunner FunnelRunner,
     	currentDateTime libtime.CurrentDateTimeGetter,
     	runner claudelib.ClaudeRunner,
     	maxDuration libtime.Duration,
     ) agentlib.Step
     ```
   - In `runClaude`, change the runner construction to prefer the injected one:
     ```go
     runner := s.runner
     if runner == nil {
     	runner = claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
     		ClaudeConfigDir:  s.claudeConfigDir,
     		AllowedTools:     s.allowedTools,
     		Model:            s.model,
     		WorkingDirectory: claudelib.AgentDir(worktreePath),
     		Env:              s.env,
     	})
     }
     ```
     then replace `runResult, runErr := runner.Run(ctx, prompt)` with `runResult, runErr, budgetExpired := runWithSoftBudget(ctx, runner, prompt, s.maxDuration)`.
   - In the `if runErr != nil` branch, BEFORE the existing failed-path return, add the budget branch:
     ```go
     if budgetExpired {
     	glog.V(2).Infof("execution: soft time budget %s exceeded nextPhase=human_review", s.maxDuration)
     	return &agentlib.Result{
     		Status:    agentlib.AgentStatusDone,
     		NextPhase: "human_review",
     		Message:   fmt.Sprintf("execution claude run exceeded the %s soft time budget — routed to human review", s.maxDuration),
     	}, nil
     }
     ```
   - The budget branch returns BEFORE `md.ReplaceSection(## Review)` and BEFORE `s.postAndRoute` — a budget-terminated run never writes `## Review`, never posts to GitHub. Keep the existing failed-path return byte-identical.

4. **Thread the budget into the ai_review step.** In `pkg/steps_review.go`:
   - Change the constructor to `func NewReviewStep(runner claudelib.ClaudeRunner, poster PrPoster, instructions claudelib.Instructions, verifier ReviewVerifier, ghToken string, botLogin string, maxDuration libtime.Duration) agentlib.Step` and store it as a new `maxDuration libtime.Duration` field on `reviewStep`.
   - In `Run`, replace `runResult, runErr := s.runner.Run(ctx, prompt)` with `runResult, runErr, budgetExpired := runWithSoftBudget(ctx, s.runner, prompt, s.maxDuration)`.
   - In the `if runErr != nil` branch, BEFORE the existing failed-path return, add:
     ```go
     if budgetExpired {
     	glog.V(2).Infof("ai-review: soft time budget %s exceeded nextPhase=human_review", s.maxDuration)
     	return &agentlib.Result{
     		Status:    agentlib.AgentStatusDone,
     		NextPhase: "human_review",
     		Message:   fmt.Sprintf("ai-review claude run exceeded the %s soft time budget — routed to human review", s.maxDuration),
     	}, nil
     }
     ```
   - The budget branch returns BEFORE `md.ReplaceSection(## Verdict)` and before `shouldVerifyPost`/verifier calls. Keep the existing failed-path return byte-identical.

5. **Update the factory wiring.** In `pkg/factory/factory.go`:
   - `CreateAgent` gains a trailing `maxDuration libtime.Duration` parameter and passes it to `NewPlanningStep`, `NewCheckoutExecutionStep` (with `nil` for the runner — production builds its own in `runClaude`), and `NewReviewStep`.
   - `CreateAgentProvider` gains a trailing `maxDuration libtime.Duration` parameter and passes it to `CreateAgent`. The `pr-override` overrideAgent is NOT given the budget (it runs no Claude).
   - In `pkg/factory/runner.go` `RunAgent`, pass `cfg.MaxReviewDuration` as the trailing argument to the `CreateAgent(...)` call.
   - In `main.go` `dispatchAgent`, pass `a.MaxReviewDuration` as the trailing argument to the `CreateAgentProvider(...)` call.
   - Update the `CreateAgent` (2 call sites) and `CreateAgentProvider` (1 call site) tests in `pkg/factory/factory_test.go` to pass the new trailing argument.

6. **Update every step-constructor call site in tests.** Grep for `NewPlanningStep`, `NewCheckoutExecutionStep`, and `NewReviewStep` across `pkg/` and update EVERY call site:
   - `pkg/steps_planning_test.go` (1 site), `pkg/steps_checkout_execution_test.go` (6 sites), `pkg/steps_review_test.go` (13 sites), `pkg/skip_post_boundary_test.go` (3 sites).
   - For non-budget constructions pass `nil` (execution runner) and `libtime.Duration(25 * time.Minute)` (the production default budget). For the new budget tests use a short budget.
   - Do not change any existing test's expectations — these are mechanical signature updates.

7. **AC 2 tests — budget expiry routes to human_review in every phase.** In each step's test file, add a `Describe("soft time budget expiry")` block with the fake-runner pattern:
   ```go
   runner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
   	<-runCtx.Done() // block until the soft budget deadline fires
   	return nil, runCtx.Err()
   }
   ```
   Construct the step with `libtime.Duration(20 * time.Millisecond)` as `maxDuration`. Call `step.Run(ctx, md)` and assert:
   - `result.Status == agentlib.AgentStatusDone`
   - `result.NextPhase == "human_review"`
   - `result.Message` contains `"soft time budget"` and the budget string (e.g. `"20ms"`) — this is the budget-naming message the framework serializes into the delivered result.
   - The step did NOT write its normal section (`## Plan` / `## Review` / `## Verdict` absent).
   - Budget-terminated runs are not retried: for planning, assert `runner.RunCallCount()` is exactly 1.
   Per step:
   - **planning** (`pkg/steps_planning_test.go`): reuse the existing md shape (no `## Plan`, PR URL present).
   - **execution** (`pkg/steps_checkout_execution_test.go`): build a dedicated step with `claudeConfigDir` = a `os.MkdirTemp` dir containing a fake plugin at `plugins/marketplaces/coding/commands/pr-review.md` (mirror the `writePlugin` pattern in `pkg/prompts/execution_test.go`), `repoManager.EnsureWorktreeReturns("/work/test", nil)`, `funnelRunner` nil, the injected fake runner, and a short budget. The md needs frontmatter `clone_url` + `ref` + `task_identifier` + `base_ref` and a GitHub PR URL in the body (so `extractRequiredFrontmatter`, `ParseCloneURLParts`, `checkAllowlist`, and `ExtractPRURL` all succeed).
   - **ai_review** (`pkg/steps_review_test.go`): md with no `## Verdict`; poster mock passed; assert `poster.PostCallCount() == 0` (the budget path never posts).

8. **AC 2 negative row — non-budget failure keeps the failed path.** The existing error-path tests are the regression guards and must keep passing unchanged: planning's "runner transport error not retried" (`runner.RunReturns(nil, context.DeadlineExceeded)` → `AgentStatusFailed`), planning's "when Claude runner returns an error", review's "when Claude runner returns an error", and execution's `runClaude` failed path. Add ONE explicit new negative test for the detection boundary in `pkg/steps_planning_test.go`: a fake runner returning `nil, errors.New("claude CLI crashed")` immediately (deadline not fired) → `AgentStatusFailed` (not human_review). This locks the precise-detection contract: only a FIRED run-context deadline routes to human_review.

9. **Update `CHANGELOG.md`.** The `## Unreleased` section may or may not exist from a prior prompt in this batch. Create it if absent (below the preamble, above the newest `## vX.Y.Z`), else append. Add exactly one bullet:
   ```markdown
   - feat: enforce the `REVIEW_MAX_DURATION` soft time budget on every Claude phase run and route budget overruns to `human_review` with a budget-naming message — never a blank `needs_input`, never a retry, never a partial posted
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Budget-expiry detection is precise and exclusive: ONLY a fired run-context deadline (`runCtx.Err() == context.DeadlineExceeded`) routes to `human_review`; any other claude-run failure (crash, network, or a returned-but-not-fired `context.DeadlineExceeded`) keeps the existing `failed`/controller-retry path so the retry safety net is preserved (spec Constraint).
- Budget-terminated runs are NOT retried by this agent (spec Non-goal): the planning retry loop must return the budget result immediately, never retry.
- A budget-terminated run never writes its normal section and never posts to GitHub — only the salvage prompt (next in the batch) writes a `## Salvage` section; this prompt leaves the budget branch with no markdown write.
- No per-phase budgets, no per-module chunking, no opt-out flag (spec Non-goals). One `REVIEW_MAX_DURATION` for all three phases.
- Do NOT change the verdict rubric, the `## Review`/`## Verdict` idempotency guards, or the fail-closed verdict parsing (spec Constraint).
- Do NOT add an operator-note or operator-verification step inside this prompt — the forced-low-budget replay is the spec's own operator-executable verification ladder.
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`; no `context.Background()` in `pkg/` code.
- Existing tests must still pass (including the transport-error negative guards).
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- `grep -n 'runWithSoftBudget' pkg/soft_budget.go pkg/steps_planning.go pkg/steps_checkout_execution.go pkg/steps_review.go` — must show the helper plus its use in all three phase steps.
- `grep -n 'soft time budget' pkg/steps_planning.go pkg/steps_checkout_execution.go pkg/steps_review.go` — must return the budget-naming messages in all three steps (AC 2 evidence).
- `grep -n 'MaxReviewDuration' main.go pkg/factory/runner.go` — must show the wiring into `RunConfig` and the `CreateAgentProvider` call.
- `grep -n 'runner == nil' pkg/steps_checkout_execution.go` — must show the injected-runner fallback in `runClaude`.
- `go test -mod=mod ./pkg/... ./... -count=1` — must pass, including the three new budget-expiry blocks, the negative detection test, and the unchanged transport-error negative guards.
</verification>

<!-- AUDITOR NOTES
1. Decomposition: the spec's Suggested Decomposition row 3 is "budget-kill detection + routing" and row 5 is "wrapper-side salvage". This prompt implements row 3 only; the `## Salvage` write lands in the later salvage prompt (which also consumes the `ExtractBudgetPartial` helper from the dependency-bump prompt). The budget branch is left with no markdown write here on purpose — the salvage prompt adds exactly that.
2. The execution step's runner injection (requirement 3) is NOT in the spec's Suggested Decomposition text but is required by AC 2/AC 5's "unit test with a fake runner" / "integration test injects a runner": `runClaude` constructs a real `claudelib.NewClaudeRunner` today and would otherwise be untestable with a fake. The field is nil in production wiring (behavior unchanged) and set only in tests.
3. The message format `"... exceeded the %s soft time budget — routed to human review"` uses `s.maxDuration` whose `String()` renders `25m`/`20ms`. AC 2 requires "the message names the budget" — satisfied. If the auditor wants the salvage section mentioned in the message, that is a later-prompt concern (the Salvage heading itself is the attachment).
4. Dependency deviation from the spec's Suggested Decomposition table (which lists prompt 3 as "Depends on prompts 1, 2"): in this decomposition prompt 3 does NOT consume the streamed-partial capture at all (the budget branch writes no markdown here; the salvage prompt, gated on the dependency-bump prompt, adds the `## Salvage` write via `ExtractBudgetPartial`). Consequently prompt 3 — and prompt 4, which follows and reads `s.maxDuration` — can both ship BEFORE the `bborbe/agent` release exists, a superset of the spec's "prompts 2 and 4 can proceed independently" intent. Only prompt 5 is genuinely gated on the release. The execution order (1->2->3->4->5) is unchanged.
-->
