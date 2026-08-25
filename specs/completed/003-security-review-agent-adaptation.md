---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-08-24T20:15:53Z"
generating: "2026-08-24T20:16:49Z"
prompted: "2026-08-24T20:34:03Z"
verifying: "2026-08-24T21:14:06Z"
completed: "2026-08-24T21:28:53Z"
branch: dark-factory/security-review-agent-adaptation
---

## Summary

- The repo is a seed of `github-pr-review-agent` v0.5.2 (`origin/master` e7430a8). The code still identifies as the PR reviewer (module, agent name, service name, image name) and contains zero `security-review` code. This spec adapts it into the `security-review-agent` the [[Security Review Mode]] goal's SC4/SC5 require.
- The adapted agent consumes a `security-review` task (`task_type: security-review`, `assignee: security-reviewer-agent`), checks out the target PR, invokes the coding security review (`/coding:pr-review --security`), and writes findings + a binary verdict (`approve` / `request-changes`) into the task's `## Review` section.
- GitHub PR posting is DISABLED for this agent (declared iteration-2 Non-goal of the parent goal): it wires no poster and no verifier, so the run stops at the `## Review` write-back. The binary verdict contract and fail-closed parsing are reused unchanged.
- A manual task-creation trigger (vault skill + script mirroring `github-pr-review-trigger` and the `[[Create PR Review Agent Task]]` runbook) creates `security-review` tasks in the OpenClaw vault.
- Deploy (image push, Helm chart, Config CRD) is owned by a separate task; this spec asserts the image builds and the pod runs as acceptance criteria, without the deploy steps.

## Problem

The parent goal's SC4/SC5 ("`security-review-agent` consumes a `security-review` task, calls the coding security slash command, and writes findings + binary verdict into the task's `## Review` section") are unshipped: the seeded repo still builds and identifies as the PR reviewer, with zero `security-review` references in the tree. The coding side (`--security` in `/coding:pr-review` v0.49.0+) is done; the Go agent half of the two-repo split is the missing piece. Without it, security reviews exist only as a slash command a human can run — not as an autonomous task-driven agent the fleet can execute.

## Goal

After this work, the repo produces a `security-review-agent` binary and image that: identifies as `security-reviewer-agent`; accepts `security-review` tasks; checks out the target PR; runs the coding security review with the `--security` flag so the full security pipeline (recon → derived model → classifier → adjudicator → verifier → blocking, plus the fail-closed toolchain pass) runs; writes the security findings and the binary verdict into the task's `## Review` section; never posts anything to GitHub; routes through the existing planning / execution / ai_review phases with the verdict contract and fail-closed guarantees intact; and ships a manual task-creation trigger skill. When deployed, a real bborbe repo PR can be security-reviewed end to end by a human firing the trigger.

## Non-goals

- Do NOT implement a PR watcher / `.maintainer.yaml: securityScan.enabled` auto-generation — iteration 2.
- Do NOT post verdicts to GitHub PRs — iteration 2; specifically, do NOT add any opt-in flag that re-enables posting on this agent (an escape hatch on the Non-goal is itself a regression; if a future consumer needs posting, that is a separate spec).
- Do NOT add runtime/network probing (design phase 2, gated capability tiers) — not in v1.
- Do NOT build a standalone SAST tool — explicitly the design non-goal; security is a review domain in the coding funnel.
- Do NOT adapt or remove `cmd/cli` (the legacy single-shot local tool). It must still compile under the renamed module, but it is not part of the security agent's task-driven flow.
- Do NOT add a new scenario in this spec — unit + integration tests cover the adaptation; E2E on dev is the separate deploy/verify task's job (spec-writing default: no scenario).
- Do NOT touch the `.dark-factory.yaml` workflow (direct, pr: false).
- Do NOT move the runbook or skill content into the Go repo — the trigger is a vault artifact.

## Acceptance Criteria

- [ ] Module, agent identity, service name renamed — evidence: `grep -n 'github.com/bborbe/security-review-agent' go.mod` returns line ≥1; `grep -n 'github.com/bborbe/github-pr-review-agent' go.mod` returns 0 lines; `grep -n 'security-reviewer-agent' main.go` returns line ≥1; `grep -n 'maintainer-agent-security-reviewer' pkg/factory/factory.go` returns line ≥1
- [ ] Image + docs renamed — evidence: `grep -n 'SERVICE = security-review-agent' Makefile` returns line ≥1; `grep -n 'security-review-agent' docs/dod.md` returns line ≥1; `grep -n 'github-pr-review-agent' README.md` returns 0 lines; `grep -n 'security-review-agent' docs/architecture.md` returns line ≥1 (security variant's no-posting flow, `--security` invocation contract, fail-closed-on-flag-drift documented); `docs/pr-post-back.md` does not read as the security agent's live behavior — evidence: `bash -c 'if [ -f docs/pr-post-back.md ]; then ! grep -q "security-review-agent" docs/pr-post-back.md; else exit 0; fi'` exits 0 (kept-as-historical or renamed/removed both satisfy)
- [ ] Task dispatch routes security-review and refuses pr-review — evidence: unit test in `pkg/factory/` (row names present, `go test ./pkg/factory/...` exits 0) asserts `provider.Get(ctx, "security-review")` returns a non-nil agent, `provider.Get(ctx, "healthcheck")` still returns the liveness agent, and `provider.Get(ctx, "pr-review")` returns an error (refused, never run as a plain review)
- [ ] Execution invokes the security review — evidence: unit test in `pkg/prompts/` (row names present, `go test ./pkg/prompts/...` exits 0) asserts the assembled execution instructions string passed to the runner contains `--security` (wired through the pre-filled arguments, not a comment); `grep -n -- '--security' pkg/prompts/execution.go` returns line ≥1; a fail-closed row asserts assembly returns an error when the runtime-read command file content lacks `--security`
- [ ] No GitHub posting — evidence: `grep -n 'githubposter' pkg/factory/factory.go` returns 0 lines (the security provider builds no poster machinery); unit tests assert the security execution phase writes `## Review` and routes to ai_review with zero `Post` calls on the injected poster fake (call count 0), and the ai_review phase with a nil verifier makes zero `VerifyReview` calls (call count 0)
- [ ] Verdict contract preserved — evidence: `git diff pkg/verdict.go` returns empty (negative, file untouched); existing verdict tests remain green in `go test ./pkg/...`; a new unit test asserts a security `## Review` body containing a Must Fix finding parses to `request-changes` and an empty body fail-closes to `request-changes`
- [ ] Build + test green — evidence: `make precommit` exits 0; `make test` exits 0
- [ ] Local CLI parity — evidence: integration test in `cmd/run-task/` (row present, `go test ./cmd/...` exits 0) asserts the local entry wires the security agent (security task-type route, no poster); operator rung: a local `--phase execution` run against a fixture security task writes `## Review` containing `"verdict"` and routes to ai_review — `grep -n '## Review\|"verdict"' <task-file>` returns line ≥1, `grep -n 'posting\|githubposter' <run log>` returns 0 lines, and the run log shows the security invocation passed through (`grep -n -- '--security' <run log>` returns line ≥1) so a canned `## Review` write cannot satisfy this AC
- [ ] Manual task-creation trigger exists — evidence: `ls ~/Documents/Obsidian/Personal/.claude/skills/security-review-trigger/` succeeds listing `SKILL.md` and `scripts/trigger.sh`; `bash ~/Documents/Obsidian/Personal/.claude/skills/security-review-trigger/scripts/trigger.sh https://github.com/bborbe/<owner>/<repo>/pull/<N> --dry-run` exits 0 and prints the would-be task file path with `task_type: security-review` and `assignee: security-reviewer-agent` (stdout match + exit code)
- [ ] **Post-Deploy (Rung-2):** a pod running the `security-review-agent` image is Running in the dev stage after the separate deploy task lands — evidence: `kubectlquant -n dev get pods | grep security-review-agent` shows `Running`. Deployment itself is executed by the separate deploy task per [[Agent - Deploy New Version]] § Standalone split-repo agents; this AC is the freshness gate and is verifiable only after that deploy lands.
  - `deploy_check:` `if kubectlquant -n dev get pods -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.containers[0].image}{"\n"}{end}' 2>/dev/null | grep -q 'security-review-agent'; then echo deployed; else echo not-deployed; fi`
  - `deploy_target:` `deployed`

## Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean (exit 0). `make generate` must regenerate mocks under the renamed module path.
- `make test` — unit + integration suite green (exit 0)
- `go test ./pkg/... ./cmd/...` — dispatch, prompts, no-posting, verdict-contract, and run-task row names present
- `grep -rn 'github.com/bborbe/github-pr-review-agent' go.mod main.go pkg/ cmd/` — returns 0 lines (negative)
- `grep -n -- '--security' pkg/prompts/execution.go` — returns line ≥1
- `git diff pkg/verdict.go` — returns empty (negative)

## Operator-executable (runs on the host after PR merge, spec verification ladder)

- `VERSION=v0.6.0 make build` exits 0 and `docker images | grep security-review-agent` returns ≥1 line (image builds under the new name)
- Local replay: prepare a fixture security task file (frontmatter `task_type: security-review`, `assignee: security-reviewer-agent`, `phase: planning`, `clone_url`/`ref`/`base_ref` for a real bborbe PR), run `go run ./cmd/run-task --task-file <file> --phase execution`, then `grep -n '## Review\|"verdict"' <file>` returns line ≥1 with a binary verdict
- Trigger: `bash ~/Documents/Obsidian/Personal/.claude/skills/security-review-trigger/scripts/trigger.sh <pr-url> --dry-run` exits 0 (see AC9)
- Post-deploy: the separate deploy task lands the image + Config CRD in dev, then AC10's `deploy_check` prints `deployed`

## Desired Behavior

1. **Security identity.** The binary identifies as the security-review agent everywhere it names itself: the Go module is `github.com/bborbe/security-review-agent`, the agent name is `security-reviewer-agent` (also the Prometheus job grouping), the service name is `maintainer-agent-security-reviewer`, and the built image is `security-review-agent`. Every in-repo reference to the old name (README, CHANGELOG, repo CLAUDE.md, docs) is updated; generated mocks are regenerated under the new module path.
2. **Security task-type dispatch.** A `security-review` task type is defined and routed to the security agent; the `healthcheck` liveness task type keeps its route. The `pr-review` and `pr-override` routes are removed: a task mis-routed to this binary as `pr-review` or `pr-override` is refused via the existing unknown-task-type rejection — it is never run as a plain review and never posted. The security task template (trigger + runbook) uses `task_type: security-review`, `assignee: security-reviewer-agent`, `phase: planning`.
3. **Security review invocation.** The execution phase invokes the coding security review, not the plain review: the assembled execution instructions pass `--security` through the pre-filled arguments so the inlined `/coding:pr-review` command runs its full security pipeline — recon + derived security model, classifier trait groups and invariant selection, adjudicator with model evidence, the verifier falsification gate, and the fail-closed toolchain/dependencies pass — in every review mode. The review output carries the security-specific report sections (Security Findings with per-finding JSON verdict fields and provenance; Security Model provenance block) inside the task's `## Review` body, which ends with the same binary verdict JSON. If the runtime-read command file does not declare `--security` (plugin version drift), the phase fails closed to human_review instead of silently running a non-security review.
4. **No GitHub posting.** The security agent wires no poster and no verifier: the run ends at the `## Review` write-back (vault-first). The execution phase routes to ai_review after writing `## Review` without any GitHub API call; the ai_review phase runs the consistency checks (concerns addressed, no hallucinations, verdict consistency) and writes `## Verdict`, skipping GitHub post-verification and hallucination dismissal. Nothing is written to any GitHub PR; the task file is the only output.
5. **Verdict contract preserved.** The binary verdict (`approve` / `request-changes`), the fail-closed parsing (empty / unparseable / unknown verdict → request-changes), the funnel-failed and unverified-concerns fail-close gates, and the `## Verdict` meta-verdict with `done` / `human_review` routing are reused unchanged from the seed.
6. **Local CLI parity.** The local CLI entry (`cmd/run-task`) runs the identical security flow against a local task file: same security identity, same `--security` invocation, same no-posting guarantee. A local security-review run writes `## Review` with findings + binary verdict into the file and routes through ai_review exactly as the Kafka Job does.
7. **Manual task-creation trigger.** A vault skill + script mirroring `github-pr-review-trigger` and the `[[Create PR Review Agent Task]]` runbook lives at `~/Documents/Obsidian/Personal/.claude/skills/security-review-trigger/`. It takes a PR URL or number, resolves the head SHA via `gh`, and creates a task file in the OpenClaw tasks vault with `task_type: security-review`, `assignee: security-reviewer-agent`, `phase: planning`, `stage`, and the PR URL in the body, then commits and pushes. `--dry-run` prints the would-be task without creating it; success criteria mirror the original (task file observed → exit 0; absent file → exit 1 with a likely-cause hint).

## Constraints

- The module rename (`github.com/bborbe/github-pr-review-agent` → `github.com/bborbe/security-review-agent`), `agentName = "security-reviewer-agent"`, `serviceName = "maintainer-agent-security-reviewer"`, and image `security-review-agent` are frozen — the exact values above, no variation.
- Do NOT widen the execution-phase tool allowlist for the security variant. The exfiltration boundary (read-only git, no network tools, no arbitrary bash) is unchanged; the `--security` toolchain/dependencies pass runs via the Task-tool sub-agent dispatch the coding command already specifies, never by adding binaries/network to the parent allowlist.
- Do NOT change the binary verdict contract or fail-closed parsing — `pkg/verdict.go` is untouched.
- Do NOT edit the coding plugin command file; the agent reads it at runtime and may only probe it for the `--security` contract (fail-closed when absent).
- The three-phase flow and phase names (`planning` / `execution` / `ai_review`) and the `## Plan` / `## Review` / `## Verdict` idempotency guards are unchanged; the vault-first `## Review` write before any external call is preserved.
- The `healthcheck` route is kept; `pr-review` and `pr-override` routes are removed (mis-routed tasks refused, never run as a plain review, never posted).
- The GitHub App auth path (clone + `gh` reads) is unchanged and still required — the security review still clones the PR worktree.
- No opt-in flag re-enables GitHub posting on this agent (see Non-goals).
- The `.dark-factory.yaml` workflow (direct, pr: false) is untouched.
- Reference docs: `docs/architecture.md` (three-phase flow, verdict rubric, posting contract), `docs/dod.md`; parent goal [[Security Review Mode]] (SC4/SC5 + Non-goals); runbook [[Create PR Review Agent Task]]; skill `github-pr-review-trigger`; coding plugin `commands/pr-review.md` (the `--security` contract, position-independent flag mapping to `SECURITY_REVIEW=1`).
- `docs/architecture.md` is updated to document the security variant's flow: no GitHub posting (poster/verifier absent, run stops at `## Review` write-back), the `--security` invocation contract, and the fail-closed-on-flag-drift behavior. The fate of `docs/pr-post-back.md` (a posting flow this agent disables) is resolved — either it documents the pr-reviewer lineage as historical or it is renamed/removed; it must not read as the security agent's live behavior.
- Assumption: the coding plugin installed in the agent image supports `--security` (v0.49.0+; the Dockerfile installs `bborbe/coding` from the marketplace at build time).
- Assumption: the executor delivers `TASK_TYPE=security-review` for security tasks (the separate deploy task sets the Config CRD).
- Assumption: the OpenClaw vault remains the task store for `security-review` tasks.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Coding plugin in the image lacks `--security` (installed version < v0.49.0) | Execution-instructions assembly fails closed to human_review; no `## Review` written as a plain review — detection: task routes to human_review with a message naming the missing flag; pod log line at the assembly error | Install/update the coding plugin in the image (`claude plugin update coding`, rebuilt at `make build`) and re-deploy; do not fall back to a non-security review |
| GitHub API / git clone unavailable (external unavailability) | Existing clone/auth-failure handling: NeedsInput with auth diagnostics; no review, no post | Re-trigger after the outage; existing retry semantics unchanged |
| Security task mis-routed with `pr-review` task_type (executor Config CRD not yet updated) | Refused via unknown-task-type rejection — no plain review runs, nothing posted — detection: a `pr-review` task on the security agent does not spawn a review | The deploy task updates the Config CRD task-type filter to `security-review` before/with the deploy |
| Toolchain pass hits a rate limit / network timeout on a large repo | Toolchain finding surfaces as a Must-Fix fail-closed finding (never a silent skip), or the `REVIEW_MAX_DURATION` soft budget fires and the partial is salvaged to human_review | Existing fail-closed + salvage path (spec 002) unchanged; human reviews the flagged dependency |
| Model investigation overruns the soft budget (resource exhaustion / huge diff) | Existing budget behavior: partial salvaged under `## Salvage`, route human_review, never posted | Existing salvage path unchanged |
| Task re-triggered after `## Review` exists (partial-progress crash / controller retrigger) | `## Review`-present idempotency guard advances to ai_review without re-running the security review | None — designed idempotency |
| Post-Deploy pod check runs before the separate deploy task lands | Freshness gate refuses verification (`deploy_check` prints `not-deployed`) | Sequence: merge → deploy task → then verify-spec; the gate is the intended order-lock |
| Clock skew / TZ edge on the pod | Existing `libtime` handling, UTC job timestamps — no new time logic added | None — unchanged |

## Security / Abuse Cases

- The security agent clones and reviews untrusted PR content in the YOLO container. The existing execution-tool allowlist (read-only git, no network, no arbitrary bash) is the exfiltration boundary and MUST NOT be widened for the security variant; the `--security` toolchain pass runs via the Task-tool sub-agent dispatch the coding command already specifies, not parent-session shell access. An attacker-controlled PR cannot exfiltrate GH_TOKEN or mint tokens.
- No GitHub posting removes the PR-review abuse surface entirely: there is no `autoApprove` gate to bypass and no verdict can be forged onto a PR by this agent.
- The `--security` argument is composed in Go from a constant boolean, not from task content — a malicious task body cannot inject additional CLI arguments or flags into the claude invocation (pre-filled arguments are built from code).
- The trigger script renders the PR title into the task body (markdown, not frontmatter); frontmatter fields (`task_type`, `assignee`, `phase`, `stage`, `task_identifier`) are fixed or validated, never derived from untrusted input. The script must reject URLs that do not match the GitHub PR pattern before creating a task file.
- GH_TOKEN / GitHub App credentials remain in the subprocess env, never written to the task file or logs (existing invariant, unchanged).
- Vault-first: `## Review` is written before any external call; with no post, there is no window in which an external call fails after partial output.

## Suggested Decomposition

Seven desired behaviors across 5 code layers (module/docs, dispatch wiring, prompt assembly, CLI entry, host vault artifact) plus a deploy gate — split into prompts in this order.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Identity + module rename + image/docs: module → `github.com/bborbe/security-review-agent`, agentName `security-reviewer-agent`, serviceName `maintainer-agent-security-reviewer`, Makefile `SERVICE = security-review-agent`, README/CHANGELOG/CLAUDE.md/docs updated, `make generate` regenerates mocks | 1 | 1, 2, 7 | — |
| 2 | Security dispatch + agent wiring: define `security-review` task type, route it to the security agent (nil poster + nil verifier, no githubposter import), drop `pr-review`/`pr-override` routes, keep `healthcheck`; no-posting unit tests | 2, 4 | 3, 5 | prompt 1 |
| 3 | `--security` threading in the execution instructions (pre-filled arguments) + fail-closed when the command file lacks the flag; prompts tests | 3 | 4 | prompt 2 |
| 4 | Verdict-contract lock + local CLI parity: prove `pkg/verdict.go` untouched, add security `## Review` → binary-verdict + fail-closed tests, wire `cmd/run-task` to the security agent + no-posting integration test | 5, 6 | 6, 8 | prompts 2, 3 |
| 5 | Manual trigger skill + script in the Personal vault at `~/Documents/Obsidian/Personal/.claude/skills/security-review-trigger/` — a HOST artifact (the YOLO container does not mount the vault; execute this prompt on the host or author the files directly per the runbook) | 7 | 9 | — |
| 6 | Operator verification + deploy gate: image build (`VERSION=v0.6.0 make build`), local replay, trigger `--dry-run`; coordinate the separate deploy task, then the Rung-2 pod check | — | 2, 8, 9, 10 | prompts 1–5 + separate deploy task |

Rationale: prompt 1 is the mechanical rename seam — every other prompt imports the renamed module, so it lands first. Prompt 2 is the load-bearing behavior (dispatch + no-posting) and can be tested once the module builds. Prompt 3 threads the security flag into the prompt assembly the dispatch wires in. Prompt 4 locks the contract the security agent inherits and proves CLI parity — a regression lock, safest after 2 and 3 exist. Prompt 5 is fully independent (vault artifact) and can run in parallel. Prompt 6 is verification-only and gates on the external deploy task for AC10.

## Do-Nothing Option

Doing nothing leaves the goal's SC4/SC5 unshipped: the seeded repo keeps building and identifying as the PR reviewer with zero `security-review` code, and the security review capability remains a human-run slash command rather than an autonomous agent. The parallel session that was to ship the adaptation has stalled (no pushes in 26 h at takeover); the fleet gains no security-review agent, and the [[Security Review Mode]] goal cannot close. The seed itself is stable and buildable, so there is no risk in waiting — the cost is purely the unshipped feature.
