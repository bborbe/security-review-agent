---
status: completed
spec: [002-pr-reviewer-soft-time-budget-and-salvage]
summary: Bumped github.com/bborbe/agent to v0.82.0 (grep-verified ClaudeResult.Partial streamed-partial capture) and added pkg.ExtractBudgetPartial reading the captured partial with Ginkgo tests + CHANGELOG entry
execution_id: github-pr-review-agent-salvage-exec-009-spec-002-dependency-bump-capture
dark-factory-version: dev
created: "2026-08-21T08:07:20Z"
queued: "2026-08-21T13:01:51Z"
started: "2026-08-21T13:43:41Z"
completed: "2026-08-21T13:47:27Z"
branch: dark-factory/pr-reviewer-soft-time-budget-and-salvage
---

# Bump bborbe/agent to the release exposing the streamed partial capture, and add the extraction helper

<summary>
- The vendored Claude runner dependency is upgraded to the release that captures the streamed assistant text when a run is killed at a deadline, instead of discarding it
- A small, repo-local helper is added that extracts that captured partial from a runner result/error pair, so later steps can salvage it without knowing the runner's internals
- The helper returns an empty string when no capture is present, so completed runs and unrelated failures behave exactly as before
- A unit test locks the extraction against the real, grep-verified capture shape of the bumped dependency
- A hard guard is included: if the newest dependency release does not actually expose the capture, the work stops and reports the dependency as unavailable rather than inventing an API or patching the module cache
- No review behavior changes in this prompt — it only prepares the seam later prompts build on
</summary>

<objective>
Consume the `github.com/bborbe/agent` release whose claude runner surfaces the bounded streamed partial review text on kill/deadline, and add a repo-local `ExtractBudgetPartial` helper so the salvage and budget-routing work in later prompts can read that partial through a stable, tested contract. This prompt is the hard seam: everything that salvages partial output depends on it, and it is gated on the external release existing.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, factory purity, test style).

Read `go.mod` — `github.com/bborbe/agent v0.81.3` is the current direct dependency (line 10).

Read `docs/architecture.md` — the "Result Delivery" and "Three Phases" sections describe the task-file mutation model and phase routing this dependency change supports.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mod-dependency-fix-guide.md` — the `go get` / `go mod tidy` sequence this repo uses (never `go mod vendor`, never `-mod=vendor`)
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega conventions, coverage rules
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` wrapping, never `fmt.Errorf`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and bullet style

Read the CURRENT claude runner at the currently-pinned version to understand what exists today (the reference for "before"):
- Module source at `$(go env GOPATH)/pkg/mod/github.com/bborbe/agent@v0.81.3/claude/claude-runner.go` — `scanOutput` currently keeps only the final `result` event text, a usage summary, and a bounded tail; the streamed assistant text is logged at V(2)/V(4) and otherwise discarded. The bumped release adds the capture this prompt consumes.

The downstream consumers of the helper you add here are the budget-routing and salvage prompts (spec prompts 3 and 5, this spec's Desired Behaviors 5 and 6). They will call `ExtractBudgetPartial` by name with the `(*claudelib.ClaudeResult, error)` pair a failed run returns. Keep the name and signature stable.
</context>

<requirements>
1. **Confirm the release exists before bumping.** Run `go list -m -versions github.com/bborbe/agent` to see the newest released version, and `go get github.com/bborbe/agent@latest` to resolve it. Then GREP the downloaded module source for the streamed-partial capture before writing any code:
   ```bash
   grep -rn "Partial\|capture\|streamed\|scanOutput" \
     "$(go env GOPATH)/pkg/mod/github.com/bborbe/agent@<resolved-version>/claude/"*.go
   ```
   The bump is only valid if `claude/claude-runner.go` (or the `claude` package's result types) now captures the bounded streamed assistant text during `scanOutput` and returns it to the caller on kill/deadline, alongside the existing tail-line error. Document the actual symbol you find (field name, type, and where it lives) — the auditor needs it, and the next prompt depends on it.

2. **Fail-loud guard — do NOT fabricate.** If the newest released `github.com/bborbe/agent` does NOT expose the capture (no such symbol exists in the released `claude` package), STOP here. Do not vendor-patch the module cache, do not fork the runner, do not duplicate the claude-CLI invocation, do not write a helper that always returns `""` to "pass" this prompt. Bump nothing, add nothing, and complete with `"status":"partial"` and a message stating that the `bborbe/agent` release with streamed-partial capture is not yet available (its companion spec must land first). This is the spec's documented failure mode row 1.

3. **Bump the dependency.** With the capture confirmed, update `go.mod` so the `require` line reads the resolved version (e.g. `github.com/bborbe/agent vX.Y.Z`), then run `go mod tidy`. Order matters: write the helper file from requirement 4 FIRST (it imports the already-present `claudelib "github.com/bborbe/agent/claude"`), then tidy — the import already exists elsewhere in the repo, so tidy cannot drop the dependency. Run `go build ./pkg/...` to confirm the repo still compiles against the bumped version; if the bump introduced unrelated breaking changes in the `agent` API, fix them minimally and note them in the completion report.

4. **Add the extraction helper.** Create `pkg/claude_partial.go` (package `pkg`) with exactly this signature:
   ```go
   // ExtractBudgetPartial returns the bounded streamed partial review text the
   // claude runner captured when a run was terminated before its final result
   // event. Returns "" when no capture is present (the run completed normally,
   // or the failure was unrelated to termination). Consumed by the soft-budget
   // routing so a budget-terminated run can salvage what the model wrote.
   func ExtractBudgetPartial(result *claudelib.ClaudeResult, runErr error) string
   ```
   Implement the body against the ACTUAL symbol you grep-verified in requirement 1 (read it from the released `claude` package's result/error types — do not assume a name from training data). If the capture lives on the error, read it from `runErr`; if it lives on a non-nil `ClaudeResult`, read it from `result`; if it lives in both, prefer the error path (a killed run returns a nil result). Return `""` whenever the value is absent or empty. If the released API is shaped so this exact two-arg signature cannot express the capture (e.g. `Run` now returns the partial as an extra value), keep the helper NAME `ExtractBudgetPartial`, extend its signature minimally to express the released shape, and document the final signature prominently in the completion report — the salvage prompt will adapt its one call site. Note: `pkg/claude_partial.go` must use the `claudelib "github.com/bborbe/agent/claude"` alias that every other file in this repo uses.

5. **Test the extraction against the released shape.** Create `pkg/claude_partial_test.go` (external test package `pkg_test`) with a `Describe("ExtractBudgetPartial")` block:
   - A positive row: construct the grep-verified capture shape the way the released `claude` package exposes it (e.g. a `*claudelib.ClaudeResult` with the capture field populated, or an error that carries the partial — whatever requirement 1 found) and assert `ExtractBudgetPartial` returns the captured text.
   - A negative row: the no-capture shape (nil result + unrelated error, or a result with an empty capture) → `Expect(ExtractBudgetPartial(...)).To(Equal(""))`.
   - A completed-run shape (non-nil result with no capture) → `""` as well.
   Follow the Ginkgo v2 / Gomega style of the existing `pkg` test files (e.g. `pkg/review_test.go`).

6. **Update `CHANGELOG.md`.** The `## Unreleased` section currently does NOT exist (latest heading is `## v0.4.5`). Create it in the correct place — `# Changelog` title → preamble (Keep a Changelog / Semantic Versioning lines) → `## Unreleased` → `## v0.4.5`. Add exactly one bullet naming the bumped version:
   ```markdown
   ## Unreleased

   - feat: bump `github.com/bborbe/agent` to the release that captures the streamed partial review text when a claude run is terminated, and add `pkg.ExtractBudgetPartial` so budget-terminated runs can salvage what the model wrote
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT vendor-patch the module cache, do NOT fork the claude runner, do NOT duplicate the claude-CLI invocation. The capture change lives in `github.com/bborbe/agent/claude` (claude-runner.go `scanOutput`), released by the `bborbe/agent` repo first, then consumed here by bumping `go.mod` (spec Constraint, Desired Behavior 7).
- The helper must not change review behavior: completed runs and non-termination failures return `""` and are untouched by this prompt.
- Never run `go mod vendor`; this repo does not commit `vendor/` and `make precommit` uses `-mod=mod` (see go-mod-dependency-fix-guide).
- The `## Unreleased` section order is `# Changelog` → preamble → `## Unreleased` → newest `## vX.Y.Z` (docs/dod.md).
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- `grep -n 'github.com/bborbe/agent ' go.mod` — must return the BUMPED version tag (higher than v0.81.3).
- `grep -n 'func ExtractBudgetPartial' pkg/claude_partial.go` — must return line ≥ 1 with the signature from requirement 4.
- `grep -n 'ExtractBudgetPartial' pkg/claude_partial_test.go` — must show the positive and negative rows.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the new `ExtractBudgetPartial` tests.
- If requirement 2 fired (no capture in the newest release): report `"status":"partial"` and DO NOT claim the greps above pass for a fabricated helper.
</verification>

<!-- AUDITOR NOTES
1. This is the spec's hard seam (failure mode row 1). The streamed-partial capture API is defined by the companion `bborbe/agent` spec (worktree `~/Documents/workspaces/agent`, specs/ inbox), which must land and release BEFORE this prompt is approved and run. The exact capture symbol (field name / error type / return arity) cannot be pinned here — it does not exist in the currently-pinned v0.81.3, and inventing a name would be a hallucination. This prompt therefore requires the executor to discover the symbol from the released module source (requirement 1) and implement the fixed-name repo-local helper against it (requirement 4), with a fail-loud guard (requirement 2). Reviewers: confirm the `bborbe/agent` release is available before approving this prompt.
2. If the companion spec chooses a return-arity that cannot map into `ExtractBudgetPartial(result, runErr)`, the helper signature may need a third parameter; requirement 4 instructs the executor to keep the NAME stable and document the signature, and the salvage prompt (5) adapts its single call site.
-->
