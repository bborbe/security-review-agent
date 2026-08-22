---
status: verifying
approved: "2026-08-18T11:53:45Z"
generating: "2026-08-18T11:56:18Z"
prompted: "2026-08-18T13:01:23Z"
verifying: "2026-08-21T12:02:46Z"
branch: dark-factory/bug-execution-allowlist-missing-git-c
---

# Summary

- The pr-reviewer agent's execution-phase allowlist denies `git -C <workdir> ...` commands, even though the model drives git against the `/work/<taskid>` workdir with the `-C` flag.
- When the model calls `git -C /work/<taskid> diff ...` during a review, the command is rejected with `permission_denied` before it runs.
- The review then stalls in an API retry loop and is killed by the 5m heartbeat — the review never posts a verdict and the vault task is left stuck in `planning`.
- Fix (both): (a) add `-C` allowlist entries scoped per read-only git subcommand (glob form, e.g. `Bash(git -C * diff *)`) — NOT a bare `Bash(git -C:*)`, which would also permit write/network commands (`push`, `clean`, `checkout`); and (b) add an execution-prompt guidance line telling the model the cwd is the worktree and to use plain `git` (never `-C`). Plus regression-lock unit tests.

# Problem

`pkg/factory/factory.go` builds `executionTools` — a fixed Claude-Code `AllowedTools` allowlist for the non-interactive execution phase of a PR review. It permits `Bash(git diff:*)`, `Bash(git log:*)`, `Bash(git show:*)`, etc., but not the `git -C <workdir> ...` form. The execution prompt sets the workdir to `/work/<taskid>` and the model (deepseek-v4-flash-max) invokes git as `git -C /work/<taskid> diff ...`, which does not match the `git diff` prefix pattern. Every such call is denied, the Claude run fails, and the review dies. The autonomous review gate silently fails on real PRs.

# Goal

Execution-phase reviews complete even when the model calls git with the `-C <workdir>` flag, so a real PR review always posts a verdict (approve / request-changes / comment) instead of dying on a denied command.

# Reproduction

**Observed incident** — `Seibert-Data/bigquery` PR #5, review task `32d9ce02-d843-5ed5-9104-552c3ac37aa7`, head SHA `4ce99be79aef0946137b076fac6078b1109fd1c9`:

1. Watcher published a `pr-review` task for the head SHA (2026-08-18 07:26).
2. The agent wrote a planning result (07:30), then entered execution.
3. The model issued `Bash` command `git -C /work/32d9ce02-d843-5ed5-9104-552c3ac37aa7 diff 3762333d68eed6fba2fc5208ec8bbac407532108...HEAD -- export/append/pkg/message-handler-event.go` (07:51).
4. Result: `{"type":"system","subtype":"permission_denied","tool_name":"Bash","decision_reason":"This command requires approval","message":"This command requires approval"}` — the command never ran.
5. The Claude run entered an `api_retry` loop (max 10, 501ms delay), then the process died with `signal: killed` (the 5m `heartbeat` watchdog cancelled the stalled run).
6. The failure was written to the task file; the task stayed `phase=planning` / `status=in_progress`; the task file records `execution claude run failed: claude CLI failed: ... permission_denied ... signal: killed`.

**Minimal unit-level repro** (same mechanism, no cluster needed):

```go
allowed := claudelib.AllowedTools{"Bash(git diff:*)"}
// git -C form: NOT matched → this is the bug
matched, err := allowed.Allowed(ctx, "Bash", "git -C /work/<taskid> diff <old>...HEAD -- <file>")
// matched == false, err == permission_denied-class rejection
```

`dark-factory --version` (toolchain used to run the review container): see the YOLO container image `europe-west3-docker.pkg.dev/smedia-octopus-prod/octopus/bborbe/github-pr-review-agent:v0.4.1`.

# Expected vs Actual

- **Expected:** a `git` command that starts with `git -C <workdir> diff ...` is permitted in the execution phase, because `-C <workdir>` is how the execution prompt targets the cloned worktree (`/work/<taskid>`). Documented source: `pkg/prompts/execution.go` runs under `factory.executionTools`; the review clones into `/work/<taskid>` and the model addresses it with `-C`.
- **Actual:** the command is denied with `permission_denied` (`This command requires approval`) because `executionTools` only contains `Bash(git diff:*)`-style prefixes, which do not match a `git -C ...` command line. The review dies.

# Why this is a bug

The execution prompt's own environment drives the model toward `-C` (workdir is `/work/<taskid>`; the model is told the review runs headless under the fixed allowlist). The allowlist therefore contradicts the environment it governs: the only supported way to diff the worktree is rejected. This is the same class of defect as the previously-fixed hardcoded `AllowedTools` gap (completed task `Fix pr-reviewer Agent Config on Octopus (tool permissions + sub-agent model)`): its sub-agent `Task` fallback works around denied `Read`/`Bash(bash:*)`, but does not cover a direct `git -C` call. Without the fix the autonomous review gate silently fails on real PRs.

# Workaround

None for the agent itself. Affected PRs can be unblocked by a human applying the `override-review` label (the override step is pure Go and posts an APPROVE without running the execution phase), or by an org admin admin-merging.

# Acceptance Criteria

- [ ] `pkg/factory/factory.go` `executionTools` contains scoped `-C` entries for the read-only subcommands (e.g. `Bash(git -C * diff *)`) — evidence: `grep -n 'Bash(git -C \* diff \*)' pkg/factory/factory.go` returns line ≥ 1
- [ ] Unit test asserts the `AllowedTools` matcher accepts `git -C <workdir> diff <args>` and `git -C <workdir> log <args>`, still accepts the bare `git diff ...` form, AND denies `git -C <workdir> push <args>` — evidence: the `-C` read-only rows return `matched=true`, the bare `git diff ...` row stays `matched=true`, and the `git -C <workdir> push` row returns `false` (write boundary preserved); removing the scoped `-C` entries from `executionTools` flips the `-C` read-only rows to `false` (regression-lock)
- [ ] The execution prompt instructs the model to run git from the worktree cwd and never use `git -C` — evidence: `grep -n -i 'never.*git -C\|git -C.*not' pkg/prompts/execution.go` returns line ≥ 1 (or equivalent wording in the execution instructions)
- [ ] **Post-Deploy (Rung-2):** a forced review (`force=true` admin-gateway trigger) on a real PR completes execution and posts a verdict at the current head SHA — the original repro (denied `git -C` diff) no longer reproduces. Run it on `Seibert-Data/bigquery` #5 at its current head SHA, or on any other open `Seibert-Data` PR if #5 has merged or moved. Evidence: `gh pr view <n> --repo <owner>/<repo> --json reviews` lists a review by `octopus-pr-reviewer[bot]` whose `commit_id` equals the PR head SHA at trigger time with state `APPROVED` or `CHANGES_REQUESTED` (not missing, not stuck in planning).
  - `deploy_check:` `kubectlprod -n agent get config maintainer-agent-pr-reviewer -o jsonpath='{.spec.image}'`
  - `deploy_target:` `europe-west3-docker.pkg.dev/smedia-octopus-prod/octopus/bborbe/github-pr-review-agent:v0.4.3`

# Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `grep -n 'Bash(git -C \* diff \*)' pkg/factory/factory.go` — returns line ≥ 1
- `go test ./pkg/...` — new matcher test rows pass; suite green
- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean

## Operator-executable (runs on the host after PR merge, verification ladder)

- After the agent image (`v0.4.3`) is deployed and the Config CR `maintainer-agent-pr-reviewer` points at it: trigger a forced review on `Seibert-Data/bigquery` PR #5 via the admin gateway (`.../admin/maintainer-watcher-github-pr/trigger?url=https://github.com/Seibert-Data/bigquery/pull/5&force=true`, browser OAuth), then confirm `gh pr view 5 --repo Seibert-Data/bigquery --json reviews` shows a fresh review by `octopus-pr-reviewer[bot]` at the head SHA (`4ce99be79aef0946137b076fac6078b1109fd1c9`), and the corresponding review task (`tasks/PR Review github - Seibert-Data-bigquery - 5 - 4ce99be7 - ... - prod.md`) is no longer stuck in `planning` — its `## Override`/verdict section reflects a completed run.

# Desired Behavior

1. The execution-phase allowlist accepts `git -C <workdir> diff ...` — no `permission_denied` for the observed command shape.
2. The allowlist matcher accepts `git -C <workdir>` forms for the same read-only git subcommands already covered without `-C` (`diff`, `log`, `show`, `status`, `ls-files`, `fetch`, `worktree`, `branch`, `rev-parse`), via scoped glob entries (e.g. `Bash(git -C * diff *)`) — and still denies write/network subcommands (`push`, `clean`, `checkout`, `reset`, `merge`) in the `-C` form.
3. The bare forms (`git diff ...`, `git log ...`) keep working exactly as before — no entry is removed or re-scoped.
4. A review whose execution phase uses `git -C` completes: it reaches the verdict step and posts a review to GitHub instead of dying in the retry loop.
5. No other allowlist entries are changed, and no permission scope beyond git is added.
6. The execution prompt explicitly tells the model that its working directory is the cloned worktree and to run plain `git` commands from the cwd (never `git -C`), so the model stops producing the denied form in the first place.

# Constraints

- Add only scoped `-C` entries to `executionTools` in `pkg/factory/factory.go` — one glob-form entry per read-only subcommand (`Bash(git -C * <subcmd> *)`, plus a bare form `Bash(git -C * <subcmd>)` if the matcher requires it for no-arg invocations); do NOT add a bare `Bash(git -C:*)` and do not rework or remove existing entries.
- The `AllowedTools` matching semantics are unchanged (new entries only; the glob form `Bash(git -C * <subcmd> *)` is verified to match `git -C <path> <subcmd> <args>` while denying other subcommands).
- The write/network boundary is preserved: `git -C <workdir> push` / `clean` / `checkout` / `reset` / `merge` must remain denied.
- The execution prompt guidance (`pkg/prompts/execution.go`) is updated to tell the model the cwd is the worktree and to use plain `git` (never `git -C`); the guidance must not change the allowlist-driven security model (the scoped entries are the deterministic backstop, the guidance reduces `-C` usage).
- New test uses Ginkgo v2 / Gomega with the existing `AllowedTools` test helper (no live Claude, no network).
- No changes to the execution workdir scheme (`/work/<taskid>`), the re-trigger/dedup semantics, or the heartbeat watchdog.
- The no-space `git -C/path` form is out of scope — the scoped entries match the spaced form the model uses (observed evidence); do not attempt to match the no-space form.

# Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| A scoped `-C` entry matches a write subcommand (glob too loose) | The negative-boundary unit-test row (`git -C <workdir> push <args>` returns `false`) fails | Tighten the glob form (per-subcommand); do not loosen the matcher or add a bare `Bash(git -C:*)` |
| The matcher rejects `git -C` despite the entries (glob semantics differ from assumption) | The new unit test fails | Fix the entry form to match the matcher's actual glob rule (bare vs with-args forms); do not loosen the matcher |
| The model still produces a denied form (e.g. `cd /work && git diff`) | Review fails as before | The scoped `-C` allowlist + prompt guidance cover the `-C` form; `cd && git` is explicitly out of scope — extend `executionTools` in a follow-up if it recurs |
| New image not deployed before verification | Post-Deploy AC's `deploy_check` mismatch flags it | Deploy `v0.4.3` and re-run the Rung-2 AC |

# Suggested Decomposition

Single-layer, single-behavior bug fix — one prompt covers the allowlist entry + regression-lock test + verification.

# Do-Nothing Option

Not applicable for a bug spec (bug-workflow.md: fixing the bug is the only outcome; cost of not fixing = every real PR review whose execution uses `git -C` dies in the retry loop and is heartbeat-killed, leaving stuck `planning` tasks that `force=false` re-triggers can never restart).
