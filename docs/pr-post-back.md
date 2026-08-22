# PR Post-Back — How Reviews Are Delivered to GitHub

After Claude writes the `## Review` section, the execution phase posts the verdict back to the PR as a GitHub review. This document describes the posting contract, the vault-first invariant, diagnostics, and failure routing.

## Vault-First Invariant

`## Review` is written to the task file **before** any GitHub API call. The order in `checkoutExecutionStep.runClaude` is strict:

1. Claude writes the review body as `runResult.Result`.
2. `md.ReplaceSection(Section{Heading: "## Review", Body: runResult.Result})` — review is in vault.
3. `postAndRoute(...)` — GitHub API call happens only after the vault is updated.

This means the review is never lost: if the pod crashes between steps 2 and 3, the controller re-spawns and sees `## Review` already present — `ShouldRun` returns false and the step is skipped (idempotent). If the pod crashes after step 3, the diagnostic block records the outcome.

## What Gets Posted

The poster receives:
- **Verdict** (`approve` / `request-changes`) — extracted from the JSON block at the end of `## Review` by `ParseVerdict`.
- **Summary** — the `## Review` body with the JSON verdict block stripped, via `StripJSONVerdict`. This is the human-readable part GitHub shows on the review.
- **HeadSHA** — the `ref` frontmatter field, used to anchor the review to the exact commit.
- **WorkDir** — the worktree path, used by the poster to read `.maintainer.yaml` for `prReviewer.autoApprove` config.

### GitHub body length limit (65,536 chars)

GitHub's API rejects PR review bodies, issue comments, and PR descriptions longer than **65,536 characters** with HTTP 422 "Body is too long" (per [REST API docs](https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request)). The poster truncates over-length bodies just below the limit and appends a one-line notice pointing to the full content in the vault `## Review` section. The truncation event is recorded as a `soft-warning` in the diagnostics block so the operator can audit without crashing the agent. In practice, agent reviews run 2-8 KiB; the truncation is a defensive guardrail, not a routine code path.

## The Posting Flow

```
postAndRoute
  │
  ├─ nil poster? → skip (advance to ai_review, no diagnostic written)
  │
  ├─ extract verdict + summary from ## Review body
  ├─ parse PR URL from task preamble (captured before any md mutations)
  ├─ check platform (non-GitHub → skip posting, advance to ai_review, write diagnostic)
  │
  ├─ PrPoster.Post(ctx, PostRequest{...})
  │     │
  │     ├─ bot identity self-check (GET /user == BOT_GITHUB_LOGIN)
  │     ├─ autoApprove config read (.maintainer.yaml in worktree)
  │     ├─ POST /repos/{owner}/{repo}/pulls/{n}/reviews
  │     └─ verify-after-POST (GET /pulls/{n}/reviews to confirm review appears)
  │
  ├─ appendDiagnosticsSection(md, buildDiagnosticBlock(...))
  │     → always written, success or failure, append-only
  │
  └─ route:
        outcome=success OR class=not-a-failure → advance to ai_review
        anything else                           → advance to human_review
```

Note: The planning phase has a separate LGTM posting path via `PrPoster.PostLGTM` for the empty-concerns route. This path bypasses the worktree checkout and posts directly from the planning phase. Failure routes to `human_review` in the same way as the execution posting path.

## Diagnostic Block Format

One block is appended to `## Diagnostics` per Job run (append-only; history is preserved across re-triggers).

**Success** (compact one-liner):
```
job_run: 2026-05-15T12:00:00Z outcome: success review_id: 12345
```

**Failure** (fenced YAML block):
```yaml
job_run: 2026-05-15T12:00:00Z
trigger_count: 2
outcome: failed
failure_step: post
class: transient
escalate_hint: false
attempt: 2
http_status: 500
error_message: "internal server error"
response_body: "<html>..."
elapsed_ms: 342
```

`failure_step` names the step where the error occurred (`pr_url_extraction`, `pr_url_parse`, `pr_url_platform`, `bot_identity`, `post`, `verify`). `class` is one of `transient`, `permanent`, `unknown`, `not-a-failure`. `escalate_hint` is true when the poster's retry logic recommends human escalation.

## Failure Routing

| Posting outcome | Class | Next phase |
|---|---|---|
| `success` on LGTM POST | any | `done` |
| `not-a-failure` on LGTM POST | `not-a-failure` | `done` |
| failure on LGTM POST | `transient` / `permanent` / `unknown` | `human_review` |
| `success` | any | `ai_review` |
| any | `not-a-failure` | `ai_review` |
| any | `transient` / `permanent` / `unknown` | `human_review` |

The `not-a-failure` class covers expected non-error states: 422 Unprocessable Entity (PR already closed or merged), duplicate review (already reviewed at this SHA). These are not errors — the review is simply no longer relevant.

`human_review` is a terminal state that routes the task to a human operator. The full diagnostic block in `## Diagnostics` gives the operator everything needed to diagnose and re-trigger if appropriate.

## Always-Post Review Invariant

After spec 034, every PR that reaches the planning phase produces at least one visible artifact on the GitHub PR — there is no silent-skip path.

**Empty-concerns path (LGTM):** When the planning phase's `## Plan` JSON has `concerns: []` (no concerns flagged), the agent POSTs a `COMMENT` review with body `Reviewed by <BotLogin> — no concerns flagged.` via `PrPoster.PostLGTM`. The `## Verdict` section is written to the vault after the POST succeeds, naming the review id and `COMMENT` event. The task advances to `phase: done`.

**Non-empty-concerns path:** When concerns are non-empty, the existing planning → execution → ai_review flow proceeds unchanged. `## Verdict` is written by the `ai_review` phase after the full review is posted.

**Failure routing:** If the LGTM POST fails (network error, GitHub 5xx/4xx), the task escalates to `human_review`. The `## Diagnostics` block records the failure. This is the same failure routing as the execution-phase posting path.

**BotLogin:** The LGTM body interpolates `BotLogin` (the `BOT_GITHUB_LOGIN` env value resolved by the factory) at runtime. No hardcoded bot login literals in agent code or templates.

**Vault `## Verdict` section (LGTM path):**
```
review_id: 12345
event: COMMENT
```

**Vault `## Verdict` section (full review path — written by ai_review):**
```
review_id: 67890
event: APPROVE  # or REQUEST_CHANGES
verdict: pass
reason: <meta-verdict reason>
```

**Non-GitHub platforms:** If the PR URL resolves to a non-GitHub platform, the LGTM path skips posting and returns `done` immediately. No `human_review` escalation for platform mismatches.

**nil poster:** When `prPoster` is nil, the LGTM path skips posting and returns `done` without writing `## Verdict` or `## Diagnostics`. (Note: `PostLGTM` itself is deprecated and has no non-test callers.)

## nil Poster — Local / Backward-Compatible Mode

`prPoster` is `nil` when `--skip-post` is set on `cmd/run-task`. A nil poster skips the entire posting flow and advances directly to `ai_review` without writing any diagnostic. This preserves backward compatibility with the local CLI mode.

## Dismissal Contract

`dismissPriorReviews` removes bot reviews that were left at **superseded** (older) commit SHAs as a PR accumulates new commits. It never removes a review whose `commit_id` equals the PR's current head SHA.

**Invariant:** a bot review at the current head SHA is always preserved by the dismissal step. The verifier (`verifier.go`) looks for a review at the current head SHA to confirm the POST succeeded — the dismissal step must not remove that artifact.

**SHA filter rule** (source: `pkg/githubposter/poster.go` `listBotReviews`, spec 031):

- Review `commit_id == current head SHA` → **never dismissed** (preserves the just-posted review)
- Review `commit_id != current head SHA` → eligible for dismissal, subject to:
  - bot identity filter: `user.login == BOT_GITHUB_LOGIN`
  - state filter: `COMMENTED` reviews are never dismissed — the GitHub API rejects their dismissal with HTTP 422
  - state filter: `DISMISSED` reviews are skipped in the caller loop (already inactive)

**Re-spawn safety:** if a controller re-spawns a pod on the same head SHA, the second pod's dismissal step returns an empty list (the first pod's review is at the current head SHA, which is preserved). The second pod short-circuits on vault idempotency and the PR ends with the first pod's review intact. This is the intended behavior; the original bug (`commit_id == headSHA` instead of `!=`) caused the second pod to wipe the first pod's review, leaving the PR with zero reviews despite a successful agent run.

## Key Files

| File | Purpose |
|---|---|
| `pkg/poster_types.go` | `PrPoster` interface, `PostRequest`, `PostResult`, `ErrorClass` |
| `pkg/githubposter/poster.go` | Concrete HTTP implementation of `PrPoster` |
| `pkg/githubposter/verifier.go` | verify-after-POST logic |
| `pkg/githubposter/retry.go` | One-retry transient error policy |
| `pkg/steps_checkout_execution.go` | `postAndRoute`, `buildDiagnosticBlock`, `appendDiagnosticsSection` |
| `pkg/factory/factory.go` | `CreatePrPoster` constructor wired into `CreateAgent` |
