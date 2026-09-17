---
status: prompted
approved: "2026-09-17T19:03:27Z"
generating: "2026-09-17T19:04:29Z"
prompted: "2026-09-17T19:15:31Z"
branch: dark-factory/bug-build-fix-requires-github-app-auth
---

## Summary

- The `build-fix` task type cannot run at all: the binary resolves GitHub App credentials **unconditionally at startup** and aborts when they are absent, before the task type is ever consulted.
- The build-fix agent is documented in-repo as code-only — `docs/architecture.md:81` describes it as "single code-only step under `execution` | **no Claude, no clone**" — so its deploy config deliberately carries no `APP_ID` / `INSTALLATION_ID` / `PEM_KEY`.
- Every dispatch therefore dies at startup with `security-reviewer auth: GitHub App credentials not configured`, proven live on the dev cluster on 2026-09-17.
- Supplying the credentials is **not** the fix: it would hand a code-only analysis agent repo-write access its own criteria forbid.
- The fix is to make GitHub auth conditional on the task type. Of the three registered types (`pkg/factory/factory.go:271-273`), exactly one acts on a repository: `security-review`.

## Problem

A task type that needs no GitHub access still pays for it at startup, and pays with its life. The agent never reaches its own first step, so the failure reads as an agent bug rather than an auth-scope bug, and it is invisible to every unit test — the defect lives in the startup path, not in the step under test.

## Goal

A `build-fix` task, and a `healthcheck` task, run to completion in an environment with no GitHub App credentials configured. `security-review` still fails fast and loudly when its credentials are missing.

## Reproduction

Environment: ExampleFleet **dev** cluster (GKE example-cluster, `kubectl-example`), `agent` namespace; build-fix Config applied 2026-09-16T21:07:26Z; mirror image `example-fleet/agent-build-fix:dev` at `security-review-agent` v0.7.1.

1. Create a task in the shared agent vault (`example-org/obsidian-agent`) carrying `stage: dev`, `status: in_progress`, `phase: planning`, `assignee: build-fixer-agent`, `task_type: build-fix`.
2. The dev executor dispatches it. Observed job: `build-fixer-agent-fa11341c-20260917182058`.
3. Read the pod log:

```bash
kubectl-example -n agent logs build-fixer-agent-fa11341c-20260917182058-mpzjl
```

Observed, verbatim (last two lines; the pod exited non-zero after 40s):

```
I0917 18:21:12.424187       1 main.go:151] review max duration=25m
E0917 18:21:12.459816       1 service_main.go:98] application failed: security-reviewer auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)
```

The Config supplies no `APP_ID`, `INSTALLATION_ID`, `PEM_KEY_FILE` or `PEM_KEY` — by design. No `build-fix` job had ever run before this one, so the defect had never been observed.

## Expected vs Actual

**Expected**, per `docs/architecture.md:81` (the `build-fix` row reads "no Claude, no clone") and `docs/architecture.md:97` ("It never advances to `done`, never modifies code, and never files a second task"): a `build-fix` task starts, fetches the build log, enriches the task, and exits — with no GitHub App credentials present anywhere in its environment.

**Actual**: startup aborts at `main.go:153` → `resolveAuth` (`main.go:289-296`), which returns an error whenever `AppID == 0 || InstallationID == 0 || (no PEM file && no PEM content)`. `resolveAuth` is called **before** `provider.Get(ctx, agentlib.TaskType(a.TaskType))` at `main.go:278`, so the task type cannot influence it.

## Why this is a bug

It contradicts a documented in-repo invariant, and the contradiction is load-bearing rather than cosmetic: the "no credential" posture is what keeps this agent from holding repo-write access. A fix that satisfies the runtime by adding credentials breaks the security property; only a fix in the code satisfies both.

## The two entrypoints are NOT symmetric

This is the part a lazy implementation gets wrong, so it is stated as a premise rather than left to the implementer.

- **`main.go`** carries `TaskType` on the `application` struct, parsed from `TASK_TYPE` (`:125`), and calls `resolveAuth` at `:153`. Gating there is a one-line condition.
- **`cmd/run-task/main.go`** has **no `TaskType` field**. It derives the type from the task frontmatter *inside* `dispatchAgent` (`md.Frontmatter.TaskType()`, `:212`), and `dispatchAgent` is called at `:153` — **after** `resolveAuth` at `:144`. At the moment credentials are resolved, the task type is not yet known.

So `cmd/run-task` cannot be fixed by the same one-line condition; it needs the task content parsed **before** credential resolution. Constraint: `resolveAuth` keeps its current signature, so the reordering happens in `Run()` — parse first, then resolve, then dispatch.

## Acceptance Criteria

Registered task types are exactly `security-review`, `healthcheck`, `build-fix` (`pkg/factory/factory.go:271-273`). Repository-acting set: **`{security-review}`**. Non-repo-acting set: **`{healthcheck, build-fix}`**. Both sets are fixed by this spec, not left to implementation.

**Every Ginkgo evidence command below must report a non-zero spec count.** `go test -run` filters *Go test function names*, not Ginkgo spec text — both suites bootstrap as a single `func TestSuite(t *testing.T)`, so `-run 'application.resolveAuth'` matches nothing and exits 0 with `[no tests to run]` on the **unfixed** tree. Verified 2026-09-17: `go test ./ -run 'application.resolveAuth' -count=1` → `ok … [no tests to run]`, exit 0. Hence `-ginkgo.focus` plus `-v`, and the non-zero-count requirement.

`-ginkgo.focus` matches **`Describe` text**, not spec content — so the new specs must be registered under the `Describe` each AC names. A correct fix that files them under a differently-named container would fail the count floor while being right; widening the focus regex is the alternative if the implementer prefers other naming.

- [ ] `make precommit` exits 0 — evidence: exit code 0.
- [ ] `build-fix` starts with no App credentials — evidence: `go test ./ -v -ginkgo.focus='application.resolveAuth' -count=1` exits 0, reports `Ran N of M Specs` with **N ≥ 3**, and includes a spec asserting the resolved token is empty **and** the returned error is nil for `TaskType: build-fix` with zero `AppID` / `InstallationID` / PEM.
- [ ] `healthcheck` starts with no App credentials — evidence: the same command, including an equivalent spec for `TaskType: healthcheck`. **This AC exists because the laziest fix (`!= build-fix ⇒ repo-acting`) passes every other criterion while introducing a new startup failure for `healthcheck`.**
- [ ] `security-review` still fails fast without credentials — evidence: the same command, including a spec asserting a non-nil error whose message contains `APP_ID` for `TaskType: security-review` with zero App credentials.
- [ ] An unset or unknown task type keeps the stricter behaviour — evidence: the same command, including specs asserting a non-nil error containing `APP_ID` for `TaskType: ""` and for an unregistered value. (This is what keeps `auth_resolve_test.go:27`'s `&application{}` case passing unchanged.)
- [ ] `GH_TOKEN` is absent — not empty — in the agent environment when no token was resolved, and present when one was — evidence: a spec that resolves credentials for the no-credential `build-fix` case and then calls `dispatchAgent` with the resulting token, asserting the environment map has no `GH_TOKEN` key; plus a spec calling `dispatchAgent` with a **non-empty literal token**, asserting the map has a `GH_TOKEN` key. The positive direction must **not** mint: `githubapp.MintIAT` is a live HTTP mint, and the existing suite is deliberately hermetic (`auth_resolve_test.go:15-18`).
- [ ] `cmd/run-task` resolves credentials only for repo-acting types — evidence: `go test ./cmd/run-task/ -v -ginkgo.focus='task type dispatch' -count=1` exits 0, reports `Ran N of M Specs` with **N ≥ 7** (the focused suite reports 6 on the unfixed tree — the floor is what proves a new spec landed, so the count itself discriminates), and the spec drives `Run()`'s **real ordering** — read task content → resolve credentials → dispatch — for a `build-fix` task with no credentials, asserting an empty token and a nil error. A spec that sets a field and calls `resolveAuth` directly does **not** satisfy this: it never exercises the `:144`-before-`:153` sequence the fix must correct.
- [ ] **Post-Deploy (Rung-2):** the reproduction no longer reproduces on dev — evidence: a dispatched `build-fix` task produces a job whose log has `grep -c 'GitHub App credentials not configured'` returning 0 **and** `grep -c 'build-fix: enriched task build_id='` returning ≥1, with the task's frontmatter showing a cleared `assignee`.
  - `deploy_check:` `kubectl-example -n agent get pods --sort-by=.metadata.creationTimestamp -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[0].imageID}{"\n"}{end}' | grep build-fix | tail -1 | awk '{print $2}' | sed 's|.*@||'`
  - `deploy_target:` `$(gcloud artifacts docker images describe europe-west3-docker.pkg.dev/smedia-example-fleet-dev/example-fleet/agent-build-fix:dev --format='value(image_summary.digest)')`

  This gate is **not vacuous**, unlike a literal `:dev` comparison: `deploy_check` emits the digest of the newest build-fix pod actually scheduled on dev, while `deploy_target` expands to the digest GAR currently serves for `:dev`. A pod that predates the post-fix republish carries a different digest, and Phase 0.5 refuses before the AC walk.

**This gate is a consistency check, not a substitute for the republish.** It proves the scheduled pod matches what the registry serves; it cannot prove the registry carries the fix. An operator who re-dispatches without republishing the mirror would see both sides equal and the gate pass against a pre-fix deployment — which is why the AC's own log evidence is load-bearing and fails independently (a pre-fix pod logs the auth error, so `grep -c` returns 1). The republish step above is mandatory, not optional. The gate also assumes the finished Job's pod is still retained at verification time; if the executor garbage-collects it, `deploy_check` emits empty and the gate refuses — conservative, and self-consistent with the log evidence.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean
- `make test` — unit + integration suite passes, including the seven specs above
- `grep -n 'requiresRepositoryAccess' main.go cmd/run-task/main.go` — the predicate is present at **both** call sites. The name is frozen by Constraint, so this grep is a contract check, not an incidental identifier match.

### Operator-executable (runs on the host after PR merge)

- `VERSION=vX.Y.Z make build upload` — the release image publishes
- in the example-fleet deploy repo (`example-org/agent`), `BRANCH=dev make build upload` from `images/agent-build-fix/` — republish the mirror
- re-run the reproduction above; the Rung-2 AC above is the evidence

## Desired Behavior

1. Startup resolves GitHub App credentials only for task types in the repo-acting set `{security-review}`.
2. A repo-acting task type with no credentials still fails fast, before any other work, with the existing error message naming `APP_ID`.
3. `build-fix` and `healthcheck` start with an empty token and no `GH_TOKEN` in the agent environment.
4. An unset or unknown task type takes the stricter branch, so an unknown type fails loudly rather than silently running without auth.
5. `cmd/run-task` parses the task content **before** resolving credentials, so it can gate on the same set despite having no `TaskType` field.
6. No change to the error message text, so existing operator runbooks and alerts keep matching.

## Constraints

- The repo-acting set is **`{security-review}`**, hardcoded. It is not a runtime configuration knob, and it is not a per-task-type default derived at call time.
- The predicate is named **`requiresRepositoryAccess`** and lives in both `main.go` and `cmd/run-task/main.go`. Frozen so the Verification grep is meaningful.
- The `GH_TOKEN` environment variable must remain absent — not set to an empty string — when no token is resolved; `main.go:234`'s `if resolvedToken != ""` guard is the contract.
- `resolveAuth` stays unexported and keeps its current signature; the existing white-box specs in `auth_resolve_test.go` must keep passing unchanged. The `cmd/run-task` reordering therefore happens in `Run()`, not by widening the signature.
- The agent must never hold a credential it does not use — no credential may be supplied to satisfy this bug.
- `docs/architecture.md` gains one line recording the rule (credential resolution is gated on the repo-acting set), so it does not die with this spec.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `security-review` runs with no credentials | Fails fast at startup with the existing `APP_ID` error, before any clone or review work | Operator configures the App credentials for that agent's Secret, then re-dispatches and confirms `grep -c 'GitHub App credentials not configured'` returns 0 in the new pod log |
| `healthcheck` runs with no credentials | Starts normally, empty token, no `GH_TOKEN` | None required — this is the fixed behaviour |
| `build-fix` runs with no credentials | Starts normally, empty token, no `GH_TOKEN` | None required — this is the fixed behaviour |
| A non-repo-acting type is accidentally given credentials | Starts normally; the token is resolved but unused — no crash | None required; the environment is over-provisioned but harmless |
| Task type unset, or set to an unregistered value | Treated as repo-acting — the stricter branch — so it fails loudly rather than silently running without auth | Operator sets the task type. The two paths differ: `main.go` reads `TASK_TYPE` (`:125`), while `cmd/run-task` derives it from the task frontmatter (`:212`) |
| `cmd/run-task`'s reorder is done wrong — parse moved but `dispatchAgent` still re-parses, or the type is read before the parse | The `cmd/run-task` path keeps the defect while `main.go` is fixed | Prevented by the AC that drives `Run()`'s real ordering, not `dispatchAgent` in isolation |

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Gate credential resolution on the repo-acting set in `main.go` — predicate + full spec table | 1–4, 6 | 1–6 | — |
| 2 | Reorder `cmd/run-task`'s `Run()` so the task is parsed before credentials are resolved, then apply the same gate | 5, 6 | 7 | prompt 1 (uses the same predicate) |

Two prompts, not one — the entrypoints are **not** symmetric (see the section above). Prompt 2 carries the ordering change, which prompt 1 does not need and must not assume. Splitting is deliberate: a single prompt would let the implementer treat `cmd/run-task` as a copy of `main.go`, which is exactly the failure this decomposition prevents.
