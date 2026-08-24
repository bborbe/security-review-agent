---
status: completed
spec: [003-security-review-agent-adaptation]
summary: Routed security-review tasks to a no-posting security agent (nil poster + nil verifier), dropped the pr-review/pr-override routes, moved poster constructors to pkg/factory/poster.go, and locked the dispatch + no-posting behavior with unit tests; make precommit exits 0
execution_id: security-review-agent-security-exec-012-spec-003-security-dispatch-and-no-posting
dark-factory-version: dev
created: "2026-08-24T20:22:59Z"
queued: "2026-08-24T20:44:38Z"
started: "2026-08-24T20:54:04Z"
completed: "2026-08-24T21:01:55Z"
branch: dark-factory/security-review-agent-adaptation
---

<summary>
- A `security-review` task type is defined and routed to a 3-phase security agent; the `healthcheck` liveness route is kept.
- The security agent wires a nil poster and a nil verifier, so no GitHub posting machinery is constructed and the run ends at the task's `## Review` write-back.
- The `pr-review` and `pr-override` routes are removed from the dispatch table — a task mis-routed to this binary is refused via the existing unknown-task-type rejection and never run as a plain review.
- `pkg/factory/factory.go` no longer imports or references `githubposter` at all (poster/verifier constructors move to a sibling file so the existing exported API still compiles).
- Unit tests lock the dispatch (security-review routed, healthcheck kept, pr-review/pr-override refused), the task-type constant's boundary contract, and the no-posting behavior (execution writes `## Review` and routes to ai_review with zero Post calls; ai_review with a nil verifier makes zero VerifyReview calls).
</summary>

<objective>
Make this binary consume `security-review` tasks and refuse `pr-review` / `pr-override` tasks, with the security agent built to never post to GitHub (nil poster + nil verifier) — the load-bearing behavior of the adaptation.
</objective>

<context>
Read CLAUDE.md (repo root), then read:
- `pkg/factory/factory.go` — `CreateAgentProvider`, `CreateAgent`, `CreatePrPoster`, `CreateReviewVerifier`, `serviceName`, and the `githubposter` import to remove.
- `pkg/factory/runner.go` — `ResolvePosters` (calls `CreatePrPoster`/`CreateReviewVerifier`) and the `RunAgent` fallback; these keep compiling unchanged.
- `pkg/factory/factory_test.go` — the `CreateAgentProvider` Describe to update.
- `pkg/factory/export_test.go` — the `ExecutionTools` test seam (do not disturb).
- `pkg/steps_override.go` — `TaskTypePROverride` const + `NewOverrideStep` (kept as seed legacy, route removed).
- `pkg/poster_types.go` — `PrPoster` / `ReviewVerifier` interfaces.
- `pkg/steps_checkout_execution.go` — `postAndRoute`'s nil-poster guard (`if s.prPoster == nil` returns Done + `NextPhase: "ai_review"`).
- `pkg/steps_review.go` — `reviewStep.Run`'s nil-verifier guard (`if s.verifier != nil && shouldVerify`).
- `pkg/skip_post_boundary_test.go` — the existing nil-poster/nil-verifier contract tests to reference and extend.
- `pkg/export_test.go` — `PostAndRouteForTest` seam.

Coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `go-factory-pattern.md` (Create* prefix, zero business logic in factory functions)
- `go-testing-guide.md` (Ginkgo v2 / Gomega, Counterfeiter mocks, external test packages)
- `go-error-wrapping-guide.md` (bborbe/errors wrapping)
- `go-precommit.md` (linter limits)

Do NOT touch `pkg/verdict.go` — the verdict contract is a later prompt's lock.
</context>

<requirements>
1. **Define the security task type.** Create `pkg/task_types.go`:
   ```go
   // Copyright (c) 2026 Benjamin Borbe All rights reserved.
   // Use of this source code is governed by a BSD-style
   // license that can be found in the LICENSE file.

   package pkg

   import agentlib "github.com/bborbe/agent"

   // TaskTypeSecurityReview is the task type for security review tasks. Routes to
   // the security agent's 3-phase domain flow (planning → execution → ai_review)
   // with no GitHub posting (nil poster + nil verifier); the run ends at the
   // ## Review write-back.
   const TaskTypeSecurityReview agentlib.TaskType = "security-review"
   ```
   The literal MUST stay `"security-review"` (matches `^[a-z0-9-]+$`, the agentlib `TaskType` regex the CRD-side validator enforces).

2. **Move the poster constructors out of `factory.go`.** Create `pkg/factory/poster.go` in the same package, moving the ENTIRE bodies of `CreatePrPoster` and `CreateReviewVerifier` unchanged — same signatures, same return types, same behavior — and adding to `pkg/factory/poster.go` only the imports those two functions need (`net/http`, `time`, `githubposter`). They remain exported in package `factory`, so `pkg/factory/runner.go` (`ResolvePosters`) and `pkg/factory/runner_test.go` compile unchanged. After the move, `pkg/factory/factory.go` must no longer import or reference `githubposter`, `net/http`, or `time` — but `prpkg` (used by `CreateAgent` and the rewire below) and `libtime` (used in every signature) MUST stay imported in `factory.go`.

3. **Rewire `CreateAgentProvider` in `pkg/factory/factory.go`.** Replace the body (keeping the same signature) so it builds the security agent with nil poster + nil verifier, keeps the healthcheck route, and drops the pr-review / pr-override / override-agent machinery:
   ```go
   // CreateAgentProvider wires the per-task-type dispatch table for
   // maintainer-agent-security-reviewer. TaskTypeSecurityReview routes to the
   // 3-phase security agent built by CreateAgent with nil poster + nil verifier
   // (no GitHub posting — the run ends at the ## Review write-back).
   // TaskTypeHealthcheck routes to the liveness agent. pr-review and pr-override
   // are deliberately NOT registered: a task mis-routed to this binary is refused
   // by the unknown-task-type rejection and never run as a plain review.
   // Pure plumbing; no conditional, no error.
   func CreateAgentProvider(
   	claudeConfigDir claudelib.ClaudeConfigDir,
   	agentDir claudelib.AgentDir,
   	model claudelib.ClaudeModel,
   	ghToken string,
   	env map[string]string,
   	repoManager git.RepoManager,
   	reviewMode string,
   	repoAllowlist []string,
   	currentDateTime libtime.CurrentDateTimeGetter,
   	maxDuration libtime.Duration,
   ) agentlib.AgentProvider {
   	domainAgent := CreateAgent(
   		claudeConfigDir,
   		agentDir,
   		model,
   		ghToken,
   		env,
   		repoManager,
   		reviewMode,
   		repoAllowlist,
   		nil, // prPoster — the security agent wires no poster
   		nil, // verifier — the security agent wires no verifier
   		currentDateTime,
   		maxDuration,
   	)
   	healthcheckRunner := CreateClaudeRunner(
   		claudeConfigDir,
   		agentDir,
   		model,
   		env,
   		claudelib.AllowedTools{},
   	)
   	livenessAgent := healthcheck.NewAgent(healthcheck.NewClaudeStep(healthcheckRunner))
   	return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
   		prpkg.TaskTypeSecurityReview: domainAgent,
   		agentlib.TaskTypeHealthcheck: livenessAgent,
   	})
   }
   ```
   This removes: the `poster := CreatePrPoster(...)` / `verifier := CreateReviewVerifier(...)` calls, the `overrideAgent` block (`agentlib.NewPhase(domain.TaskPhaseExecution, ..., prpkg.NewOverrideStep(poster, botLogin))`), the `botLogin := ResolveBotLogin(env)` line inside the provider, and the map entries for `agentlib.TaskTypePRReview` and `prpkg.TaskTypePROverride`. `domain` (vault-cli `TaskPhaseExecution`) is still used by `CreateAgent` — keep that import.

4. **Update `CreateAgent`'s doc comment** in `pkg/factory/factory.go` to describe the security flow (execution writes `## Review` and routes to `ai_review` without posting; ai_review writes `## Verdict` without post-verification). Do NOT change its signature or body — the nil poster/verifier are already handled (`postAndRoute`'s `if s.prPoster == nil` guard and `reviewStep.Run`'s `if s.verifier != nil && shouldVerify` guard).

5. **Intentional retention (do NOT delete):** `prpkg.TaskTypePROverride` and `prpkg.NewOverrideStep` in `pkg/steps_override.go` stay as seed legacy (referenced only by `pkg/steps_override_test.go`). The factory no longer calls them; that is the point. Do not modify `pkg/steps_override.go` in this prompt.

6. **Update the factory dispatch test** (`pkg/factory/factory_test.go`, `CreateAgentProvider` Describe). Replace the pr-review row and the bogus-row assertions:
   - `provider.Get(ctx, prpkg.TaskTypeSecurityReview)` — returns a non-nil agent, no error (AC3).
   - `provider.Get(ctx, agentlib.TaskTypeHealthcheck)` — returns a non-nil agent, no error (AC3, route kept).
   - `provider.Get(ctx, agentlib.TaskTypePRReview)` — returns an error (refused; never run as a plain review) (AC3).
   - `provider.Get(ctx, prpkg.TaskTypePROverride)` — returns an error (refused).
   - The existing bogus row: error message must contain `"unknown task_type"`, the bogus value, `"security-review"`, and `"healthcheck"` (update the old `"pr-review"` assertion).
   Keep the `BeforeEach` (it already constructs the provider with the same argument list).

7. **Boundary test for the task-type constant.** Add `pkg/task_types_test.go` (external test package `pkg_test`):
   - `prpkg.TaskTypeSecurityReview.Validate(context.Background())` returns nil — this is the level-1 boundary test proving the literal satisfies the agentlib runtime regex `^[a-z0-9-]+$` (a struct-shape/constant-value assertion alone does NOT satisfy this; the `.Validate(ctx)` call is required).
   - `string(prpkg.TaskTypeSecurityReview)` equals `"security-review"`.

8. **No-posting unit tests.** Add `pkg/security_no_posting_test.go` (external test package `pkg_test`), mirroring the harness in `pkg/steps_checkout_execution_test.go` (fake `mocks.RepoManager` with `EnsureWorktreeReturns(path, nil)`, fake `mocks.ClaudeRunnerMock` with `RunReturns(&claudelib.ClaudeResult{Result: <review body>}, nil)`):
   - **Execution (AC4):** construct the security execution step exactly as the factory does — `pkg.NewCheckoutExecutionStep(repoManagerFake, claudeConfigDir, "agent", "sonnet", env, tools, "standard", nil /*repoAllowlist*/, nil /*prPoster — the security agent wires no poster*/, nil /*funnelRunner*/, currentDateTime, fakeRunner, maxDuration)`. `claudeConfigDir` must point at a temp dir whose `plugins/marketplaces/coding/commands/pr-review.md` contains a minimal plugin body that DOES declare `--security` (this test runs the full `Run` path, which calls `BuildExecutionInstructions` — the plugin read must succeed; the `--security` probe added by a later prompt also requires it). Run it on a fixture task with `clone_url` / `ref` / `base_ref` / `task_identifier` frontmatter and a GitHub PR URL in the body. Assert: no error; `result.Status == agentlib.AgentStatusDone`; `result.NextPhase == "ai_review"`; the markdown now contains a `## Review` section; and the markdown contains no poster-verdict block (the step carries a nil poster, so `postAndRoute` returns before any GitHub API call — the meaningful proof is `result.NextPhase == "ai_review"` above, not an unwired mock's call count; do not assert on a `fakePoster` that is never wired into the step).
   - **ai_review (AC4):** construct `pkg.NewReviewStep(fakeRunner, nil /*poster*/, claudelib.Instructions{}, nil /*verifier — the security agent wires no verifier*/, "", botLogin, maxDuration)` with a canned `{"verdict":"pass","reason":"all checks pass"}` run result. Run on a markdown with `## Review` present, a `## Diagnostics` block containing `class: transient` (so `shouldVerifyPost` is genuinely evaluated — mirror `pkg/skip_post_boundary_test.go` case-4), and a GitHub PR URL in the preamble. Assert: no error; `result.Status == agentlib.AgentStatusDone`; `result.NextPhase == "done"`; no `ai_review verify:` diagnostic line is appended; and `fakeVerifier.VerifyReviewCallCount() == 0` where `fakeVerifier := &mocks.ReviewVerifier{}` is held.

9. **Changelog.** Append to the existing `## Unreleased` section (created by the previous prompt):
    `- feat: route security-review tasks to a no-posting security agent (nil poster + nil verifier) and drop the pr-review / pr-override routes`

10. **Self-check.** Before finishing: re-run `<verification>` and confirm every command passes; walk the spec's AC3 (dispatch) and AC5 (no githubposter in factory.go) acceptance criteria against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- The security agent wires NO poster and NO verifier — a nil interface, never a nil concrete pointer stored in an interface (the guards branch on the interface value). Do not introduce any poster/verifier construction into the security agent.
- Do NOT widen the execution-phase tool allowlist for the security variant. The exfiltration boundary (read-only git, no network tools, no arbitrary bash) is unchanged; the `--security` toolchain/dependencies pass runs via the Task-tool sub-agent dispatch the coding command already specifies, never by adding binaries/network to the parent allowlist.
- The `healthcheck` route is kept; `pr-review` and `pr-override` routes are removed (mis-routed tasks refused, never run as a plain review, never posted).
- No opt-in flag re-enables GitHub posting on this agent (spec Non-goal).
- `pkg/verdict.go` is untouched in this prompt.
- `pkg/steps_override.go` (constant + step + its test) is kept as seed legacy — do not modify it.
- The `.dark-factory.yaml` workflow (direct, pr: false) is untouched.
- Factory functions stay pure composition — no conditionals, no I/O, no `context.Background()`.
- Use `errors.Wrapf`/`errors.New`/`errors.Errorf` from `github.com/bborbe/errors` — never `fmt.Errorf` for error creation.
- Test coverage: new code >= 80% statement coverage; all changed/added code paths tested.
</constraints>

<verification>
- `grep -n 'githubposter' pkg/factory/factory.go` — returns 0 lines (AC5)
- `grep -n 'TaskTypePRReview\|TaskTypePROverride' pkg/factory/factory.go` — returns 0 lines (routes removed)
- `go test ./pkg/factory/... ./pkg/...` — exits 0; the dispatch rows (security-review routed, healthcheck kept, pr-review/pr-override refused), the task-type Validate boundary test, and the no-posting rows are present
- `make precommit` — exits 0
</verification>
