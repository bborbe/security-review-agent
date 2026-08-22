---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-08-21T07:51:45Z"
generating: "2026-08-21T07:52:58Z"
prompted: "2026-08-21T08:30:55Z"
verifying: "2026-08-21T13:47:27Z"
branch: dark-factory/pr-reviewer-soft-time-budget-and-salvage
---

## Summary

- Large multi-file PRs let the review agent's LLM investigation phase run unbounded: the model goes deep on a single correctness concern (e.g. vendor re-keying) and burns the Kubernetes Job's hard deadline (`ActiveDeadlineSeconds`, sourced from `ZombieJobTimeoutSeconds`, default 1800s = 30 min) before reaching a verdict.
- When the Job kills the run, the streamed partial review output is discarded — only a 5-line tail survives into the error message. The task escalates to `human_review` with a blank `needs_input` and no findings preserved.
- Fix: a soft time budget (`REVIEW_MAX_DURATION`, default 25 min, env-configurable) below the K8s hard deadline. At budget time the review prompt forces the model to stop investigating and write the verdict from what it knows, listing unexamined concerns as "flagged, not verified" instead of silently dropping them.
- If the model still overruns, the wrapper detects the budget expiry, salvages the streamed partial review into the task file, and routes to `human_review` with that content attached — never a blank `needs_input`, never a partial posted to GitHub.
- The salvage requires the vendored `github.com/bborbe/agent` claude runner to surface its streamed partial output on kill (it currently keeps only the final result event). The dependency must release first, then this repo bumps its version. No per-module chunking: the killer concern spans modules, so the fix bounds investigation time, not diff size.

## Problem

On large multi-file PRs the pr-reviewer agent fails. Evidence: `Seibert-Data/moss` PR #1 (51 files, 71+/291-) was reviewed twice and failed both times — first `reason: deadline_exceeded` at the planning phase (re-triggered 3x), then a truncated `## Review` block that cut off mid-investigation (the model was deep in Go's `modload/import.go` vendor-mode loading), producing an executor verdict of `needs_input` with no verdict/summary/comments, all 7 plan concerns marked `missed`, escalated to `human_review`. Reproduction shows the mechanical funnel is NOT the problem — 74 YAML rules ran over all 51 changed files in 1.8s with 14 findings and 0 errors. The failure is an unbounded LLM investigation phase that burns the K8s Job deadline; when the job is killed, `claude-runner.go` (in `github.com/bborbe/agent`) discards the streamed partial output. The agent's core promise — "escalation over guessing, review gate never silently fails" — is broken for exactly the PRs that need it most.

## Goal

After this work, every review run terminates inside the K8s Job deadline. A run that cannot reach a verdict within the soft budget ends in `human_review` with the partial review persisted in the task file (a verdict written from known findings with unverified concerns explicitly flagged, or the salvaged partial stream) — never `deadline_exceeded`, never a blank `needs_input`, never an incomplete review posted to GitHub. Large multi-file PRs reliably produce either a completed review or a salvageable, human-reviewable partial.

## Non-goals

- Do NOT add per-module / per-file chunking of the diff into sub-reviews. Rejected: the killer concern spans modules, and the failure is investigation time, not diff size.
- Do NOT change the K8s hard deadline (`ActiveDeadlineSeconds` / `ZombieJobTimeoutSeconds`) — that lives in the executor/CRD domain (`bborbe/agent`). The soft budget lives below it.
- Do NOT retry a budget-terminated run. A retry re-burns a full job deadline on the same unbounded investigation; budget-terminated runs go straight to `human_review`.
- Do NOT change the ai_review phase's checks or the verdict rubric for complete reviews.
- Do NOT add a per-feature opt-out flag that disables the budget — an escape hatch on the Goal is itself a regression; if a future consumer needs an unbounded investigation, that is a separate spec.
- Do NOT add per-phase budgets (planning vs execution vs ai_review). One `REVIEW_MAX_DURATION` for all phase runs.

## Acceptance Criteria

- [ ] `REVIEW_MAX_DURATION` is accepted as an env var in both entry points and defaults to `25m` (1500s, below the 1800s hard default) — evidence: `grep -n 'REVIEW_MAX_DURATION' main.go` returns line ≥ 1; unit test asserts the resolved duration is 1500s when unset, equals the parsed value when set, and an invalid value fails startup
- [ ] A Claude run that overruns the soft budget routes to `human_review` with the salvaged partial attached — evidence: unit test with a fake runner that blocks until the run context's deadline fires asserts the step returns `Status=done`, `NextPhase=human_review`, and the message names the budget; the delivered result JSON/log line records `Status=done, NextPhase=human_review` with a budget-naming reason (assert the string in the delivered result); a negative row asserts the delivered result is never `Status=needs_input` on budget expiry, and a second negative row asserts a non-budget claude failure (fake runner returns a crash/network error, deadline not fired) still returns `Status=failed` on the controller-retry path, not `human_review`
- [ ] The execution prompt contains the forced wrap-up contract (stop investigating at budget, write the verdict from known findings, flag unexamined concerns) — evidence: `grep -n -i 'unverified\|not verified\|time budget' pkg/prompts/execution.go` returns line ≥ 1; the assembly is wired, not a comment — a unit test asserts the assembled execution prompt string passed to the runner contains the wrap-up wording (e.g. `not verified`), so a comment-only insertion cannot satisfy the AC
- [ ] The execution output format has an explicit "flagged, not verified" disposition for unexamined `## Plan` concerns — evidence: `grep -n -i 'flagged\|not verified' pkg/prompts/execution_output-format.md` returns line ≥ 1; a unit test asserts a review JSON that flags any concern as not verified and emits `approve` is fail-closed to `request-changes`, and a negative row asserts a review JSON with no flagged concerns and verdict `approve` remains `approve` (the gate does not over-trigger)
- [ ] A run terminated before producing its final result persists the bounded partial stream into the task file under a section distinct from `## Review` — evidence: integration test injects a runner returning a partial capture plus a kill error and asserts the delivered task content contains a `## Salvage` section (or equivalent distinct heading) holding the partial text; `grep -n '## Salvage' <delivered-task>.md` returns line ≥ 1
- [ ] A salvaged partial never posts to GitHub and never satisfies the `## Review`-present idempotency guard — evidence: unit test asserts `advanceIfAlreadyReviewed` returns nil when only the salvage section is present (the `## Review` heading is absent), and a budget-terminated run path never calls the poster — negative probe: `grep -n 'postReview\|posting' <budget-path run log>` returns 0 lines
- [ ] The dependency bump: `go.mod` requires a `github.com/bborbe/agent` version that exposes the streamed partial capture, and `make test` compiles against it — evidence: `grep -n 'github.com/bborbe/agent ' go.mod` returns the bumped version tag; `make test` exits 0

## Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean (exit 0)
- `go test ./pkg/... ./cmd/...` — new config, budget-routing, salvage, fail-closed, and idempotency-guard tests green (exit 0)
- `grep -n 'REVIEW_MAX_DURATION' main.go` — returns line ≥ 1
- `grep -n -i 'unverified\|not verified\|time budget' pkg/prompts/execution.go pkg/prompts/execution_output-format.md` — returns line ≥ 1
- `grep -n 'github.com/bborbe/agent ' go.mod` — returns the bumped version tag

## Operator-executable (runs on the host after PR merge, verification ladder)

Replay the original failure with a forced low budget. Use `Seibert-Data/moss` PR #1 (or any open PR with ≥ 40 changed files across ≥ 5 modules as an equivalent fixture):

1. `go build -o run-task ./cmd/run-task`
2. Prepare a task file for the fixture PR (clone_url, ref, base_ref, task_identifier in frontmatter; `## Plan` with the original concerns), then run:
   `REVIEW_MAX_DURATION=120s ./run-task --task-file /tmp/moss-task.md --phase execution --skip-post`
3. Assert the run terminates (process exits) and:
   - `grep -n 'deadline_exceeded' /tmp/moss-task.md` returns 0 lines
   - `grep -n '## Review\|## Salvage' /tmp/moss-task.md` returns line ≥ 1 (a completed `## Review` with a verdict + addressed/flagged concerns, OR a `## Salvage` holding partial findings)
   - the task file shows no blank `needs_input` verdict — `grep -n -c 'needs_input' /tmp/moss-task.md` returns 0 lines in the review outcome
4. Regression control: run the same task file with the default budget on a small PR (or the fixture with `REVIEW_MAX_DURATION=25m`) and confirm a complete `## Review` with a parsed verdict is written — evidence: `grep -n '"verdict"' /tmp/small-task.md` returns line ≥ 1

## Desired Behavior

1. **Soft budget config.** `REVIEW_MAX_DURATION` is read as a duration env var in both entry points (Kafka pod and local `run-task` CLI), defaults to `25m` (1500s), is validated at startup (positive, minimum floor 60s), and is threaded into the Claude run context for every phase (planning, execution, ai_review). The default sits below the executor's default `ZombieJobTimeoutSeconds` (1800s) with ≥ 5 min headroom for salvage + delivery.
2. **Budget enforcement.** Each Claude run invocation carries the soft deadline. When it fires before a final result arrives, the step detects the budget expiry precisely (`context.DeadlineExceeded` on the run context) as a distinct outcome, terminates the run's investigation, and does NOT treat it as a generic claude failure. Unrelated claude failures (crash, network) keep the existing `failed` / controller-retry path unchanged.
3. **Execution wrap-up contract.** The assembled execution prompt tells the model the time budget and instructs it: stop investigating when the budget is reached, write the verdict from what is already known, and disposition every `## Plan` concern — addressed, confirmed non-issue, or explicitly flagged "not verified". No concern is silently dropped because investigation ended.
4. **Flagged-not-verified + fail-closed verdict.** The execution output format gains an explicit "flagged, not verified" disposition for concerns that could not be examined within budget. A review that flags any concern as not verified must not emit `approve` — the step fail-closes it to `request-changes`, mirroring the existing funnel-failed gate, so an incomplete review can never green-light a PR. A review with no flagged concerns is unaffected: its `approve` passes through unchanged.
5. **Wrapper-side salvage.** When a run terminates before producing its final result, the bounded partial streamed review text (captured by the claude runner) is persisted into the task file under `## Salvage`, a heading clearly marked as incomplete and distinct from `## Review`. The `## Review`-present idempotency guard is never satisfied by a salvaged partial, so a partial can never advance into ai_review on a later trigger.
6. **Bounded failure routing.** A budget-terminated run returns `done` with `NextPhase=human_review`, the salvaged partial attached in the task file, and a message naming the soft budget. It never returns blank `needs_input`, never advances to ai_review, and never posts the partial to GitHub. This applies to every phase: planning, execution, ai_review.
7. **Dependency.** The vendored `github.com/bborbe/agent` claude runner captures the bounded streamed assistant text during `scanOutput` and returns it to the caller on kill/deadline (alongside the existing tail-line error). This repo bumps its `go.mod` to the release containing that capture and consumes it for salvage.

## Constraints

- The K8s hard deadline is NOT changed. `REVIEW_MAX_DURATION` must be below the Job's `ActiveDeadlineSeconds`; the default `25m` satisfies this against the executor default `1800s`. Operators who lower `zombieJobTimeoutSeconds` must keep `REVIEW_MAX_DURATION` below it with headroom for salvage + Kafka delivery — an operator invariant, documented in the env field usage string.
- The dependency: the partial-capture change lives in `github.com/bborbe/agent/claude` (claude-runner.go `scanOutput`), released by the `bborbe/agent` repo first, then consumed here by bumping `go.mod`. Do NOT hand-patch the module cache, fork the runner, or duplicate the claude-CLI invocation in this repo. A companion spec in the `bborbe/agent` repo (worktree `~/Documents/workspaces/agent`, specs/ inbox) must describe the capture before this spec's dependency bump can land.
- Budget-expiry detection is precise and exclusive: only a fired run-context deadline routes to `human_review`; any other claude-run failure keeps the existing `failed` / controller-retry behavior so the retry safety net is preserved.
- The `## Salvage` section heading is distinct from `## Review`. The `## Review`-present guard in the execution step (`advanceIfAlreadyReviewed`) and the ai_review step's `## Verdict`-present guard must never fire on a salvaged partial.
- A salvaged partial is never posted to GitHub as a review. Only a complete `## Review` with a parsed binary verdict (`approve` | `request-changes`) is posted; the binary verdict rule and the existing fail-closed parsing (`pkg/verdict.go`) are unchanged.
- The execution wrap-up contract is added to the assembled instructions in `pkg/prompts/execution.go` — do NOT edit the `/coding:pr-review` plugin file in the coding plugin repo.
- The planning retry loop (`maxPlanningAttempts`) and trigger_count semantics are unchanged; budget-terminated runs are not retried by this agent.
- The three-phase flow, `## Review`/`## Verdict` idempotency guards, and the fail-closed verdict parsing (`pkg/verdict.go`) this spec preserves are documented in `docs/architecture.md` — reference it when implementing so the invariants stay consistent with the documented contract.
- No per-module chunking, no per-phase budgets, no opt-out flag disabling the budget.
- Assumption: the executor's default `ZombieJobTimeoutSeconds` remains 1800s for the deployed pr-reviewer agents.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `bborbe/agent` release with partial capture is not yet available when prompts run | The dependency-bump prompt cannot compile; `go.mod` bump fails | File + land the companion `bborbe/agent` spec first; sequence the bump prompt after the release (see Suggested Decomposition); do NOT vendor-patch |
| Operator sets `REVIEW_MAX_DURATION` at/above the Job's `ActiveDeadlineSeconds` (config drift) | K8s SIGKILLs the pod before the wrapper salvages; partial lost, back to `deadline_exceeded` — detection: task shows `deadline_exceeded` again | Operator keeps the invariant `REVIEW_MAX_DURATION < zombieJobTimeoutSeconds` with headroom; default `25m` < `30m` satisfies it; document in the env usage string |
| Model ignores the wrap-up contract and keeps investigating past the budget | Budget fires; wrapper salvages partial + routes `human_review` (bounded failure, no regression) — detection: `## Salvage` present and `phase: human_review` in the task | None needed — the bounded path is the intended safety net |
| Model emits `approve` while flagging concerns not verified (schema drift / weak model) | Step fail-closes to `request-changes`; never posts `approve` — detection: unit test on the gate + posting log | None needed — fail-closed is the contract; revisit prompt wording if it recurs |
| Salvaged partial exceeds the capture cap (resource exhaustion) | Capture truncates to a bounded size keeping the most recent text — detection: `## Salvage` shorter than the streamed review | Raise the cap in the `bborbe/agent` capture (≥ 16KB floor); the cap is an implementation constant, not an env knob |
| Operator sets `REVIEW_MAX_DURATION` below the 60s floor (misconfiguration) | Startup fails fast with a parse/validation error; no review runs with a near-zero budget — detection: pod startup error / exit non-zero | Set a duration string ≥ 60s; duration parsing (`libtime.ParseDuration`) is TZ- and clock-skew-immune, so no wall-clock ambiguity |
| Claude API retry storm (`api_retry` loop) eats the whole budget on a huge PR | Budget fires mid-retry; salvage + `human_review` route; the run is bounded instead of stuck in retry until K8s kill — detection: `## Salvage` + `phase: human_review` | None needed — bounded; the partial tells the human where the review stopped |

## Security / Abuse Cases

- `REVIEW_MAX_DURATION` is operator-controlled env input. Validate at startup (parse as duration, floor ≥ 60s, positive) so a malformed or near-zero value cannot silently disable the budget or route every review to `human_review` (denial of service). Invalid values fail fast, matching existing env validation.
- Salvage content: the captured partial is LLM streamed text already authored for the task file (the task file already holds `## Plan`/`## Review`/`## Verdict` from the model). Writing it under a controlled markdown heading adds no new trust boundary; it is persisted verbatim as markdown text, never executed or interpreted.
- Budget-expiry must never post to GitHub: an incomplete review posted as a verdict is the worst abuse outcome (a partial `request-changes` or, worse, `approve`). The routing gate (no post on the budget path) plus the fail-closed verdict rule prevent it.
- The `bborbe/agent` capture reads only `stream-json` from the claude subprocess stdout — no new command execution, no widening of the subprocess env allowlist, no new tools.
- No credentials cross new trust boundaries: the budget is a duration, the salvage is task-file text; neither touches `GH_TOKEN`, Anthropic auth, or the subprocess env.

## Suggested Decomposition

Four code layers (config, steps, prompts, dependency) and 7 desired behaviors — split into prompts in this order.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Dependency bump: bump `go.mod` to the `bborbe/agent` release exposing the streamed partial capture; add a small wrapper helper that reads the capture from the runner result/error | 7 | 7 | `bborbe/agent` release (external; companion spec in that repo) |
| 2 | `REVIEW_MAX_DURATION` env config: add to both entry points (pod + run-task), default `25m`, floor 60s, validation, thread into the claude run context | 1 | 1 | — |
| 3 | Budget-kill detection + routing in all three phase steps: wrap each Claude run with the deadline, detect `DeadlineExceeded`, route `done` + `NextPhase=human_review` with a budget-naming message; keep unrelated failures on the `failed`/retry path | 2, 6 | 2, 6 | prompts 1, 2 |
| 4 | Execution wrap-up contract + output-format flagged-not-verified field + approve fail-closed gate | 3, 4 | 3, 4 | — |
| 5 | Wrapper-side salvage: persist the captured partial under `## Salvage`, distinct from `## Review`, never posted; verify the `## Review`-present guard does not fire on a partial | 5 | 5 | prompts 1, 3 |
| 6 | Operator verification replay: forced-low-budget run on `Seibert-Data/moss` #1 (or a 50-file fixture) proving verdict+flagged or salvaged partial, never `deadline_exceeded`/blank `needs_input` | — | 2, 5, 6 (operator rung) | prompts 2, 3, 4, 5 |

Rationale: prompt 1 is the hard seam — everything that consumes the capture (budget-kill salvage, routing) builds on it, and it blocks on the external `bborbe/agent` release, so it is first in sequence and the remaining prompts are gated on it. Prompt 2 (config) and prompt 4 (prompt contract) are independent and can proceed before the release. Prompts 3 and 5 consume both the config and the capture. Prompt 6 is verification-only, after the mechanism is wired. If the `bborbe/agent` release is delayed, prompts 2 and 4 can ship independently without breaking existing behavior.

## Do-Nothing Option

Doing nothing keeps the current state: large multi-file PRs with a deep cross-module rabbit hole burn the 30-minute Job deadline, the streamed partial output is discarded, and the task escalates to `human_review` with a blank `needs_input` and zero findings preserved — exactly the `Seibert-Data/moss` PR #1 outcome (two failed reviews, all 7 concerns missed). The mechanical funnel is already fast and correct; the fix targets the real root cause. Without it, every large PR review remains a coin flip that can consume two full job runs and still produce nothing reviewable.
