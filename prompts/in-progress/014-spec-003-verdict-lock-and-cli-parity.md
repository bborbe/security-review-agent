---
status: approved
spec: [003-security-review-agent-adaptation]
created: "2026-08-24T20:22:59Z"
queued: "2026-08-24T20:44:38Z"
branch: dark-factory/security-review-agent-adaptation
---

<summary>
- The verdict contract (`pkg/verdict.go`) is locked byte-identical and its fail-closed parsing is proven for the security flow: a security `## Review` body with a Must Fix finding parses to `request-changes`, and an empty body fail-closes to `request-changes`.
- The local CLI entry (`cmd/run-task`) now dispatches on the task file's `task_type` through the security provider, so a local security-review run uses the identical no-posting security flow as the Kafka Job.
- A task file with no `task_type` defaults to the security route; a mis-routed `pr-review` task is refused by the local entry exactly as by the Kafka entry.
- An integration test in `cmd/run-task/` locks the task-type dispatch (security-review routed, missing type defaults, pr-review refused).
- Changelog gains the CLI-parity entry under `## Unreleased`.
- Reviewer note: the spec's AC9 (vault trigger skill) and AC10 (post-deploy pod check) are NOT covered by this prompt set. AC9 is a host-vault artifact (skill + script at `~/Documents/Obsidian/Personal/.claude/skills/security-review-trigger/`) that the YOLO container cannot reach — per the flow decision it is authored directly on the host by mirroring the `github-pr-review-trigger` skill and the `[[Create PR Review Agent Task]]` runbook, not by a dark-factory prompt. AC10 is operator-side verification after the separate deploy task lands and lives on the spec's operator-executable rung. Both are called out in the completion report.
</summary>

<objective>
Lock the inherited verdict contract for the security flow with regression tests, and wire the local CLI entry to run the identical security-agent flow (task-type dispatch, no poster) so local and Kafka runs behave the same.
</objective>

<context>
Read CLAUDE.md (repo root), then read:
- `pkg/verdict.go` — `ParseVerdict(reviewText string) Result`, `VerdictApprove`, `VerdictRequestChanges` (the contract being locked — do NOT modify this file).
- `pkg/verdict_test.go` — the existing `Parse` Describe and its `Context("empty review text", ...)` rows, to extend with a security context.
- `cmd/run-task/main.go` — `application.Run`, the `RunConfig` literal, `resolveAuth`, `resolveCachePaths`, and the `Agent` field (currently left nil so `factory.RunAgent` builds `CreateAgent`).
- `main.go` — `dispatchAgent` (the env-building + `factory.CreateAgentProvider` + `provider.Get` pattern to mirror in cmd/run-task).
- `pkg/factory/runner.go` — `RunConfig.Agent` handling (when set, `RunAgent` uses it instead of building `CreateAgent`).
- `cmd/run-task/main_test.go` — the existing `package main_test` Compiles test and the suite registration.
- `main_internal_test.go` (repo root) — the white-box `package main` test-file pattern (Describes registered under the external suite) to mirror for the cmd/run-task dispatch tests.
- `pkg/factory/factory_test.go` — the provider dispatch tests from the earlier prompt (the security route exists after that prompt lands).

Coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `go-testing-guide.md` (Ginkgo/Gomega, external test packages, white-box internal test files)
- `go-error-wrapping-guide.md` (bborbe/errors)
- `go-precommit.md` (funlen 80 limit — the `Run` method may already carry a `//nolint:funlen`; do not extend bodies beyond the cap without the nolint convention this repo uses)
- `changelog-guide.md`
</context>

<requirements>
1. **Lock the verdict contract — do NOT modify `pkg/verdict.go`.** It must remain byte-identical to the seed (AC6 negative evidence). Add a new `Context("security review ## Review body", ...)` inside the existing `Parse` Describe in `pkg/verdict_test.go` (external test package `pkg_test`):
   - `It("parses a Must Fix finding with the binary verdict JSON to request-changes")`: body = a security-findings body that contains a `#### Must Fix` finding AND ends in a fenced binary verdict block, e.g.:
     ```go
     body := "#### Must Fix\n- [pkg/auth.go:42] hardcoded credential in env fallback\n\n```json\n" +
     	`{"verdict":"request-changes","reason":"Must Fix finding"}` +
     	"\n```\n"
     result := pkg.ParseVerdict(body)
     Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
     ```
   - `It("fail-closes an empty ## Review body to request-changes")`: `result := pkg.ParseVerdict("")` → `Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))` and `Expect(result.Reason).To(Equal("empty review text"))`.
   - AC6's evidence is the row names present + `go test ./pkg/...` green; the existing verdict tests must remain green unchanged.

2. **Wire the local CLI to the security task-type dispatch** in `cmd/run-task/main.go`. Add a `dispatchAgent` method mirroring `main.go`'s `dispatchAgent` (same env building, same `factory.CreateAgentProvider` call shape):
   ```go
   // dispatchAgent resolves the agent for the task file's task_type via the
   // security dispatch table. A missing task_type defaults to the security-review
   // route (this binary is the security agent). A refused task_type (pr-review /
   // pr-override) returns the provider's unknown-task-type error — a mis-routed
   // task is never run as a plain review and never posted.
   func (a *application) dispatchAgent(
   	ctx context.Context,
   	taskContent string,
   	repoAllowlist []string,
   	resolvedToken string,
   	reposPath string,
   	workPath string,
   ) (*agentlib.Agent, error) {
   	md, err := agentlib.ParseMarkdown(ctx, taskContent)
   	if err != nil {
   		return nil, errors.Wrapf(ctx, err, "parse task content")
   	}
   	taskType := md.Frontmatter.TaskType()
   	if taskType == "" {
   		taskType = prpkg.TaskTypeSecurityReview
   	}
   	env := map[string]string{}
   	if resolvedToken != "" {
   		env["GH_TOKEN"] = resolvedToken
   	}
   	if a.AnthropicBaseURL != "" {
   		env["ANTHROPIC_BASE_URL"] = a.AnthropicBaseURL
   	}
   	if a.AnthropicAuthToken != "" {
   		env["ANTHROPIC_AUTH_TOKEN"] = a.AnthropicAuthToken
   	}
   	if a.AnthropicModel != "" {
   		env["ANTHROPIC_MODEL"] = a.AnthropicModel.String()
   	}
   	if a.AnthropicDefaultOpusModel != "" {
   		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = a.AnthropicDefaultOpusModel
   	}
   	if a.AnthropicDefaultSonnetModel != "" {
   		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = a.AnthropicDefaultSonnetModel
   	}
   	if a.AnthropicDefaultHaikuModel != "" {
   		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = a.AnthropicDefaultHaikuModel
   	}
   	if a.AnthropicDefaultFableModel != "" {
   		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = a.AnthropicDefaultFableModel
   	}
   	if a.BotLogin != "" {
   		env["BOT_GITHUB_LOGIN"] = a.BotLogin
   	}
   	repoManager := git.NewRepoManager(git.WorkdirConfig{ReposPath: reposPath, WorkPath: workPath}, resolvedToken)
   	provider := factory.CreateAgentProvider(
   		a.ClaudeConfigDir,
   		a.AgentDir,
   		a.AnthropicModel,
   		resolvedToken,
   		env,
   		repoManager,
   		a.ReviewMode,
   		repoAllowlist,
   		libtime.NewCurrentDateTime(),
   		a.MaxReviewDuration,
   	)
   	return provider.Get(ctx, taskType)
   }
   ```
   In `application.Run`, after `resolveAuth` / `resolveCachePaths`, call `agent, err := a.dispatchAgent(ctx, string(taskContent), repoAllowlist, resolvedToken, reposPath, workPath)`; on error return `errors.Wrap(ctx, err, "task type dispatch")` (fail loudly — a refused `pr-review` task must not run as a plain review). Set `Agent: agent` in the `RunConfig` literal (replacing the current leave-nil behavior). Keep the existing `SkipPost: a.SkipPost` field as-is (the security agent never posts regardless; `--skip-post` remains a harmless pre-existing flag).

3. **Add the cmd/run-task dispatch integration test.** Create `cmd/run-task/main_internal_test.go` (`package main`, white-box — registers `var _ = Describe(...)` under the suite bootstrapped in `cmd/run-task/main_test.go`, the same pattern as the repo-root `main_internal_test.go`). It exercises the real dispatch path of the local entry (no claude run — `CreateAgentProvider` is pure composition):
   - `It("routes a security-review task to the security agent")`: `app := &application{}`; `app.dispatchAgent(context.Background(), "---\ntask_type: security-review\nassignee: security-reviewer-agent\n---\n# Security Review\n", nil, "token", "/tmp/repos", "/tmp/work")` → non-nil agent, no error. (AC8: local entry wires the security agent via the security task-type route; the no-poster property is the security provider's construction — the provider builds the agent with nil poster/verifier, locked by the earlier factory and no-posting tests and by `grep 'githubposter' pkg/factory/factory.go` returning 0.)
   - `It("defaults a task without task_type to the security agent")`: a fixture with only `clone_url` frontmatter → non-nil agent, no error.
   - `It("refuses a pr-review task (never run as a plain review)")`: a fixture with `task_type: pr-review` → error occurs.
   - `It("refuses a pr-override task")`: a fixture with `task_type: pr-override` → error occurs.
   - Use `context.Background()` and `Expect` from Gomega as in the sibling test files.

4. **Changelog.** Append to `## Unreleased`:
    `- feat: wire cmd/run-task to the security task-type dispatch so the local CLI runs the identical no-posting security flow as the Kafka entry`

5. **Self-check.** Before finishing: re-run `<verification>` and confirm every command passes; walk the spec's AC6 (verdict contract preserved) and AC8 (local CLI parity) acceptance criteria against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- `pkg/verdict.go` is untouched — do NOT edit it; the only verdict work is adding tests in `pkg/verdict_test.go`.
- The local CLI must run the identical security flow: same security identity, same `--security` invocation (from the earlier prompt's execution-instructions threading), same no-posting guarantee. A mis-routed `pr-review` / `pr-override` task is refused, never run as a plain review.
- Do NOT add any opt-in flag that re-enables GitHub posting on this agent (spec Non-goal). `--skip-post` is pre-existing and unchanged.
- The `.dark-factory.yaml` workflow (direct, pr: false) is untouched.
- `cmd/cli` (legacy) is untouched — non-goal.
- Use `github.com/bborbe/errors` (`errors.Wrap`, `errors.Wrapf`) — never `fmt.Errorf` for error creation.
- Test coverage: new code >= 80% statement coverage; all changed/added code paths tested.
</constraints>

<verification>
- `go test ./pkg/... ./cmd/...` — exits 0; the verdict-contract rows (Must Fix → request-changes, empty body fail-closes) and the run-task dispatch rows (security-review routed, missing-type default, pr-review/pr-override refused) are present (AC6, AC8)
- `grep -n 'pr-reviewer\|github-pr-review-agent' pkg/verdict.go` — returns 0 lines (verdict.go untouched by the adaptation — this grep IS the AC6 negative evidence; `git diff --quiet pkg/verdict.go` may be skipped if the container reports `not a git repository` because the worktree `.git` pointer resolves outside the mount)
- `grep -n 'Agent:' cmd/run-task/main.go` — returns line >= 1 (the RunConfig wires the dispatched agent)
- `make precommit` — exits 0
</verification>
