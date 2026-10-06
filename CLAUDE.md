# CLAUDE.md

Autonomous **security review agent** — it consumes `security-review` tasks, checks out the target PR at its head, runs the Claude Code security review (`/coding:pr-review --security`) inside a `claude-yolo` container, and writes the findings plus the binary verdict (`approve` / `request-changes`) into the task's `## Review` section. It **never posts to GitHub** — the run ends at the vault write-back.

## Dark Factory Workflow

The headline reason to use prompts/specs: **safe unattended execution** inside a YOLO Claude container, sandboxed from the host. Queue work, step away, come back to commits — no permission interruptions.

### Choosing a Flow

**Canonical guide: `~/Documents/workspaces/dark-factory/docs/choosing-a-flow.md`** — read it, don't second-guess from memory. 30-second decision:

1. Is this code that runs in build / production / CI? No → **Direct** (edit by hand, no dark-factory). Markdown, config, yaml land here.
2. Yes — does the change carry a business-level "why" worth a permanent in-repo document? No → **Prompt**. Yes → **Spec → prompts**.

### Complete Flow

**Spec-based (multi-prompt features):**

1. Create spec → `/dark-factory:create-spec`
2. Audit spec → `/dark-factory:audit-spec`
3. Approve the spec → `dark-factory spec approve <name>`
4. dark-factory auto-generates prompts from spec (`autoGeneratePrompts: true`)
5. Audit prompts → `/dark-factory:audit-prompt`
6. Approve the prompt → `dark-factory prompt approve <name>`
7. Start daemon → `dark-factory daemon` (use Bash `run_in_background: true`)

**Standalone prompts (simple changes):**

1. Create prompt → `/dark-factory:create-prompt`
2. Audit prompt → `/dark-factory:audit-prompt`
3. Approve the prompt → `dark-factory prompt approve <name>`
4. Start daemon → `dark-factory daemon` (use Bash `run_in_background: true`)

### Claude Code Commands

| Command | Purpose |
|---------|---------|
| `/dark-factory:create-spec` | Create a spec file interactively |
| `/dark-factory:create-prompt` | Create a prompt file from spec or task description |
| `/dark-factory:audit-spec` | Audit spec against preflight checklist |
| `/dark-factory:audit-prompt` | Audit prompt against Definition of Done |
| `/dark-factory:verify-spec` | End-to-end verify a spec, then mark complete |

### CLI Commands

| Command | Purpose |
|---------|---------|
| `dark-factory spec approve <name>` | Approve spec (inbox → queue, triggers prompt generation) |
| `dark-factory prompt approve <name>` | Approve prompt (inbox → queue) |
| `dark-factory daemon` | Start daemon (watches queue, executes prompts) |
| `dark-factory run` | One-shot mode (process all queued, then exit) |
| `dark-factory status` | Combined status of prompts and specs |
| `dark-factory prompt cancel <name>` | Cancel a running/queued prompt (never `docker kill`) |

### Key rules

- Prompts go to **`prompts/`** (inbox) — never `prompts/in-progress/` or `prompts/completed/`
- Specs go to **`specs/`** (inbox) — never `specs/in-progress/` or `specs/completed/`
- Never number filenames — dark-factory assigns numbers on approve
- Never manually edit frontmatter status — use the CLI commands above
- Always audit before approving; always `/dark-factory:verify-spec <id>` before completing
- **Spec-linked prompts are daemon-generated** — after `spec approve`, wait for the `dark-factory-gen-<spec>` container; never hand-write prompts for an approved spec
- Run `dark-factory prompt approve` / `dark-factory spec approve` yourself once the prompt/spec passed its auditor (global rule execution-phase-no-reask)
- **Before starting the daemon** — run `dark-factory status` first; the daemon does not exit when the queue drains, so kill it once `Queue: 0`

## Development Standards

Follows the [coding-guidelines](https://github.com/bborbe/coding-guidelines). Go 1.26, vendored.

### Build and test

- `make precommit` — fmt, generate, test, lint, vet, vuln, license
- `make test` — tests only
- `VERSION=vX.Y.Z make buca` — build + push `docker.io/bborbe/security-review-agent:vX.Y.Z`, then apply

### Test conventions

- Ginkgo v2 / Gomega; Counterfeiter mocks (`mocks/`); external test packages (`*_test`)
- LLM steps are tested with a fake runner returning canned verdicts — no live Claude calls

## Architecture

Standalone binary; the shared lib is imported from `github.com/bborbe/maintainer` (Helm chart + deploy model live there). Tasks are produced by `github-pr-watcher` and dispatched by the agent-task-executor.

- `main.go` — Kubernetes Job entry (env-driven; `/main` in the image). Production path, Kafka/CQRS.
- `cmd/run-task/` — local CLI entry, reads a task file (`--task-file`), runs a given `--phase`, and writes the result back to the same file.
- `cmd/cli/` — legacy local tool (pr-reviewer lineage), takes a PR URL positional arg. `cmd/mint-iat/` — GitHub App installation-token smoke tool.
- `pkg/steps_planning.go` — parse PR URL → platform/owner/repo/number, fetch PR metadata.
- `pkg/steps_checkout_execution.go` — clone at PR head into scratch, run the `/coding:pr-review --security` container; writes findings + verdict to `## Review` (vault-first), never posts.
- `pkg/steps_review.go` — parse the JSON verdict from review output.
- `pkg/steps_gh_token.go` — mint the GitHub App installation token per run.
- `pkg/verdict.go` — verdict types + parsing.
- `pkg/allowlist.go` — `REPO_ALLOWLIST` matching (glob + `!` negation).
- `pkg/github/`, `pkg/bitbucket/` — platform REST clients (used read-only: metadata, clone).
- `pkg/githubauth/` — GitHub App auth. `pkg/git/` — clone ops. `pkg/prompts/` — embedded review prompt(s). `pkg/factory/` — pure-composition wiring.

## Key Design Decisions

- **No GitHub posting — the run ends at the vault write-back.** The Claude review returns a verdict only, written to the task's `## Review` section. There is no poster and no verifier — nothing is ever posted to a PR, and no new code path may add one.
- **`--security` is the review contract.** The execution phase runs the full security pipeline (`/coding:pr-review --security`); if the runtime-read plugin command file does not declare `--security`, the phase fails closed to `human_review` rather than running a non-security review.
- **Clones are throwaway scratch** at the PR head, cleaned up after the run — never written back.
- **`REPO_ALLOWLIST` is enforced** — a PR outside the allowlist is refused, never reviewed.
- **Escalation over guessing** — a step that cannot proceed returns `NeedsInput`/`human_review`, never auto-advances to `done`.
- **Factory functions are pure composition** — no conditionals, no I/O, no `context.Background()`.
- **Errors** use `github.com/bborbe/errors` with context wrapping; **logging** is `glog` with `V(n)`-gated `Info`.

## Releasing

This repo is `release.autoRelease: true` (`.maintainer.yaml`) — released by the sibling `github-releaser-agent` post-merge. Keep `## Unreleased` bullets accurate, merge to master, let the bot tag. Do NOT hand-rename `## Unreleased` or `git tag`. Full procedure + deploy: [docs/releasing-security-review-agent.md](docs/releasing-security-review-agent.md).
