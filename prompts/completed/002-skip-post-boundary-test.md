---
status: completed
summary: Renamed resolvePosters→ResolvePosters in pkg/factory/runner.go, removed the test-only alias from export_test.go, and added pkg/skip_post_boundary_test.go covering all three nil-contract consumers
execution_id: github-pr-review-agent-skip-post-exec-002-skip-post-boundary-test
dark-factory-version: v0.192.9
created: "2026-08-01T17:24:28Z"
queued: "2026-08-01T17:24:28Z"
started: "2026-08-01T17:24:30Z"
completed: "2026-08-01T17:28:42Z"
---

# Add the missing boundary test for the skip-post nil contract

<summary>
- The no-posting mode gains tests that prove it reaches the code which actually branches on it
- Today's test only checks the helper's own output, one step short of where it matters
- Protects against a future change that would silently re-enable posting while every test still passed
- Covers all three places a suppressed value travels to, including the one that could otherwise reach GitHub
- Requires making one internal wiring helper part of the package's real surface
- No behaviour change; the only production edit promotes one internal helper into the package's exported surface
</summary>

<objective>
Prove the skip-post nil contract where it is consumed, not just where it is produced: drive the value returned by the poster-resolution helper into both code paths that branch on a nil poster. A later change of that helper's return types from interfaces to concrete pointers would silently defeat the flag — a nil concrete pointer stored in an interface is itself non-nil — and no current test would fail.
</objective>

<context>
Read `CLAUDE.md` for test conventions (Ginkgo v2 / Gomega, external `*_test` packages, Counterfeiter mocks).

Read `pkg/factory/runner.go` — the unexported `resolvePosters(cfg RunConfig, botLogin string) (prpkg.PrPoster, prpkg.ReviewVerifier)`, returning `(nil, nil)` when `cfg.SkipPost` is true. It returns **interface** types; that is the contract under test.

Read `pkg/factory/export_test.go` — it currently carries `var ResolvePosters = resolvePosters`. That alias is test-only and visible **only** to package `factory_test`.

Read `pkg/export_test.go` — `PostAndRouteForTest(ctx, prPoster PrPoster, md, prURLStr, worktreePath, jobRunTime, funnelRan)`. This is also a test-only symbol, of package `pkg`, visible only to `pkg`'s own test binary.

**Why a production rename is required.** The two seams live in two different packages' `export_test.go` files, so no single test package can reference both. A previous attempt at this test failed to compile with `undefined: pkg.PostAndRouteForTest` for exactly this reason. Making `ResolvePosters` a real exported symbol resolves it: `pkg_test` can then import `factory` and use both.

Read `pkg/steps_checkout_execution.go` — `postAndRoute`'s opening `if s.prPoster == nil` guard, returning `agentlib.AgentStatusDone` and `NextPhase: "ai_review"` with a nil error, before touching any other argument.

Read `pkg/steps_review.go` — `tryDismissHallucinated`'s `if s.poster == nil` guard. Its constructor `pkg.NewReviewStep` is a **production** symbol, so this second consumer needs no export work.

Read `pkg/steps_checkout_execution_test.go` `Context("when poster is nil")` and `pkg/steps_review_test.go`'s nil-poster case. Both pass a **literal** `nil`. Neither is to be modified, and neither proves the contract — a literal `nil` is nil regardless of what the helper returns.
</context>

<requirements>
1. Rename `resolvePosters` to the exported `ResolvePosters` in `pkg/factory/runner.go`, keeping the signature and behaviour identical. Give it a GoDoc comment starting with the name, per project convention. Update its call site in `RunAgent`.

2. Delete the now-redundant `var ResolvePosters = resolvePosters` declaration from `pkg/factory/export_test.go` **together with its doc comment on the line above** — that comment names the lowercase `resolvePosters` and would otherwise dangle and fail verification grep 1. Leave the rest of that file untouched.

3. Add a new test file `pkg/skip_post_boundary_test.go` in package `pkg_test`. Import `pkg` aliased as `pkg` (the form the other seven `pkg/*_test.go` files use) and `pkg/factory` unaliased — note no existing `pkg/*_test.go` imports `factory`, so there is no precedent to copy for that one.

4. Capture **both** return values once — `poster, verifier := factory.ResolvePosters(factory.RunConfig{SkipPost: true}, "<any bot login>")` — and use those values, never a literal `nil`, in all three cases below. Using a literal `nil` anywhere defeats the entire purpose of this file.

5. First case — the checkout-execution consumer. Pass `poster` into `pkg.PostAndRouteForTest`. Assert `Status` equals `agentlib.AgentStatusDone` (the typed constant, not a bare string), `NextPhase` equals `"ai_review"`, and the error is nil. The nil branch returns before reading the other arguments, so keep them minimal — but pass an empty parsed markdown rather than a raw `nil`, so that if the contract ever breaks the test fails with a readable assertion instead of a nil-dereference panic.

6. Second case — the review-step poster consumer. Pass `poster` into `pkg.NewReviewStep` and drive the `verdict: fail` with non-empty hallucinations path, asserting it does not panic and routes to `human_review`. Mirror the argument shapes from the existing nil-poster case in `pkg/steps_review_test.go`.

7. Third case — the review-step **verifier** consumer. This is the highest-stakes guard and is currently untested: `pkg/steps_review.go`'s `if s.verifier != nil && shouldVerify` decides whether `callVerifier` issues a **live GitHub API call**. If the verifier return ever became a concrete pointer, that condition evaluates true and a `--skip-post` run would hit GitHub against a third-party PR. Pass `verifier` (not `nil`) into `pkg.NewReviewStep` alongside `poster`.

   **The fixture must clear three separate short-circuits, or the case proves nothing.** Getting only the first is the trap: the test would pass even under the regression it exists to catch.
   - `## Review` section present — otherwise `shouldVerifyPost` returns false and the `s.verifier != nil && shouldVerify` guard is never evaluated.
   - **A GitHub PR URL in the markdown preamble** — `callVerifier` opens with `githubPRURLPattern.FindString(md.Preamble)` and returns early when empty, *before* touching `s.verifier`. This is the short-circuit most likely to be missed.
   - No pre-existing `## Verdict` section — `Run` early-returns when one is present, skipping the whole verify block.

   Also set `ref:` in the frontmatter and have the mocked runner return a `verdict: pass` result. Mirror the fixture shape from the existing "verification runs and succeeds" case in `pkg/steps_review_test.go`, which already satisfies all of the above. Assert the resulting `NextPhase`.

8. Add a comment at the top of the new file stating what it protects: that `ResolvePosters` must return nil **interfaces**, because a nil concrete pointer stored in an interface is non-nil and would make all three guards fall through — two to real posting, one to a live GitHub read.

9. Grep the repository for any other reference to the old unexported name and update it, so nothing dangles after the rename.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The **only** permitted production change is the `resolvePosters` → `ResolvePosters` rename in `pkg/factory/runner.go` plus its call site. Do not alter its logic, signature shape, or return types. Do not touch `postAndRoute`, `tryDismissHallucinated`, or any other production behaviour.
- Do NOT modify or delete the existing literal-`nil` cases in `pkg/steps_checkout_execution_test.go` or `pkg/steps_review_test.go`. They cover different entry points and stay.
- Do NOT add a CHANGELOG entry. `docs/dod.md` normally requires one, and this is a deliberate exception: the existing `## Unreleased` bullet for `--skip-post` already describes this work. Do not report the absent entry as an unmet DoD criterion.
- Existing tests must still pass.
- Errors use `github.com/bborbe/errors`; no `fmt.Errorf`.
</constraints>

<verification>
Run `make precommit` -- must pass.

- `grep -rn 'resolvePosters' pkg/` -- must return **no** matches; the unexported name is fully gone.
- `grep -n 'ResolvePosters' pkg/factory/runner.go` -- must show the exported function and its call site in `RunAgent`.
- `grep -n 'ResolvePosters' pkg/skip_post_boundary_test.go` -- must show the helper's return value being obtained in the new test.
- `grep -n 'PostAndRouteForTest\|NewReviewStep' pkg/skip_post_boundary_test.go` -- must match both, proving the values are driven through the real consumers.
- `grep -c 'verifier' pkg/skip_post_boundary_test.go` -- must be ≥1; the verifier half of the contract must be exercised, not passed as a literal nil.
- `grep -nE 'github\.com/[^/]+/[^/]+/pull/' pkg/skip_post_boundary_test.go` -- must match. Without a PR URL in the fixture's preamble, `callVerifier` returns before reaching the verifier and the third case silently proves nothing.
- `go test -mod=mod ./pkg/... -count=1` -- must pass.
</verification>
