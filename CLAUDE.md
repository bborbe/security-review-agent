# CLAUDE.md

Autonomous PR review agent — given a GitHub/Bitbucket PR URL it clones the repo, runs a Claude Code review inside a `claude-yolo` container, and posts the verdict back (`approve` / `request-changes` / `comment`).

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
3. User confirms → `dark-factory spec approve <name>`
4. dark-factory auto-generates prompts from spec (`autoGeneratePrompts: true`)
5. Audit prompts → `/dark-factory:audit-prompt`
6. User confirms → `dark-factory prompt approve <name>`
7. Start daemon → `dark-factory daemon` (use Bash `run_in_background: true`)

**Standalone prompts (simple changes):**

1. Create prompt → `/dark-factory:create-prompt`
2. Audit prompt → `/dark-factory:audit-prompt`
3. User confirms → `dark-factory prompt approve <name>`
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
- **BLOCKING: never run `prompt approve`, `spec approve`, or `daemon` without explicit user confirmation.** Write the prompt/spec, then STOP and ask.
- **Before starting the daemon** — run `dark-factory status` first; the daemon does not exit when the queue drains, so kill it once `Queue: 0`

## Development Standards

Follows the [coding-guidelines](https://github.com/bborbe/coding-guidelines). Go 1.26, vendored.

### Build and test

- `make precommit` — fmt, generate, test, lint, vet, vuln, license
- `make test` — tests only
- `VERSION=vX.Y.Z make buca` — build + push `docker.io/bborbe/github-pr-review-agent:vX.Y.Z`, then apply

### Test conventions

- Ginkgo v2 / Gomega; Counterfeiter mocks (`mocks/`); external test packages (`*_test`)
- LLM steps are tested with a fake runner returning canned verdicts — no live Claude calls

## Architecture

Standalone binary; the shared lib is imported from `github.com/bborbe/maintainer` (Helm chart + deploy model live there). Tasks are produced by `github-pr-watcher` and dispatched by the agent-task-executor.

- `main.go` — Kubernetes Job entry (env-driven; `/main` in the image). Production path, Kafka/CQRS.
- `cmd/run-task/` — local CLI entry, reads a task file (`--task-file`), runs a given `--phase`, and writes the result back to the same file. Use `--skip-post` to suppress GitHub writes for local runs against third-party PRs.
- `cmd/cli/` — auxiliary local tool, takes a PR URL positional arg and optional `--comment-only` flag for local review runs. `cmd/mint-iat/` — GitHub App installation-token smoke tool.
- `pkg/steps_planning.go` — parse PR URL → platform/owner/repo/number, fetch PR metadata.
- `pkg/steps_checkout_execution.go` — clone at PR head into scratch, run the `/pr-review` container.
- `pkg/steps_review.go` — parse the JSON verdict from review output.
- `pkg/steps_override.go` — `pr-override` path: post an unconditional `APPROVE` for trusted-author label PRs.
- `pkg/steps_gh_token.go` — mint the GitHub App installation token per run.
- `pkg/verdict.go` — verdict types + parsing.
- `pkg/githubposter/` + `pkg/poster_types.go` — post the structured review (`APPROVE`/`REQUEST_CHANGES`/`COMMENT`).
- `pkg/allowlist.go` — `REPO_ALLOWLIST` matching (glob + `!` negation).
- `pkg/github/`, `pkg/bitbucket/` — platform REST clients.
- `pkg/githubauth/` — GitHub App auth. `pkg/git/` — clone ops. `pkg/prompts/` — embedded review prompt(s). `pkg/factory/` — pure-composition wiring.

## Key Design Decisions

- **LLM produces a verdict; posting is gated code.** The Claude review returns a verdict only. Whether it posts as `APPROVE`/`REQUEST_CHANGES` (vs demoted `COMMENT`) is decided by the target repo's `.maintainer.yaml` `prReviewer.autoApprove`, read from the PR head.
- **`pr-override` is the one unconditional-approve path** — trusted-author `override-review` label, `PostOverrideApprove`, no autoApprove gate, no clone, no container. Do not widen it.
- **Clones are throwaway scratch** at the PR head, cleaned up after the run — never written back.
- **`REPO_ALLOWLIST` is enforced** — a PR outside the allowlist is refused, never reviewed.
- **Escalation over guessing** — a step that cannot proceed returns `NeedsInput`/`human_review`, never auto-advances to `done`.
- **Factory functions are pure composition** — no conditionals, no I/O, no `context.Background()`.
- **Errors** use `github.com/bborbe/errors` with context wrapping; **logging** is `glog` with `V(n)`-gated `Info`.

## Releasing

This repo is `release.autoRelease: true` (`.maintainer.yaml`) — released by the sibling `github-releaser-agent` post-merge. Keep `## Unreleased` bullets accurate, merge to master, let the bot tag. Do NOT hand-rename `## Unreleased` or `git tag`. Full procedure + deploy: [docs/releasing-github-pr-review-agent.md](docs/releasing-github-pr-review-agent.md).
