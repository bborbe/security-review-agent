---
status: cancelled
created: "2026-07-31T21:05:11Z"
queued: "2026-07-31T21:05:11Z"
cancelled: "2026-07-31T21:09:15Z"
---

# Add --skip-post flag to cmd/run-task

<summary>
- The local task-file CLI can run a review without posting anything back to GitHub
- Review findings are still written to the task file exactly as before
- Makes it safe to point that CLI at pull requests the operator does not own
- Closes a gap where it always posted, despite code comments and docs claiming it never does
- Adds the missing safety check that keeps the no-posting mode from crashing during the review phase
- Corrects documentation that describes one local binary using another one's arguments
- No change to how the production Kubernetes path behaves
- No change to what the reviewer concludes — only to whether the verdict is delivered
</summary>

<objective>
Add an opt-in flag to the task-file CLI entry point that suppresses all GitHub write calls, so a reviewer run can be executed against arbitrary public pull requests for measurement and debugging without posting a review to someone else's repository. Today that CLI always constructs a real poster, so pointing it at a third-party PR attempts a real review POST — while several comments and docs state the opposite.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, factory purity, test style).

Read `pkg/factory/runner.go` — the `RunConfig` struct and `RunAgent`. Note the `if agent == nil` branch that unconditionally builds a poster and verifier: this is the branch `cmd/run-task` takes, because it leaves `RunConfig.Agent` nil.

Read `pkg/factory/factory.go` — `CreatePrPoster` and `CreateReviewVerifier`. Both return **interface** types (`prpkg.PrPoster`, `prpkg.ReviewVerifier`), not concrete pointers. This matters: see requirement 3.

Read `cmd/run-task/main.go` — the `application` struct shows the established flag pattern (struct field + `required:` / `arg:` / `env:` / `usage:` / `default:` tags) and how fields are copied into `factory.RunConfig` inside `Run`. Note its existing startup log lines use `glog.V(2)`.

Read `pkg/steps_checkout_execution.go` — find `postAndRoute`. It already opens with a nil-poster early return (`nil poster = skip posting`) returning `Status: done, NextPhase: ai_review`. That downstream branch is the mechanism this prompt makes reachable; do NOT change its behaviour. Note also that `## Review` is written to the markdown *before* `postAndRoute` is called, so suppressing posting does not suppress findings.

Read `pkg/export_test.go` — `PostAndRouteForTest` is the existing seam for driving `postAndRoute` from an external test package. `pkg/factory/export_test.go` is the equivalent seam for `pkg/factory`.

**Two distinct binaries — do not conflate them:**

- `cmd/run-task/` — takes a required `--task-file`. This is the binary this prompt changes.
- `cmd/cli/` — takes a positional PR URL and has a real, working `--comment-only` flag (`cmd/cli/main.go:27`). **Untouched by this prompt.**

Several documents currently describe `cmd/run-task` using `cmd/cli`'s interface. Requirements 10 and 11 correct that mis-attribution; they do not remove anything from `cmd/cli`.
</context>

<requirements>
1. Add a `SkipPost bool` field to `RunConfig` in `pkg/factory/runner.go`, with a doc comment explaining it suppresses all GitHub write calls and is intended for local runs against repositories the operator does not own.

2. Add an unexported helper `resolvePosters` to `pkg/factory/runner.go` with exactly this signature:

   ```go
   func resolvePosters(cfg RunConfig, botLogin string) (prpkg.PrPoster, prpkg.ReviewVerifier)
   ```

   It returns `(nil, nil)` when `cfg.SkipPost` is true, and otherwise the results of `CreatePrPoster` / `CreateReviewVerifier`. Wiring selection only — it performs no I/O itself (calling the existing constructors is fine; they own their own client construction). Note `pkg/factory/runner.go` does not yet import the `pkg` package; use the same `prpkg` alias `pkg/factory/factory.go` already uses. There is no import cycle — `pkg/factory` already depends on `pkg`.

   Put `resolvePosters` in `runner.go`, **not** `factory.go`. `CLAUDE.md`'s "factory functions are pure composition — no conditionals" rule governs the `Create*` constructors in `factory.go`, which must stay conditional-free. The precedent for a `Resolve*` helper carrying an `if` and living outside `factory.go` is `ResolveBotLogin` in `pkg/factory/botlogin.go`. Do not relocate the helper to `cmd/run-task/main.go` — that would break the export seam requirements 8 and 9 depend on.

3. **Return the interface types, never concrete pointers.** A nil concrete pointer assigned into a non-nil interface variable is itself non-nil, which would make `postAndRoute`'s `s.prPoster == nil` check evaluate false and silently post anyway. The signature in requirement 2 is the contract — match it exactly.

4. Use `resolvePosters` in `RunAgent`'s `if agent == nil` branch in place of the current unconditional `CreatePrPoster` / `CreateReviewVerifier` calls.

5. Add a `SkipPost bool` field to the `application` struct in `cmd/run-task/main.go`. Every field in that struct carries a `required:` tag and there is no existing bool to pattern-match, so use the full tag set: `required:"false" arg:"skip-post" env:"SKIP_POST" usage:"Suppress all GitHub write calls; review is written to the task file only" default:"false"`. Pass it through to `factory.RunConfig`.

6. When the flag is set, emit one `glog.V(2)` line at startup recording that posting is suppressed, matching the level of the existing startup lines in that file. No ungated `glog.Info`.

7. **Guard the second consumer of a nil poster.** Making the poster nil-able creates a live path that is currently dead. `pkg/factory/factory.go` passes the *same* `prPoster` into both `executionStep` and `reviewStep`. Only the execution side is guarded (`pkg/steps_checkout_execution.go:316`); `pkg/steps_review.go` calls `s.poster.DismissCurrentReview` inside `tryDismissHallucinated` with no nil check, reachable when the ai_review verdict is `fail` with non-empty hallucinations. With `--skip-post --phase ai_review` that is a nil-pointer dereference. Add an early `if s.poster == nil` return in `tryDismissHallucinated`, mirroring the existing guard style of the URL/platform/ref early-returns just above it, with a `glog.V(2)` line noting dismissal was skipped. Document the field as `poster PrPoster // nil = skip posting` — matching how its sibling `verifier ReviewVerifier // nil = skip verification` is already documented and guarded in the same struct. Add a test driving `reviewStep` with a nil poster down the fail-plus-hallucinations path, asserting it does not panic and routes to `human_review` as it otherwise would. That test belongs in `pkg/steps_review_test.go`, where `Describe("dismiss-and-comment routing")` already has the fail-plus-hallucinations case set up — the new case is that one with `poster` replaced by `nil` and the `DismissCurrentReview` assertions dropped.

   There is a **third** poster consumer, `overrideStep` (`pkg/steps_override.go`), which also calls the poster without a nil check. It needs no guard and you must not add one: it is wired only via `CreateAgentProvider` (the pod path, where the poster is always non-nil), and `CreateAgent` — the branch `cmd/run-task` takes — never builds it.

8. Test `resolvePosters` in both directions. Export it via `pkg/factory/export_test.go` using that file's existing `var X = x` alias convention; the test must live in `pkg/factory/runner_test.go` in package `factory_test`, since the export is only visible there:
   - `SkipPost: true` → both returns are nil **as interfaces** (`Expect(poster).To(BeNil())` on the interface-typed value, never on a concrete cast).
   - `SkipPost: false` → both returns are non-nil.

9. Add a boundary test that traverses the real consumer using the helper's actual return value. **A near-identical test already exists** — `pkg/steps_checkout_execution_test.go` has `Context("when poster is nil")` passing a literal `nil` to `pkg.PostAndRouteForTest` and asserting `NextPhase: "ai_review"`. Do not duplicate it. The new test's entire value is that it passes the value returned by `resolvePosters(RunConfig{SkipPost: true}, …)` rather than a literal `nil`, which is what proves the interface-nil contract survives the round-trip. A literal-`nil` test passes even when the typed-nil bug is present. This test also lives in `pkg/factory/runner_test.go` in package `factory_test`, since the `resolvePosters` export is only visible there.

10. Correct the mis-attributed local-CLI documentation, **preserving the information rather than deleting it**:
    - `CLAUDE.md:87` currently describes `cmd/run-task/` as taking "a PR URL positional arg (`-v`, `--comment-only`)". That description belongs to `cmd/cli`. Rewrite the `cmd/run-task/` bullet to describe its actual interface (`--task-file`, `--phase`, and the new `--skip-post`), and move the positional-PR-URL / `--comment-only` description onto the `cmd/cli/` bullet on the following line, which currently just says "auxiliary local tools".
    - `README.md` lines 29, 32, 33 and 76 describe `cmd/run-task` the same wrong way. Correct the run-modes table row, both example commands, and the tree comment, and add `--skip-post` to the run-modes table. Line 33's example is a `cmd/cli` invocation mislabelled as `run-task` — add a `cmd/cli` row to the run-modes table so that example has a correct home rather than being deleted.

11. Correct the now-false nil-poster claim in exactly two places. `docs/pr-post-back.md:124` and `:128` state `prPoster` is nil "when using `cmd/run-task`" — already untrue today, and the exact gap this prompt closes. For `:128`, say posting is suppressed only when `--skip-post` is set. For `:124`, scope the edit to removing the false `cmd/run-task` attribution only — that paragraph describes the `PostLGTM` path, which has no non-test callers and is marked deprecated, so do not rewrite it into a description of current behaviour. Update the matching code comment at `pkg/steps_checkout_execution.go:315`. Correcting these is explicitly in scope — the "do not change `postAndRoute`" constraint forbids behaviour change, not comment accuracy. Do **not** touch `pkg/factory/runner.go`'s comment on the `Agent` field, or the `PostLGTM` deprecation notes in `pkg/poster_types.go` / `pkg/githubposter/poster.go` — those describe different things and are not affected by this change.

12. Add a `## Unreleased` section to `CHANGELOG.md` with a bullet describing the new flag and the documentation correction. Placement per `docs/dod.md`: the order is `# Changelog` → preamble → `## Unreleased` → `## vX.Y.Z`. Never between the title and the preamble. The section already exists and currently holds one bullet about a dependency bump — **append** your bullet to it; do not replace the section or its existing content.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT modify `cmd/cli/`. Its `--comment-only` flag is real and working; only its *mis-attribution to `cmd/run-task`* in `CLAUDE.md` and `README.md` is being corrected.
- Do NOT change the behaviour of the production Kubernetes path (`main.go`). It dispatches via `CreateAgentProvider` and supplies a non-nil `RunConfig.Agent`, so it must be unaffected; `SkipPost` defaults to false everywhere. Leave the identical inline poster/verifier pair in `CreateAgentProvider` (`pkg/factory/factory.go`) alone.
- Do NOT change `postAndRoute`'s behaviour or its nil-poster early return — this prompt only makes the existing branch reachable. Comment corrections there are in scope; logic changes are not. The new nil guard in `pkg/steps_review.go` (requirement 7) is a separate, required addition, not a change to `postAndRoute`.
- Do NOT change what the reviewer concludes: no edits to prompts, verdict parsing, the funnel, or the fail-closed overrides.
- Do NOT relax or remove the GitHub App credential requirement in `cmd/run-task/main.go`. Cloning still needs a token; only posting is suppressed.
- Do NOT touch the separate stale claim in `CLAUDE.md` that `.maintainer.yaml` `prReviewer.autoApprove` gates the posted event. That drift is real but tracked as its own change — keep this prompt to one concern.
- Existing tests must still pass.
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`, no bare `return err`.
</constraints>

<verification>
Run `make precommit` -- must pass.

Then confirm the flag is wired, not merely declared:

- `grep -n 'SkipPost' pkg/factory/runner.go cmd/run-task/main.go` -- must show the field on both structs and the value being passed from the CLI into `RunConfig`.
- `grep -n 'resolvePosters' pkg/factory/runner.go pkg/factory/export_test.go` -- must show the helper, its use inside `RunAgent`, and its test export.
- `grep -n 'comment-only' cmd/cli/main.go` -- must still return matches; `cmd/cli`'s flag must survive untouched.
- `grep -n 'run-task' README.md CLAUDE.md` -- no matching line may also contain `comment-only` or `positional`. This is the mechanical form of "the mis-attribution is gone".
- `grep -n 'poster == nil' pkg/steps_review.go pkg/steps_checkout_execution.go` -- must return a match in **both** files; the review-step guard is what stops `--skip-post --phase ai_review` panicking.
- `go test -mod=mod ./pkg/... -count=1` -- must pass, including the both-directions helper test, the `resolvePosters`-return boundary test, and the nil-poster review-step test.
</verification>
