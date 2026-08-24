---
status: approved
spec: [003-security-review-agent-adaptation]
created: "2026-08-24T20:22:59Z"
queued: "2026-08-24T20:44:38Z"
branch: dark-factory/security-review-agent-adaptation
---

<summary>
- The execution-phase prompt assembly threads the `--security` flag through the pre-filled arguments, so the inlined `/coding:pr-review` command runs the full security pipeline.
- The assembled instructions string that reaches the runner now contains `--security` in its Pre-filled arguments block (not as a comment).
- The assembly fails closed: if the runtime-read plugin command file does not declare `--security` (plugin version drift below v0.49.0), assembly returns an error naming the missing flag.
- The execution step routes that specific failure to human_review (NeedsInput) with a message naming the missing flag and a pod log line — no `## Review` is written as a plain review, no fallback to a non-security review.
- Unit tests lock the threading (assembled instructions contain `--security` in the pre-filled args), the fail-closed assembly error, and the human_review routing.
</summary>

<objective>
Make the execution phase invoke the coding security review — not the plain review — by passing `--security` through the pre-filled arguments, and fail closed to human_review instead of silently running a non-security review when the plugin lacks the flag.
</objective>

<context>
Read CLAUDE.md (repo root), then read:
- `pkg/prompts/execution.go` — `BuildExecutionInstructions`, `prefilledArgsHeaderTemplate`, and the `os.ReadFile(pluginPath)` read of the `/coding:pr-review` plugin command file.
- `pkg/prompts/execution_test.go` — the `fakePlugin` constant and the test rows to update/extend.
- `pkg/steps_checkout_execution.go` — `checkoutExecutionStep.Run`'s call to `BuildExecutionInstructions` and its current `errors.Wrapf` on assembly failure.
- `pkg/git/auth_failure.go` — the `IsGitAuthFailure` substring-matcher precedent to mirror for the fail-closed predicate.

Coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/` (if present in the executor image; a failed read of these advisory docs must not stall the work):
- `go-error-wrapping-guide.md` (bborbe/errors: `errors.New(ctx, msg)`, `errors.Errorf(ctx, fmt, args...)`, `errors.Is(err, target)`, `errors.Wrapf`)
- `go-logging-guide.md` (glog `V(n)`-gated `Infof`)
- `go-testing-guide.md` (Ginkgo/Gomega)

Do NOT edit the coding plugin command file — the agent reads it at runtime and may only probe it for the `--security` contract. Do NOT touch `pkg/verdict.go`.
</context>

<requirements>
1. **Add `--security` to the pre-filled arguments header.** In `pkg/prompts/execution.go`, replace the `prefilledArgsHeaderTemplate` const with:
   ```go
   const prefilledArgsHeaderTemplate = "## Pre-filled arguments\n\n" +
   	"The procedure below describes a `/coding:pr-review` slash command that takes\n" +
   	"`<target-branch>`, a mode argument, and the `--security` security-review flag.\n" +
   	"Those arguments have already been resolved for this run — do NOT prompt for\n" +
   	"them, do NOT re-derive them:\n\n" +
   	"- **TARGET_BRANCH**: %s\n" +
   	"- **mode**: %s\n" +
   	"- **--security**: enabled — run the full security pipeline (recon → derived security model → classifier → adjudicator → verifier → fail-closed toolchain/dependencies pass)\n" +
   	"- **Working directory**: your cwd is already the cloned worktree at the PR head — " +
   	"run plain `git` from it, never `git -C <path>`; `git -C` is not needed.\n\n" +
   	"Now follow the procedure below as if the slash command had been invoked with\n" +
   	"those arguments. The procedure references sub-agents via the `Task` tool;\n" +
   	"dispatch them as written.\n\n" +
   	"---\n\n"
   ```
   The `BuildExecutionInstructions` signature is unchanged — `--security` is a constant boolean composed in Go (a static pre-filled argument), NOT derived from task content and NOT a new parameter.

2. **Probe the runtime-read command file and fail closed.** In `BuildExecutionInstructions`, immediately after the `os.ReadFile(pluginPath)` success (after the `#nosec` read, before assembling the header), add:
   ```go
   if !strings.Contains(string(raw), "--security") {
   	return nil, errors.Errorf(
   		ctx,
   		"coding plugin command file path=%s does not declare --security (required for security review); plugin version < v0.49.0",
   		pluginPath,
   	)
   }
   ```
   The message MUST name the missing flag (contains `--security`) — the spec requires the routing message and pod log to name it. `strings` is already imported in this file.

3. **Add the fail-closed predicate.** In `pkg/prompts/execution.go`, add (mirroring `pkg/git/auth_failure.go`'s `IsGitAuthFailure` substring matcher):
   ```go
   // missingSecurityFlagSubstring is the error-message fragment emitted when the
   // runtime-read /coding:pr-review command file does not declare --security.
   const missingSecurityFlagSubstring = "--security"

   // IsSecurityFlagMissing reports whether err is the fail-closed
   // missing-`--security` assembly error. Returns false for nil. Mirrors
   // git.IsGitAuthFailure's substring matcher (pkg/git/auth_failure.go).
   func IsSecurityFlagMissing(err error) bool {
   	if err == nil {
   		return false
   	}
   	return strings.Contains(err.Error(), missingSecurityFlagSubstring)
   }
   ```

4. **Route the missing-flag failure to human_review in the execution step.** In `pkg/steps_checkout_execution.go`, `checkoutExecutionStep.Run`, replace the assembly-error branch:
   ```go
   instructions, err := prompts.BuildExecutionInstructions(
   	ctx,
   	s.claudeConfigDir,
   	s.reviewMode,
   	baseRef,
   	funnel.Ran,
   	funnel.FindingsJSON,
   	funnel.FailDetail,
   	s.maxDuration,
   )
   if err != nil {
   	if prompts.IsSecurityFlagMissing(err) {
   		glog.V(2).Infof(
   			"execution: coding plugin lacks --security contract — failing closed to human_review: %v",
   			err,
   		)
   		return &agentlib.Result{
   			Status:  agentlib.AgentStatusNeedsInput,
   			Message: fmt.Sprintf("execution step: %v", err),
   		}, nil
   	}
   	return nil, errors.Wrapf(
   		ctx,
   		err,
   		"build execution instructions base_ref=%s mode=%s",
   		baseRef,
   		s.reviewMode,
   	)
   }
   ```
   The NeedsInput result routes to human_review with a message naming the missing flag, and the `V(2)` log line fires at the assembly error. Because `BuildExecutionInstructions` fails BEFORE `runClaude`, no `## Review` is ever written for this path (no plain review). Other assembly errors (empty baseRef, missing plugin file, read failure) keep the existing error-propagation behavior.

5. **Update and extend the prompts test** (`pkg/prompts/execution_test.go`):
   - Update EVERY plugin-file fixture in the two touched test files so its body declares `--security` (e.g. append `Security review flag: --security.`), since the probe rejects any body without it: the `fakePlugin` constant AND the `noFrontmatter` local var in `pkg/prompts/execution_test.go` (the "no frontmatter" row uses its own local, not `fakePlugin`), and the three plugin bodies at `pkg/steps_checkout_execution_test.go` (~lines 724, 797, 871) in the soft-budget and two salvage tests — those must keep `--security` so they still reach `runClaude` and exercise the soft-budget/salvage path they lock.
   - Add a row: assembled `workflow` content contains `--security` AND the occurrence is inside the Pre-filled arguments block — assert `strings.Index(workflow, "## Pre-filled arguments") < strings.Index(workflow, "--security") < strings.Index(workflow, "Procedure body line 1.")` (AC4: wired through the pre-filled arguments, not merely echoed in the injected plugin body).
   - Add a fail-closed row (AC4): write a plugin body WITHOUT `--security` → `BuildExecutionInstructions` returns an error whose message contains `"--security"`.
   - Add a row: `prompts.IsSecurityFlagMissing(err)` returns true for the missing-flag error and false for nil and for unrelated errors.

6. **Add the routing test** in `pkg/steps_checkout_execution_test.go` (external test package `pkg_test`):
   - New Context "missing --security flag": create a temp `claudeConfigDir` with `plugins/marketplaces/coding/commands/pr-review.md` whose content does NOT contain `--security`; construct the step via `pkg.NewCheckoutExecutionStep(repoManagerFake, claudeConfigDir, "agent", "sonnet", env, tools, "standard", nil, nil, nil, currentDateTime, fakeRunner, maxDuration)`; `repoManagerFake.EnsureWorktreeReturns(worktreePath, nil)`; run on a fixture task with `clone_url` / `ref` / `base_ref` / `task_identifier` + GitHub PR URL. Assert: no error; `result.Status == agentlib.AgentStatusNeedsInput`; `result.Message` contains `"--security"`; `fakeRunner.RunCallCount() == 0` (the runner is never invoked — no plain review); and the markdown contains NO `## Review` section.

7. **Changelog.** Append to `## Unreleased` (create `## Unreleased` below the preamble and above `## v0.5.2` if it is absent — the previous prompt should have created it):
    `- feat: thread the --security flag through the execution instructions and fail closed to human_review when the coding plugin lacks it`

8. **Self-check.** Before finishing: re-run `<verification>` and confirm every command passes; walk the spec's AC4 (execution invokes the security review) acceptance criterion against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- Do NOT edit the coding plugin command file — the agent reads it at runtime and may only probe it for the `--security` contract (fail-closed when absent). Do NOT fall back to a non-security review when the flag is missing.
- The `--security` argument is composed in Go from a constant boolean, not from task content — a malicious task body cannot inject additional CLI arguments or flags.
- Do NOT widen the execution-phase tool allowlist. The `--security` toolchain/dependencies pass runs via the Task-tool sub-agent dispatch the coding command specifies, never by adding binaries/network to the parent allowlist.
- The three-phase flow and phase names (`planning` / `execution` / `ai_review`) and the `## Plan` / `## Review` / `## Verdict` idempotency guards are unchanged; the vault-first `## Review` write before any external call is preserved.
- No opt-in flag re-enables GitHub posting on this agent (spec Non-goal).
- `pkg/verdict.go` is untouched in this prompt.
- Use `github.com/bborbe/errors` (`errors.New`, `errors.Errorf`, `errors.Wrapf`) — never `fmt.Errorf` for error creation; `errors.Is` is available if you need sentinel comparison.
- Test coverage: new code >= 80% statement coverage; all changed/added code paths tested.
</constraints>

<verification>
- `grep -n -- '--security' pkg/prompts/execution.go` — returns line >= 1 (AC4)
- `go test ./pkg/prompts/... ./pkg/...` — exits 0; the threading row, the fail-closed assembly row, the predicate rows, and the human_review routing row are present
- `make precommit` — exits 0
</verification>
