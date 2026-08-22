# pr-reviewer Architecture

How this agent decides whether to approve or reject a pull request, and where each piece of that decision lives.

## Three Phases

A single PR review is split across three sequential phases. Each phase is a fresh Claude container with no memory of the prior one — context flows only through the task file body.

```
┌──────────┐     ┌───────────┐     ┌──────────┐
│ planning │ ──► │ execution │ ──► │ ai_review│
└──────────┘     └───────────┘     └──────────┘
   gh metadata    posts the review   sanity-checks
   focus areas    + verdict          execution output
                  (THE verdict)      (meta-verdict)
```

| Phase | Reads | Writes | Emits |
|---|---|---|---|
| **planning** | task body (PR URL) | `## Plan` section (focus areas, concerns) | `status: needs_input / failed / done` — no verdict |
| **execution** | task body + `## Plan` | `## Review` section (vault-first); `## Diagnostics` block (posting outcome); posts review to GitHub via `PrPoster` | **the review verdict** (`approve` / `request-changes`); routes to `ai_review` on success or `human_review` on posting failure |
| **ai_review** | `## Plan` + `## Review` | `## Verdict` section | **a meta-verdict** (`pass` / `fail`) judging whether execution did a good job |

**Key distinction:** the execution-phase verdict is the actual PR review outcome posted back to GitHub. The ai_review-phase verdict is a separate sanity check — does the executor's output have hallucinations, did it address the planning concerns, is its verdict consistent with its own comments? `pass` allows the executor's verdict through; `fail` escalates to `human_review`.

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

## ai_review's Consistency Check

The ai_review phase reads both `## Plan` and `## Review` and runs four checks (`pkg/prompts/review_workflow.md`):

1. **Concerns addressed** — every concern from `## Plan` either has a corresponding comment in `## Review` or appears in `concerns_addressed` as explicitly non-issue.
2. **No hallucinations** — every comment in `## Review` cites a file + line that actually exists in `gh pr diff`.
3. **Verdict consistency** — does the executor's verdict match the severity of its comments? `approve` with critical comments = inconsistent; `request-changes` with only nits = inconsistent.
4. **Post verification** — after the LLM writes `## Verdict`, the step calls `ReviewVerifier.VerifyReview` (GET `/pulls/{n}/reviews`) to confirm the execution-phase review actually persisted on GitHub. If absent, the step returns `AgentStatusFailed` and appends a diagnostic line (`ai_review verify: ...`) to `## Diagnostics`. Verification is skipped when `## Review` is absent (no post was attempted) or when the last `## Diagnostics` YAML block contains `class: permanent` or `class: unknown` (retry would not help). A `nil` verifier disables the check for local/test runs.

ai_review's purpose is catching the case where execution rubber-stamped its own reasoning. Its `pass` / `fail` is meta — a green light to trust the executor's verdict, not a verdict on the PR itself.

## Posting Reviews Back to GitHub

After Claude writes `## Review`, the execution phase calls `PrPoster.Post` to submit the verdict as a GitHub review. The full posting contract — vault-first invariant, diagnostic block format, failure routing, and `nil`-poster backward-compatibility mode — is documented in [`docs/pr-post-back.md`](pr-post-back.md).

## Result Delivery

The task file mutates in place across phases. The k8s Job entry (`main.go`) reads `TASK_CONTENT`, runs the appropriate phase based on the `phase:` frontmatter field, mutates the task body, and publishes a status update to Kafka (`master-agent-task-v1-request`). The result-deliverer (`pkg/factory/`) handles the publish.

Phase advancement is driven by the controller (in the `bborbe/agent` repo, not here): on each phase completion the controller updates `phase:` in the task and respawns. Terminal states are `done`, `human_review`, `aborted`.

The Kafka contract is `{"status":"done|needs_input|failed","message":"..."}`. Other consumers depend on this shape — see [the maintainer architecture doc](../../../docs/architecture.md) for the full pipeline.

## File Map

```
agent/pr-reviewer/
├── main.go                          k8s Job entry: read TASK_CONTENT → run phase → publish Kafka result
├── cmd/run-task/                    local CLI for the same flow against a task markdown file
├── cmd/cli/                         legacy single-shot CLI: PR URL → worktree → Claude → posts comment
├── pkg/
│   ├── verdict.go                   JSON-line parser + heuristic fallback (the rubric, in code)
│   ├── verdict_test.go              Ginkgo/Gomega coverage of every parser branch
│   ├── prompts/
│   │   ├── execution.go             SOURCE OF TRUTH for the verdict rubric (Go string the LLM reads)
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
