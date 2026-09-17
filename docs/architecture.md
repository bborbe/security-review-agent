# security-review-agent Architecture

How the security-review agent decides whether to approve or reject a pull request, and where each piece of that decision lives. The agent consumes `security-review` tasks, runs the coding security review (`/coding:pr-review --security`) against the target PR, writes findings + binary verdict into the task's `## Review` section, and never posts to GitHub.

## Three Phases

A single security review is split across three sequential phases. Each phase is a fresh Claude container with no memory of the prior one — context flows only through the task file body.

```
┌──────────┐     ┌───────────┐     ┌──────────┐
│ planning │ ──► │ execution │ ──► │ ai_review│
└──────────┘     └───────────┘     └──────────┘
   gh metadata    writes ## Review   sanity-checks
   focus areas    (vault-first)      execution output
                  (THE verdict)      (meta-verdict)
```

| Phase | Reads | Writes | Emits |
|---|---|---|---|
| **planning** | task body (PR URL) | `## Plan` section (focus areas, concerns) | `status: needs_input / failed / done` — no verdict |
| **execution** | task body + `## Plan` | `## Review` section (vault-first); `## Diagnostics` block (run outcome) | **the review verdict** (`approve` / `request-changes`); routes to `ai_review` — never posts to GitHub |
| **ai_review** | `## Plan` + `## Review` | `## Verdict` section | **a meta-verdict** (`pass` / `fail`) judging whether execution did a good job |

**Key distinction:** the execution-phase verdict is the actual review outcome — written to the vault `## Review`, never posted to the PR. The ai_review-phase verdict is a separate sanity check — does the executor's output have hallucinations, did it address the planning concerns, is its verdict consistent with its own comments? `pass` allows the executor's verdict through; `fail` escalates to `human_review`.

The phases live in `pkg/prompts/`:

- `planning.go` + `planning_workflow.md` + `planning_output-format.md`
- `execution.go` + `execution_output-format.md` (no separate workflow file — workflow is embedded in the Go string)
- `review.go` + `review_workflow.md` + `review_output-format.md`

## The Verdict Rubric

The mapping from review findings to verdict is the agent's behavioral contract. **Source of truth: `pkg/prompts/execution.go`** (the Go-embedded `verdictTranslationFooter` string the LLM reads). The JSON schema constraining the verdict value is in `pkg/prompts/execution_output-format.md`.

This document does not duplicate the rubric table — it would rot. Read the prompt directly. Any change to the rubric requires changing the prompt; any other documentation is derivative and links here.

## Verdict Parsing — The Heuristic Fallback

The execution phase emits the verdict as a JSON block at the end of its output. The deliverer must extract a structured verdict from free-form text the LLM produces, so two parsers run in sequence in `pkg/verdict.go`:

1. **JSON-line parser** (`tryParseJSONLine` → `parseJSONVerdict`) — scans the last 50 lines for a `{"verdict": "...", "reason": "..."}` block, validates the verdict value against the binary set (`approve`, `request-changes`). Any other value (including the old `comment`) is rejected and falls through to the heuristic.
2. **Heuristic fallback** (`ParseVerdict`) — if no valid JSON block is found, scan section headers (`## Must Fix`, `## Should Fix`) and apply the same rubric as the LLM prompt:
   - Must Fix with content → `request-changes`
   - Should Fix with content → `request-changes`
   - Must Fix or Should Fix present but empty/None, or only Nice to Have → `approve`
   - No recognizable sections → `request-changes` (fail-closed)

The fallback exists because LLMs sometimes drop the JSON block under load or wrap it in unexpected markup. **Fail-closed default**: empty or unparseable text returns `request-changes`, never `approve` — a flaky agent run cannot silently green-light a PR.

## The `--security` Invocation Contract

The execution phase assembles the inlined `/coding:pr-review` procedure with `--security` passed through the pre-filled arguments, so the full security pipeline runs: **recon → security model → classifier → adjudicator → verifier → fail-closed toolchain/dependencies pass**. The review is a security review — not the general coding review.

**Fail-closed on flag drift:** if the runtime-read plugin command file does not declare `--security`, the phase fails closed to `human_review` instead of running a non-security review. A security task must never silently degrade into a general review; the contract is enforced at the prompt/procedure level.

## ai_review's Consistency Check

The ai_review phase reads both `## Plan` and `## Review` and runs the consistency checks (`pkg/prompts/review_workflow.md`):

1. **Concerns addressed** — every concern from `## Plan` either has a corresponding comment in `## Review` or appears in `concerns_addressed` as explicitly non-issue.
2. **No hallucinations** — every comment in `## Review` cites a file + line that actually exists in `gh pr diff`.
3. **Verdict consistency** — does the executor's verdict match the severity of its comments? `approve` with critical comments = inconsistent; `request-changes` with only nits = inconsistent.

ai_review writes `## Verdict` from these checks. Because the agent never posts, there is **no GitHub post-verification** step and no hallucination dismissal (nothing was posted to dismiss). Its `pass` / `fail` is meta — a green light to trust the executor's verdict, not a verdict on the PR itself.

## No GitHub Posting

The security-review agent wires a **nil poster and a nil verifier**. The execution phase writes `## Review` to the vault (vault-first) and routes to `ai_review` — there is no `PrPoster.Post` call and no verify-after-POST step. The run ends at the `## Review` write-back; nothing is written to any GitHub PR. Clones are throwaway scratch dirs at the PR head, cleaned up after the run, and `REPO_ALLOWLIST` is still honored — a PR outside the allowlist is refused, never reviewed.

The pr-reviewer posting contract (vault-first invariant, diagnostic block format, failure routing, dismissal) is retained as historical documentation in [`docs/pr-post-back.md`](pr-post-back.md) — it describes the pr-reviewer lineage, not the security agent's live behavior.

## The `build-fix` Task Type

The binary dispatches on `task_type`, not on the phase name. Three routes exist:

| `task_type` | Agent | Shape |
|---|---|---|
| `security-review` | 3-phase security agent (planning → execution → ai_review) | Claude, clone, no posting |
| `healthcheck` | liveness agent | single Claude step |
| `build-fix` | single code-only step under `execution` | **no Claude, no clone** |

`build-fix` is the second half of the two-agent build-failure chain: the
google-cloud-build-watcher files the task (with `build_id`), this agent explains
why the build broke, and a human fixes it. `pr-review` and `pr-override` are
deliberately **not** registered — a task mis-routed here is refused by the
provider's unknown-task-type rejection.

The step (`pkg/steps_build_fix.go`) reads the build log in two hops
(`pkg/build_log_fetcher.go` — the Cloud Build API for the terminal status and the
build's start timestamp, then Cloud Logging `entries:list` for the log text;
plain `net/http` with a bearer token minted from the `GCP_KEY_JSON`
service-account key, falling back to Application Default Credentials when it is
unset, no Google API client and no `gcloud` subprocess, so it is unit-testable
against `httptest`), extracts the failure cause, writes
`## Failure Analysis`, clears the
`assignee`, and routes to `human_review`. It never advances to `done`, never
modifies code, and never files a second task.

`ExtractFailureCause` anchors on the **last** error-marked line: a build log
carries many earlier error-shaped lines (retried steps, a probe's `exit status 1`)
before the fatal one, so the first match reports the wrong cause. Excerpt lines
with a leading `#` are escaped with a space — `agentlib.ParseMarkdown` treats any
line beginning with `# ` or `## ` as a heading regardless of fences, and a Go/cgo
build log legitimately contains `# github.com/foo/bar` lines.

## Result Delivery

The task file mutates in place across phases. The k8s Job entry (`main.go`) reads `TASK_CONTENT`, runs the appropriate phase based on the `phase:` frontmatter field, mutates the task body, and publishes a status update to Kafka (`master-agent-task-v1-request`). The result-deliverer (`pkg/factory/`) handles the publish.

Phase advancement is driven by the controller (in the `bborbe/agent` repo, not here): on each phase completion the controller updates `phase:` in the task and respawns. Terminal states are `done`, `human_review`, `aborted`.

The Kafka contract is `{"status":"done|needs_input|failed","message":"..."}`. Other consumers depend on this shape — see [the maintainer architecture doc](../../../docs/architecture.md) for the full pipeline.

## File Map

```
agent/security-reviewer/
├── main.go                          k8s Job entry: read TASK_CONTENT → run phase → publish Kafka result
├── cmd/run-task/                    local CLI for the same flow against a task markdown file
├── cmd/cli/                         legacy single-shot CLI (pr-reviewer lineage): PR URL → worktree → Claude → comment
├── pkg/
│   ├── verdict.go                   JSON-line parser + heuristic fallback (the rubric, in code)
│   ├── verdict_test.go              Ginkgo/Gomega coverage of every parser branch
│   ├── prompts/
│   │   ├── execution.go             SOURCE OF TRUTH for the verdict rubric (Go string the LLM reads; security-rubric source of truth)
│   │   ├── execution_output-format.md   JSON schema constraining the verdict value
│   │   ├── planning_workflow.md     planning-phase instructions
│   │   ├── planning_output-format.md
│   │   ├── review_workflow.md       ai_review-phase instructions (the meta-verdict consistency check)
│   │   └── review_output-format.md
│   └── factory/                     TaskRunner + Kafka deliverer
├── agent/.claude/CLAUDE.md          headless guardrails (no internet, no installs, no shell escapes)
└── k8s/                             Config CRD + secret + PVC + priority + quota
```

## Why this split exists

- **planning** isolates the "what should we look at" decision from the "what do we say about it" decision. Cheap, fast, no file reads beyond `gh`.
- **execution** is the single bottleneck where the verdict is decided. Concentrating the rubric in one phase's prompt + one Go parser file (`pkg/verdict.go`) means there's exactly one place to change behavior.
- **ai_review** exists because LLMs hallucinate, and an LLM reviewing its own output catches roughly half of the cases where the executor confidently cites a non-existent line. Cheap insurance against silently bad reviews.
