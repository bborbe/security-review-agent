---
status: completed
spec: [003-security-review-agent-adaptation]
summary: Renamed the module, binary identity, service name, and image from the pr-reviewer to the security-reviewer agent — module github.com/bborbe/security-review-agent, agentName security-reviewer-agent, serviceName maintainer-agent-security-reviewer, image security-review-agent — swept all imports, regenerated mocks, and rewrote README/docs/CLAUDE.md/CHANGELOG to the security identity
execution_id: security-review-agent-security-exec-011-spec-003-security-identity-and-rename
dark-factory-version: dev
created: "2026-08-24T20:22:59Z"
queued: "2026-08-24T20:44:38Z"
started: "2026-08-24T20:44:39Z"
completed: "2026-08-24T20:54:03Z"
branch: dark-factory/security-review-agent-adaptation
---

<summary>
- The repo's Go module path changes from `github.com/bborbe/github-pr-review-agent` to `github.com/bborbe/security-review-agent`, and every import in Go code follows.
- The binary identifies as `security-reviewer-agent` and the service name becomes `maintainer-agent-security-reviewer`.
- The Makefile builds and tags the image as `security-review-agent`.
- The README is rewritten to describe the security-review agent (consumes `security-review` tasks, runs the coding security review, never posts to GitHub) with zero references to the old name.
- The architecture doc, Definition of Done, releasing guide, and GitHub-App-setup doc all describe the security agent's flow (no posting, `--security` invocation, fail-closed on flag drift).
- The posting-flow doc no longer reads as the security agent's live behavior — it is marked as pr-reviewer-lineage historical documentation.
- Generated mocks are regenerated under the renamed module path.
- `pkg/verdict.go` is left byte-identical — the verdict contract is untouched by this prompt.
- The legacy `cmd/cli` tool only gets its import paths updated; its behavior and usage strings are unchanged (spec non-goal).
- The repo CLAUDE.md, the local-CLI fixture task, and CHANGELOG's `## Unreleased` are updated to the security identity.
</summary>

<objective>
Rename the module, binary identity, service name, image name, and all in-repo documentation from the PR-reviewer to the security-reviewer agent, so every later prompt in this spec builds on the correct identity and the repo no longer identifies as the PR reviewer anywhere it names itself.
</objective>

<context>
Read CLAUDE.md (repo root) for project conventions, then read:
- `go.mod` — the module line to rename.
- `main.go` — the `agentName` const and identity strings to update.
- `pkg/factory/factory.go` — the `serviceName` const and package doc.
- `Makefile` — the `SERVICE =` line.
- `README.md`, `CHANGELOG.md`, `docs/dod.md`, `docs/architecture.md`, `docs/pr-post-back.md`, `docs/github-app-setup.md`, `docs/releasing-github-pr-review-agent.md`, `cmd/run-task/dummy-task.md` — all identity-bearing files.

Follow the coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `go-mod-dependency-fix-guide.md` for the module-path rename mechanics
- `changelog-guide.md` for the `## Unreleased` entry format
- `readme-guide.md` for the README rewrite
- `go-precommit.md` for linter limits (funlen 80, golines 100) when editing comments

The module rename is a mechanical sweep: every `github.com/bborbe/github-pr-review-agent` prefix becomes `github.com/bborbe/security-review-agent`. Do NOT touch any other module path (e.g. `github.com/bborbe/maintainer`, `github.com/bborbe/agent`).
</context>

<requirements>
1. **Rename the Go module.** In `go.mod`, change `module github.com/bborbe/github-pr-review-agent` to `module github.com/bborbe/security-review-agent`. Do not touch the `replace` directives or any dependency version.

2. **Sweep every import.** Replace the module prefix `github.com/bborbe/github-pr-review-agent` with `github.com/bborbe/security-review-agent` in every `.go` file that references it — `main.go`, `main_test.go`, `main_internal_test.go`, `auth_resolve_test.go`, `domain_normalize_test.go`, `cmd/cli/main.go`, `cmd/run-task/main.go`, `cmd/run-task/main_test.go`, `cmd/mint-iat/main.go`, all of `pkg/`, and all of `mocks/`. This includes the `gexec.Build("github.com/bborbe/github-pr-review-agent/...", ...)` package paths in `main_test.go` and `cmd/run-task/main_test.go`. Scope the sweep to the repo's own module prefix only.

3. **Binary identity.** In `main.go`:
   - `const agentName = "pr-reviewer-agent"` → `const agentName = "security-reviewer-agent"` (this is also the Prometheus job grouping — keep the const's use sites unchanged, only the value changes).
   - Package doc comment: `// Command maintainer-agent-pr-reviewer is the Kafka entry point for the PR-review agent` → describe it as the security-review agent (`security-reviewer-agent`).
   - `glog.V(2).Infof("maintainer-agent-pr-reviewer started phase=%s", a.Phase)` → `"maintainer-agent-security-reviewer started phase=%s"`.
   - Kafka sync producer name `"agent-pr-reviewer"` → `"agent-security-reviewer"`.
   - Auth error strings `"pr-reviewer auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)"` and `"pr-reviewer auth mode=github-app app_id=%d installation_id=%d"` → prefix `"security-reviewer"` instead of `"pr-reviewer"`.

4. **Service name.** In `pkg/factory/factory.go`: `const serviceName = "maintainer-agent-pr-reviewer"` → `const serviceName = "maintainer-agent-security-reviewer"`. Update the package doc comment (`// Package factory wires concrete dependencies for the maintainer-agent-pr-reviewer binary.` → security-reviewer). Do NOT touch `CreateAgent`, `CreateAgentProvider`, or any function bodies in this prompt — dispatch wiring is a later prompt. Sweep the remaining `maintainer-agent-pr-reviewer` / `pr-reviewer` identity strings that name the agent: the `// RunAgent performs the shared startup + execution flow for the maintainer-agent-pr-reviewer binary.` comment in `pkg/factory/runner.go` (~line 78), the `// CreateAgentProvider wires the per-task-type dispatch table for maintainer-agent-pr-reviewer.` comment in `pkg/factory/factory.go` (~line 247), and the `"pr-reviewer fail-closed to request-changes: reason=%q ..."` log string in `pkg/steps_checkout_execution.go` (~line 384) — all become the `security-reviewer` identity.

5. **Image name.** In `Makefile`: `SERVICE = github-pr-review-agent` → `SERVICE = security-review-agent`. The image is built/tagged as `docker.io/bborbe/security-review-agent:vX.Y.Z` by the existing `build`/`buca` targets. Leave `Makefile.docker`, `Makefile.precommit`, `Makefile.variables` untouched unless they hardcode the old image name (they don't). Update `.gitignore`'s stale binary/build-artifact entries `/pr-reviewer` and `/github-pr-review-agent` → `/security-reviewer-agent` and `/security-review-agent`.

6. **Local CLI entry docs + fixture.** In `cmd/run-task/main.go`:
   - Package doc `// Command run-task is the local-CLI entry point for maintainer-agent-pr-reviewer.` → `... for maintainer-agent-security-reviewer.`
   - Auth error + log strings `"pr-reviewer auth: ..."`, `"pr-reviewer auth mode=github-app ..."` → `"security-reviewer ..."`.
   - `glog.V(2).Infof("pr-reviewer skip-post enabled — GitHub writes suppressed")` → `"security-reviewer skip-post enabled — GitHub writes suppressed"`.
   - Do NOT change any struct field, flag, or behavior in `cmd/run-task` in this prompt (dispatch wiring is a later prompt).
   - In `cmd/run-task/dummy-task.md` (the local-CLI fixture), update the frontmatter `assignee: pr-reviewer-agent` → `assignee: security-reviewer-agent`. The review-body sample text may stay as historical fixture content.
   - In `cmd/run-task/Makefile`, update the `generate-dummy-task` target that echoes `assignee: pr-reviewer-agent` (line ~33) → `assignee: security-reviewer-agent`, otherwise `make generate-dummy-task` regenerates the fixture back to the old identity and contradicts this prompt.

7. **README rewrite.** Rewrite `README.md` so it contains ZERO occurrences of the literal `github-pr-review-agent` and describes the security-review agent: it consumes `security-review` tasks (`task_type: security-review`, `assignee: security-reviewer-agent`), checks out the target PR, runs the coding security review (`/coding:pr-review --security`), writes findings plus the binary verdict (`approve` / `request-changes`) into the task's `## Review` section, and NEVER posts to GitHub (the run ends at the vault write-back). Keep the three-phase flow (planning / execution / ai_review), the run modes table (Kubernetes Job `main.go`, local CLI `cmd/run-task`, legacy `cmd/cli`), and the image name `docker.io/bborbe/security-review-agent`. Update the file map and doc links (`docs/architecture.md`, `docs/github-app-setup.md`, `docs/releasing-security-review-agent.md`).

8. **docs/dod.md.** Update so it references `security-review-agent` (AC evidence `grep -n 'security-review-agent' docs/dod.md` returns line >= 1). Replace the "Review-Posting Safety (load-bearing — this agent writes to other people's PRs)" section with a "No GitHub Posting" section reflecting the security agent's contract: no poster, no verifier, the run stops at the `## Review` write-back, clones are throwaway scratch at the PR head, `REPO_ALLOWLIST` is still honored, and no code path may add a poster. Fix the image name in the Build section (`docker.io/bborbe/security-review-agent:vX.Y.Z`).

9. **docs/architecture.md.** Update to describe the security variant (AC evidence `grep -n 'security-review-agent' docs/architecture.md` returns line >= 1). Concretely:
   - Retitle to the security-review agent and update the phase table: execution writes `## Review` (vault-first) and routes to `ai_review` WITHOUT posting; ai_review runs the consistency checks (concerns addressed, no hallucinations, verdict consistency) and writes `## Verdict`, skipping GitHub post-verification and hallucination dismissal.
   - Replace the "Posting Reviews Back to GitHub" section with a "No GitHub Posting" section: the security agent wires a nil poster and a nil verifier, so the run ends at the `## Review` write-back; nothing is written to any GitHub PR. Reference `docs/pr-post-back.md` only as pr-reviewer-lineage history.
   - Document the `--security` invocation contract: the execution phase assembles the inlined `/coding:pr-review` procedure with `--security` passed through the pre-filled arguments so the full security pipeline (recon → security model → classifier → adjudicator → verifier → fail-closed toolchain/dependencies pass) runs, and fail-closed-on-flag-drift: if the runtime-read plugin command file does not declare `--security`, the phase fails closed to human_review instead of running a non-security review. (The code change itself lands in a later prompt; the doc states the contract now.)
   - Update the file map entries (main.go, cmd/run-task, pkg/prompts/execution.go as the security-rubric source of truth).

10. **docs/pr-post-back.md fate.** Keep the file (do not delete it) but make it read as pr-reviewer-lineage history, NOT the security agent's live behavior. Add a banner at the top, e.g.:
    > Historical (pr-reviewer lineage): this document describes the pr-reviewer posting flow. The current agent does not post to GitHub — the run ends at the `## Review` write-back. Retained as historical documentation of the posting contract; it does not describe current behavior.
    The banner must NOT contain the literal string `security-review-agent` (AC evidence `bash -c 'if [ -f docs/pr-post-back.md ]; then ! grep -q "security-review-agent" docs/pr-post-back.md; else exit 0; fi'` must exit 0).

11. **Releasing guide.** Rename `docs/releasing-github-pr-review-agent.md` to `docs/releasing-security-review-agent.md` (create the new file with updated content — image `docker.io/bborbe/security-review-agent:vX.Y.Z`, single-artifact description — then delete the old file). Update the reference in the repo `CLAUDE.md` (`[docs/releasing-github-pr-review-agent.md](docs/releasing-github-pr-review-agent.md)` → `[docs/releasing-security-review-agent.md](docs/releasing-security-review-agent.md)`).

12. **docs/github-app-setup.md.** Update identity references that name the agent (`pr-reviewer` → `security-reviewer` where it describes the current agent; the historical "In progress" note at the bottom may stay as history).

13. **Repo CLAUDE.md.** Update the top-level description ("Autonomous PR review agent — given a GitHub/Bitbucket PR URL...") to describe the security-review agent: consumes `security-review` tasks, checks out the target PR, runs `/coding:pr-review --security`, writes findings + binary verdict into the task's `## Review`, and never posts to GitHub. Update the architecture section, the key design decisions (remove/reframe the "posting is gated code" and "pr-override" bullets to the security agent's no-posting contract), and the releasing-doc link.

14. **CHANGELOG.** Add `## Unreleased` immediately below the preamble block (above `## v0.5.2`) with:
    `- feat: adapt the pr-reviewer seed into the security-review agent — renamed module, agent identity, service name, and image to the security-review identity; docs updated to the security variant`
    Leave the historical `## vX.Y.Z` entries as-is (they document the pr-reviewer releases and stay factual). Do not renumber or rename the existing sections.

15. **Explicitly out of scope (do NOT change in this prompt):**
    - `pkg/verdict.go` — must remain byte-identical to the seed (verdict contract is a later prompt's lock; the module sweep does not touch it because it has no module-path references — verify after the sweep).
    - `cmd/cli` behavior and usage strings (e.g. `usage: pr-reviewer ...`, `fmt.Printf("pr-reviewer %s\n", ...)`) — non-goal says the legacy tool must still compile under the renamed module but is not adapted.
    - `pkg/config.go` legacy env constants (`DefaultGitHubToken = "PR_REVIEWER_GITHUB_TOKEN"`, `DefaultBitbucketToken = "BITBUCKET_TOKEN"`) — legacy cmd/cli config, not the agent's live identity.
    - `.dark-factory.yaml` — workflow is `direct, pr: false`; do not touch.
    - `pkg/githubposter` bot-login defaults (`ben-s-pull-request-reviewer[bot]`) and the PEM filename in `cmd/mint-iat/main.go` doc comments (`bborbe-pr-reviewer.pem`) — legacy poster/config artifacts, unchanged.
    - `cmd/run-task/dummy-task.md` review-body sample text — historical fixture content, leave as-is.

16. **Regenerate mocks under the renamed module path.** After the import sweep, run `make generate` (`go generate -mod=mod ./...`) so counterfeiter regenerates all `mocks/*.go` files (they embed the module path). Confirm at least `mocks/http-client.go` and `pkg/factory/factory_mocks.go` no longer reference `github-pr-review-agent`.

17. **Self-check.** Before finishing: re-run `<verification>` and confirm every command passes; walk each acceptance criterion from the spec (AC1 identity, AC2 image+docs) against the actual files.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- The module name `github.com/bborbe/security-review-agent`, `agentName = "security-reviewer-agent"`, `serviceName = "maintainer-agent-security-reviewer"`, and image `security-review-agent` are frozen — the exact values above, no variation.
- `pkg/verdict.go` must remain byte-identical — do not edit it, and verify the module sweep did not touch it.
- Do NOT adapt or remove `cmd/cli` — it must still compile under the renamed module, but its behavior and usage strings are unchanged.
- Do NOT touch `.dark-factory.yaml` (workflow direct, pr: false).
- Do NOT touch `pkg/factory/factory.go` function bodies in this prompt — dispatch wiring lands in a later prompt (only the `serviceName` const and package doc change here).
- No opt-in flag re-enables GitHub posting (spec Non-goal) — this prompt adds none.
- Historical CHANGELOG version entries are left as-is; only `## Unreleased` is added.
- Use `errors.Wrapf(ctx, err, "...")` from `github.com/bborbe/errors` — never `fmt.Errorf`, never `context.Background()` in `pkg/`.
- Test coverage: new/changed code paths need >= 80% statement coverage; existing untested code needs no retroactive coverage.
</constraints>

<verification>
- `grep -rn 'github.com/bborbe/github-pr-review-agent' go.mod main.go main_test.go main_internal_test.go cmd/ pkg/ mocks/` — returns 0 lines (negative, matches the spec's container-executable check; the root-level test files embed the module path in their `gexec.Build(...)` calls)
- `grep -n 'github.com/bborbe/security-review-agent' go.mod` — returns line >= 1
- `grep -n 'security-reviewer-agent' main.go` — returns line >= 1
- `grep -n 'maintainer-agent-security-reviewer' pkg/factory/factory.go` — returns line >= 1
- `grep -n 'SERVICE = security-review-agent' Makefile` — returns line >= 1
- `grep -n 'security-review-agent' docs/dod.md` — returns line >= 1
- `grep -n 'github-pr-review-agent' README.md` — returns 0 lines
- `grep -n 'security-review-agent' docs/architecture.md` — returns line >= 1
- `bash -c 'if [ -f docs/pr-post-back.md ]; then ! grep -q "security-review-agent" docs/pr-post-back.md; else exit 0; fi'` — exits 0
- `grep -c 'pr-reviewer' pkg/verdict.go` — returns 0 (verdict.go untouched; if `git` is available in the container, also run `git diff --quiet pkg/verdict.go` which must exit 0)
- `make generate` then `make precommit` — both must exit 0 (make precommit runs fmt, generate, test, lint, vet, vuln, license)
</verification>
