# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased
- chore: bump Go toolchain directive to 1.27.3

## v0.7.6

- fix: bump `osv-scanner` to v2.6.0 and `golang.org/x/net` to v0.60.0 so the Linux vulnerability gates stop failing. v2.3.1 pins `golang.org/x/tools` v0.38.0, whose SSA builder aborts with `unexpected expr: *ast.KeyValueExpr` on the promoted-field composite-literal key Go 1.27 permits in the Linux stdlib, so a repo on the old pin passes locally on darwin and fails only in Linux CI. `x/net` v0.58.0 carries `GO-2026-6603/6610/6611/6612/6617`, which fail both `vulncheck` and `trivy`.
- chore: disposition the one gosec finding `golangci-lint` v2.14.0 newly raises. v2.14.0's stricter G703 taint analysis no longer accepts `filepath.Clean` as a sanitizer at `cmd/mint-iat/main.go:132`, so the operator-supplied PEM path is dispositioned there with a scoped `//nolint:gosec` and a rationale, leaving the rule enabled repo-wide.

## v0.7.5

- docs: Remove the dark-factory approval gate from this repo's `CLAUDE.md`. It contradicted the global `execution-phase-no-reask` carve-out and would have stopped every spec and prompt approval.
- docs: Name the daemon and inline what the global rule grants in the `CLAUDE.md` dark-factory bullet, so the replacement for the removed approval gate is resolvable from inside the repo.

## v0.7.4

- chore: bump `github.com/bborbe/agent` v0.87.1 → v0.89.4, which makes `Agent.unsupportedPhase` report `needs_input` instead of `failed` when a task's phase is not one the agent registers. The two statuses carry opposite retry semantics: `failed` means transient infra, so the deliverer preserved the assignee and the controller kept re-driving a task this lane could never run; `needs_input` means task-wrong, so the assignee is cleared and the task surfaces in the operator inbox. Nothing else bounded the retry for a task carrying neither `ref` nor `max_triggers`, so a rejected phase re-drove every ~60s indefinitely — the build-fix lane rejected `planning` for hours on 2026-09-24 (132 jobs) until its Config stopped dispatching that phase. The message string is unchanged, so log greps and dashboards keyed on `unsupported entry phase` keep working. Transitive bumps come with it: `errors` 1.6.1, `kafka` 1.25.16, `vault-cli` 0.126.3, `parse` 1.11.4.

## v0.7.3

- fix: record the pre-clear owner as `previous_assignee` when the build-fix step unassigns a task — the step clears `assignee` itself, and the result deliverer stamps that field only on its `needs_input` and cap-exhaustion paths, so a `done` build-fix escalation left no record of which agent parked the task for a human

## v0.7.2

- fix: resolve GitHub App credentials at startup only for task types that act on a repository (`security-review`) — `build-fix` and `healthcheck` no longer abort with "GitHub App credentials not configured" before their first step; an unset or unknown task type keeps the stricter branch and still fails fast
- fix: resolve GitHub App credentials in the local task runner (`cmd/run-task`) only after the task file has been read and its `task_type` derived from the frontmatter — the runner has no task-type config field, so it previously resolved credentials before it could know whether the task needed them

## v0.7.1

- fix: read build-fix Cloud Build logs from the Cloud Logging API (`entries:list`) instead of the build's Google-managed GCS logs bucket — the bucket cannot be read by the agent's service account, so the previous fetch always failed. The build's terminal status is still read from the Cloud Build API first, and the log query carries a `timestamp>=` lower bound because the Logging API otherwise answers with an empty entry list alongside a valid `nextPageToken`, which is indistinguishable from an empty log.

## v0.7.0

- fix: bound each Cloud Build / Cloud Storage response body read at 50 MiB and fail with an error naming the limit when it is exceeded, instead of reading an unbounded GCS log object into memory before the excerpt is taken. The size of the log object is not bounded from this side, so the previous `io.ReadAll` was the one place a pathological log could exhaust the pod.
- fix: resolve the build-fix Cloud Build token from the `GCP_KEY_JSON` service-account key when it is set and fall back to Application Default Credentials only when it is unset — the pod no longer authenticates as the broad node service account. Accepting any credential type present in the env var (`google.CredentialsFromJSON` rather than a type-pinned variant) is a deliberate tradeoff, not an oversight.
- feat: add a build-fix task type that reads the failed Cloud Build log over the Cloud Build REST API, extracts the failure cause, and enriches the task in place with the analysis while unassigning it for a human (analysis only — no auto-fix)

## v0.6.3

- fix: `make build` refuses to stamp a version onto a tree that is not that version's tag (`check-version-tag`, escape hatch `ALLOW_UNTAGGED_BUILD=1`). `VERSION` defaults to the newest tag repo-wide, so an operator-run build from an untagged or older tree silently republishes under the newest tag. The guard compares `git describe --exact-match HEAD` against `$(VERSION)` and exits non-zero on mismatch.

## v0.6.2

- chore: update Go to 1.27.1 and github.com/bborbe/agent to v0.87.1, github.com/bborbe/kafka to v1.25.13, github.com/bborbe/maintainer to v0.50.6, github.com/bborbe/service to v1.10.13, github.com/bborbe/time to v1.27.14, github.com/bborbe/vault-cli to v0.123.0

## v0.6.1

- chore: update github.com/bborbe/agent to v0.87.0, github.com/bborbe/argument/v2 to v2.13.2, github.com/bborbe/cqrs to v0.6.10, github.com/bborbe/errors to v1.6.0, github.com/bborbe/kafka to v1.25.11, github.com/bborbe/maintainer to v0.50.5, github.com/bborbe/sentry to v1.10.1, github.com/bborbe/service to v1.10.11, github.com/bborbe/time to v1.27.12, github.com/bborbe/vault-cli to v0.121.3, github.com/onsi/gomega to v1.43.0

## v0.6.0

- feat: wire cmd/run-task to the security task-type dispatch so the local CLI runs the identical no-posting security flow as the Kafka entry
- feat: thread the --security flag through the execution instructions and fail closed to human_review when the coding plugin lacks it
- feat: adapt the pr-reviewer seed into the security-review agent — renamed module, agent identity, service name, and image to the security-review identity; docs updated to the security variant
- feat: route security-review tasks to a no-posting security agent (nil poster + nil verifier) and drop the pr-review / pr-override routes
- refactor: fail closed when RunAgent receives no pre-dispatched agent — remove the poster/verifier-wired fallback so GitHub-posting machinery can never be constructed by this agent

## v0.5.3

- fix: key the `HasUnverifiedConcerns` fail-close on the concern's own blocker tiering (MUST-tier "must verify" / "will never fire" / "blocking" language, or a bare unexamined admission) instead of a benign-phrase whitelist — a benign "not verified (could not be cross-checked — source not in this repository)" note no longer demotes a clean `approve` to `CHANGES_REQUESTED` (port of github-pr-review-agent PR #24 / v0.6.3; false-request-changes recurrence 2026-08-24, bborbe/nuke#68)

## v0.5.2

- fix: narrow `HasUnverifiedConcerns` fail-close to genuinely-unexamined MUST-tier concerns — a benign "not verified (code logic not applicable — config-only diff)" concern no longer demotes approve → request-changes (port of github-pr-review-agent PR #23)

## v0.5.1

- chore: update Go to 1.27.0 and github.com/bborbe/agent to v0.82.1, github.com/bborbe/argument/v2 to v2.12.36, github.com/bborbe/cqrs to v0.6.8, github.com/bborbe/errors to v1.5.20, github.com/bborbe/kafka to v1.25.9, github.com/bborbe/maintainer to v0.50.0, github.com/bborbe/sentry to v1.9.26, github.com/bborbe/service to v1.10.9, github.com/bborbe/time to v1.27.10, github.com/bborbe/vault-cli to v0.114.6

## v0.5.0

- feat: persist a budget-terminated run's streamed partial review into a `## Salvage` task section (marked incomplete, distinct from `## Review`, never posted) so every budget overrun ends in a human-reviewable partial
- feat: add the time-budget wrap-up contract to the execution prompt (stop at the budget, disposition every `## Plan` concern, flag unexamined ones as `not verified`) and fail-close an `approve` that carries any unverified concern to `request-changes`
- feat: enforce the `REVIEW_MAX_DURATION` soft time budget on every Claude phase run and route budget overruns to `human_review` with a budget-naming message — never a blank `needs_input`, never a retry, never a partial posted
- feat: add `REVIEW_MAX_DURATION` soft time budget env (default `25m`, floor `60s`) to both entry points, validated at startup, and thread it through `RunConfig`
- test: lock the `## Review`-present idempotency guard against a `## Salvage` section — a salvaged partial (a distinct, clearly-incomplete heading) can never advance to `ai_review` on a later trigger, and a stale salvage never blocks a completed review
- feat: bump `github.com/bborbe/agent` to the release that captures the streamed partial review text when a claude run is terminated, and add `pkg.ExtractBudgetPartial` so budget-terminated runs can salvage what the model wrote

## v0.4.6

- chore: Fix `make precommit` on the Go 1.27 toolchain — run `gofmt -w` last in the `format` target (after golines) so its wrapping is normalized before the gofmt lint check, and bump `GOLANGCI_LINT_VERSION` v2.12.2 → v2.13.1 (fixes staticcheck `buildir` panic on Go 1.27 AST) + `ERRCHECK_VERSION` v1.10.0 → v1.20.0 (fixes `package "context" without types`) in `tools.env`

## v0.4.5

- fix: require verification of funnel findings against the actual worktree file before reporting (execution-phase prompt no longer treats over-inclusive mechanical findings as pre-confirmed)

## v0.4.4

- chore: update dependencies

## v0.4.3

- fix: allow read-only `git -C <workdir>` forms (`diff`, `log`, `show`, `status`, `ls-files`, `fetch`, `worktree`, `branch`, `rev-parse`) in the execution-phase allowlist (`pkg/factory`) so reviews that target the worktree with `-C` no longer die on `permission_denied`, and steer the execution prompt (`pkg/prompts`) to run plain `git` from the worktree cwd

## v0.4.2

- chore: bump Go 1.26.5 → 1.26.6 and update all dependencies

## v0.4.1

- chore: rebuild to pick up `bborbe/coding` v0.42.3, which gates the two `go-licensing` MUST rules on real repo visibility. The plugin is baked at image build time and frozen for the life of the tag (see `.chart-maintainer` spec 054), so a plugin fix only reaches the reviewer through a new agent image — no code change here, the rebuild *is* the delivery mechanism. Without it the bot keeps emitting `go-licensing/license-file-required` on private repos: 69 of 73 non-archived `Seibert-Data` repos carry no LICENSE by design, and one PR had to be admin-merged to get past the false positive.

## v0.4.0

- fix: bump `github.com/klauspost/compress` v1.18.6 → v1.18.7 (GO-2026-5841, out-of-bounds read in `compress/s2`). `make vulncheck` failed on master, which blocked the dark-factory preflight baseline.
- feat: add `--skip-post` flag to `cmd/run-task` that suppresses all GitHub write calls, allowing safe local review runs against third-party PRs. Findings are still written to the task file. Also adds the required nil-poster guard in `pkg/steps_review.go`'s `tryDismissHallucinated` and corrects mis-attributed CLI documentation in `CLAUDE.md`, `README.md`, and `docs/pr-post-back.md`.

## v0.3.6

- fix: code-level fail-closed gate on the funnel-failed path. When the Go-side mechanical funnel could not run (`funnel.Ran == false`) and the review model still returns `approve`, `postAndRoute` now overrides the verdict to `request-changes` (reason `mechanical funnel did not run`, recognised by `isFailClosedReason`) instead of trusting the prompt-only "you MUST NOT approve" instruction — which leaned on the same weak model this fix exists to work around. Mirrors the existing unparseable-verdict fail-close. `funnel.Ran` is threaded through `runClaude` → `postAndRoute`; tests cover both directions (funnelRan=false approve→request-changes, funnelRan=true approve preserved).

## v0.3.5

- fix: run the ast-grep mechanical funnel in the agent (Go) and inject its findings into the execution prompt, instead of steering the review model to invoke the runner. The execution prompt prescribed `ast-grep-runner.sh … > /tmp/pr-review-findings.json` — but that `>` redirect (plus the model's own `; echo EXIT:$?` and `bash -c '…'` wrappers) makes a compound command the `Bash(runner.sh:*)` allowlist entry can never match, so every runner call was denied. A weak model (MiniMax-M2.7, quant dev+prod) then concluded "funnel unavailable" and silently did a judgment-only review — dropping the entire MUST-tier mechanical pass on every PR, invisibly. Reproduced on quant dev (go-skeleton#58): runner ground truth was 6 findings; the bot posted a judgment-only review sourced from code comments, not the runner. New `pkg.FunnelRunner` diff-scopes to the PR's changed files and execs the runner deterministically; `BuildExecutionInstructions` injects the authoritative JSON (model must consume, not re-run) or, on funnel failure, a fail-closed status that forbids a silent `approve`. The runner path is removed from `factory.executionTools` (the model no longer invokes it — smaller attack surface). Because the injected findings carry PR-author-controlled strings (matched text from the diff), the runner output is validated as well-formed JSON (fail-closed on garbage / a compromised runner) and any markdown code-fence sequence is neutralised before embedding, so a crafted PR cannot break out of the prompt's ```json block and inject directives. Fixes the bypass model-independently (M2.7 or M3).

## v0.3.4

- fix: extract the verdict JSON by ```json fence boundaries instead of byte-level brace matching. `findLastJSONVerdictBlock` walked braces without string-awareness, so a stray brace or unescaped quote inside a JSON string *value* — common when the review prose describes parser code — mis-extracted the block, failed `json.Unmarshal`, and fail-closed an `approve` verdict to `request-changes` → GitHub state `CHANGES_REQUESTED` → blocked auto-merge and forced admin overrides. Observed on bborbe/github-update-go-agent#5 (valid JSON, a lone `'}'` in a string fooled the brace walker into grabbing prose) and #3 (unescaped inner `"` made the fenced block invalid JSON). New `findFencedJSONVerdictBlock` extracts the last ```json fenced block containing a `verdict` field (fence-delimited → immune to braces/quotes in string values); the brace walk survives only as a fallback for bare, unfenced JSON. When a fenced block is still invalid JSON, `recoverFencedVerdict` reads the literal `verdict` field verbatim — it can surface only the value the model actually wrote, never invent one, so it cannot flip a genuine `request-changes` to `approve`. Regression fixtures are the real #3/#5/#6 review bodies captured from the GitHub API (`pkg/testdata/`). The counter-example #6 (Dockerfile review, no braces/quotes in string values) already worked and stays `approve`.

## v0.3.3

- fix: install `python3` and `jq` in the Dockerfile alpine stage so the ast-grep mechanical funnel (`scripts/ast-grep-runner.sh`, Step 4a of `/coding:pr-review`) can run. The image shipped `ast-grep` but neither `python3` (the runner shells `python3 -c` for millisecond timestamps at lines 82/254, unconditionally under `set -euo pipefail`) nor `jq` (used throughout for JSON assembly) — so the runner died at line 82 with `python3: command not found` (exit 127) before any scan, silently skipping the entire MUST-tier mechanical rule pass on every review while judgment-tier coverage still completed. Reproduced end-to-end in an alpine:3.23 container mirroring the image packages: current state → exit 127, zero findings; `+python3 +jq` → exit 0, 66 YAMLs run, real MUST findings, `errors: []`. Surfaced on octopus dev during v0.3.2 selector-allowlist verification (`Seibert-Data/test-dev#1`).

## v0.3.2

- fix: restore real reviews under selector mode. The `/coding:pr-review` default dispatcher switched to selector mode (in-session classify+adjudicate, zero sub-agent spawns) in coding v0.22.0; the execution-phase `--allowedTools` allowlist (`factory.executionTools`) still assumed the old per-owner-dispatch model (`Task` + git only), so the review could not `Read` files, run the ast-grep mechanical funnel, or shell `jq`/`git rev-parse` — and the non-interactive container stalled, posting "I need your approval to proceed" as the review with a false `CHANGES_REQUESTED`. Expand `executionTools` with `Read`/`Grep`/`Glob`, `Bash(git rev-parse:*)`, `Bash(command -v:*)`, `Bash(jq:*)`, and the ast-grep runner's literal container path — all read-only, no network tools, so the anti-injection boundary holds. The assembled execution header now steers the model to invoke the runner/guide by literal path instead of the plugin's `$RUNNER`/`$GUIDE` shell variable (which an allowlist entry cannot match).

## v0.3.1

- fix(security): bump `golang.org/x/text` v0.38.0 → v0.40.0 to clear CVE-2026-56852 (infinite loop on invalid input) and restore a green `make precommit` baseline.

## v0.3.0

- feat: add `pr-override` task type. A trusted-author PR carrying the `override-review` label (emitted by github-pr-watcher) routes to a new code-only, single-phase override agent that posts an `APPROVE` at the PR head SHA via the new `PrPoster.PostOverrideApprove` — a fresh bot APPROVE supersedes the bot's own false-positive `CHANGES_REQUESTED` for reviewDecision, so any write-access user can merge without admin. No Claude, no clone, no container. CI status checks still apply (the label clears only the review requirement). Registered as a second task type in `CreateAgentProvider` alongside `pr-review` + `healthcheck`; `PostOverrideApprove` posts unconditionally (no autoApprove gate, no WorkDir, no prior-review dismissal), mirroring `PostLGTM`.

## v0.2.0

- fix: remove the planning-phase "no concerns → LGTM" shortcut. Planning is now pure triage: every GitHub PR advances to the execution phase for a real (checkout + deep) review that posts an earned `APPROVE`/`REQUEST_CHANGES` verdict. Previously a shallow planning pass with empty concerns posted a `COMMENT` "no concerns flagged" review without ever running the real review — a rubber-stamp that also failed to satisfy a required-approving-review merge gate (a COMMENT is not an approval). Tasks with no GitHub PR URL now escalate to `human_review` (subsumes the old non-GitHub-platform terminal case) instead of posting an LGTM. Removed the now-dead `postLGTMAndDone`/`handleEmptyPRURL`/`isGitHubPRURL`/`hasAnyPRURL`/`writePlanningVerdict` planning helpers and simplified `NewPlanningStep` (no longer takes a poster/botLogin/clock). `PrPoster.PostLGTM` is retained but deprecated.

## v0.1.3

- fix: planning-step JSON parser now tolerates conversational prose around the `## Plan` block. `parsePlanningConcerns` previously only stripped ```json fences at the very start, so any model that narrates before the fence (DeepSeek/vLLM emits e.g. "Now I have the full picture…") produced `invalid character 'N'`, failed 3× retries, and marked the review task `failed` with no verdict posted. New `extractJSONObject` locates the JSON via the first ```json/``` fenced block, else the first `{`…last `}` span. Unblocks real-diff reviews on non-Anthropic endpoints (real Anthropic emits clean JSON, so this was latent on quant).

## v0.1.2

- fix: forward `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU,FABLE}_MODEL` (new `--anthropic-default-*-model` args / env) into the claude subprocess env, so spawned review sub-agents (which request opus/sonnet/haiku) resolve to the configured model instead of the default `claude-sonnet-*`. Needed against non-Anthropic endpoints (DeepSeek/vLLM) where the default aliases 404 — the top-level `--model` worked but sub-agents crashed. Empty = unset (no-op on Anthropic).
- chore: bump Go 1.26.4 → 1.26.5 (GO-2026-5856); ignore unmaintained-openpgp GO-2026-5932 in `VULNCHECK_IGNORE` + `.trivyignore`.

## v0.1.1

- refactor: import the shared library from its new root module path `github.com/bborbe/maintainer` (was `github.com/bborbe/maintainer/lib`) and bump to `@v0.45.0`. The maintainer repo flattened `lib/` to its root to match the `bborbe/agent` layout. No behavior change.

## v0.1.0

- Extracted from the `bborbe/maintainer` monorepo (`agent/pr-reviewer`) into a
  standalone publish-only repository. The shared code now comes from the versioned
  `github.com/bborbe/maintainer/lib` module instead of a local `replace`. Builds and
  publishes `docker.io/bborbe/github-pr-review-agent:<version>` via `make buca`.
