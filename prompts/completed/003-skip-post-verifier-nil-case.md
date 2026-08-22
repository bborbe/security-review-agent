---
status: completed
summary: Added case 4 to skip-post boundary test (nil verifier from ResolvePosters) and removed stale resolvePosters comment
execution_id: github-pr-review-agent-skip-post-exec-003-skip-post-verifier-nil-case
dark-factory-version: v0.192.9
created: "2026-08-01T19:20:01Z"
queued: "2026-08-01T19:20:01Z"
started: "2026-08-01T19:20:03Z"
completed: "2026-08-01T19:21:57Z"
---

# Cover the suppressed verifier in the skip-post boundary test

<summary>
- The no-posting mode's highest-stakes safety check finally gets a real test
- Today one suppressed value is proven to reach its consumers; the other is not
- Protects against a change that would let a no-posting run still read from GitHub
- Adds one test case to an existing file — no production code touched
- Also removes a stale name from a comment so the existing checks pass cleanly
</summary>

<objective>
Close the remaining half of the skip-post nil contract. The suppressed poster is now driven through its real consumers, but the suppressed verifier is not — it is discarded at every call site in the boundary test. That leaves the guard with the worst consequence untested: if the verifier ever became a nil concrete pointer inside its interface, a run that is supposed to make no GitHub calls at all would perform a live read.
</objective>

<context>
Read `CLAUDE.md` for test conventions (Ginkgo v2 / Gomega, external `*_test` packages, Counterfeiter mocks).

Read `pkg/skip_post_boundary_test.go` — the file this prompt extends. It has three cases under `Describe("skip-post nil contract")`. **Every one of them writes `poster, _ := factory.ResolvePosters(...)`, discarding the verifier.** Case 3 passes a `mocks.ReviewVerifier` fake instead and asserts `VerifyReviewCallCount() == 1`; that is a positive control proving the fixture reaches the guard, not a test of the suppressed value. Its fixture is the one to reuse — it already clears all three short-circuits.

Read `pkg/factory/runner.go` — `ResolvePosters(cfg RunConfig, botLogin string) (prpkg.PrPoster, prpkg.ReviewVerifier)` returns `(nil, nil)` when `cfg.SkipPost` is true. Both returns are interface-typed; that is the contract.

Read `pkg/steps_review.go` — two guards matter here:
- `if s.verifier != nil && shouldVerify` decides whether verification runs at all.
- `callVerifier` then calls `s.verifier.VerifyReview(...)`, which performs the GitHub read.

Under the regression this protects against, a nil concrete pointer in the interface makes `s.verifier != nil` **true**, so `callVerifier` proceeds and dereferences it — the run either panics or issues the live call. Either outcome is a test failure, which is the point.

Read `pkg/steps_review_test.go` `Context("verification runs and succeeds")` for the canonical fixture shape if case 3's is unclear.
</context>

<requirements>
1. Add a fourth case to `pkg/skip_post_boundary_test.go`, inside the existing `Describe("skip-post nil contract")` block, following the naming style of the three cases already there.

2. Capture **both** return values — `poster, verifier := factory.ResolvePosters(factory.RunConfig{SkipPost: true}, botLogin)` — and pass **both** into `pkg.NewReviewStep`. Do not discard either with `_`, and do not substitute a fake or a literal `nil` for the verifier. Driving the real returned value is the entire point of the case.

3. Reuse case 3's fixture verbatim in shape: `ref:` frontmatter, a GitHub PR URL in the preamble, a `## Review` section, no `## Verdict`, and a mocked runner returning a `verdict: pass` result. This is what makes the verify branch reachable; a fixture missing any of the three would short-circuit before the guard and prove nothing.

4. Assert the run completes without error and does not panic, with `result.Status` equal to `agentlib.AgentStatusDone` and `result.NextPhase` equal to `"done"`. These are the **same** expectations as case 3 — `Run` routes a `pass` verdict to `done` whether or not the verify block ran, because that block only changes the outcome when verification actively fails. Do not expect a different value here.

   Additionally assert that **no verify diagnostic was appended** to the markdown's `## Diagnostics` section. Under the regression, `callVerifier` might return a result rather than panicking; without this assertion the case would then pass silently. This gives it a deterministic failure signal that does not depend on a panic.

5. Add a short comment above the case stating what it protects: that a nil concrete pointer stored in the `ReviewVerifier` interface would satisfy `s.verifier != nil`, reach `callVerifier`, and turn a no-posting run into a live GitHub read.

6. Remove the stale lowercase `resolvePosters` mention from the header comment of `pkg/skip_post_boundary_test.go` — the helper is exported now, and that reference is the sole remaining match for a check that is supposed to return nothing. Reword the sentence rather than deleting it.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Test-only change plus the one comment edit in requirement 6. Do NOT modify any production code, including `ResolvePosters`, `NewReviewStep`, `callVerifier`, or either guard.
- Do NOT modify or delete the three existing cases in `pkg/skip_post_boundary_test.go`. Case 3's fake-verifier positive control stays — it proves the fixture reaches the guard, which this new case depends on.
- Do NOT modify the existing cases in `pkg/steps_review_test.go` or `pkg/steps_checkout_execution_test.go`.
- Do NOT add a CHANGELOG entry. `docs/dod.md` normally requires one; this is a deliberate exception because the existing `## Unreleased` `--skip-post` bullet already covers this work. Do not report the absent entry as an unmet DoD criterion.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` -- must pass.

- `grep -rn 'resolvePosters' pkg/` -- must return **no** matches; the lowercase name must be gone everywhere including comments.
- `grep -n 'poster, verifier := factory.ResolvePosters' pkg/skip_post_boundary_test.go` -- must match at least once, proving both returns are captured rather than one being discarded.
- `grep -c 'poster, _ := factory.ResolvePosters' pkg/skip_post_boundary_test.go` -- must be exactly 3; the three pre-existing cases keep their form and only the new case captures both.
- `grep -c 'factory.ResolvePosters' pkg/skip_post_boundary_test.go` -- must be exactly 4 (the three discarding calls plus the new one), pinning that a case was added rather than an existing one rewritten.
- `go test -mod=mod ./pkg/... -count=1` -- must pass.
</verification>
