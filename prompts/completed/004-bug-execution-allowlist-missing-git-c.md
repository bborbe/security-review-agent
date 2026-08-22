---
status: completed
spec: [001-bug-execution-allowlist-missing-git-c]
summary: Added 18 scoped read-only `git -C <workdir>` allowlist entries to the execution-phase tools, worktree-cwd guidance to the execution prompt, regression-lock tests, and a changelog entry; make precommit passes.
execution_id: github-pr-review-agent-fix-git-c-allowlist-exec-004-bug-execution-allowlist-missing-git-c
dark-factory-version: dev
created: "2026-08-18T12:53:32Z"
queued: "2026-08-18T13:16:38Z"
started: "2026-08-18T13:16:39Z"
completed: "2026-08-18T13:25:35Z"
branch: dark-factory/bug-execution-allowlist-missing-git-c
---

<summary>
- Execution-phase reviews no longer die the moment the review model targets the cloned worktree with `git -C <workdir>` — the command allowlist now accepts the read-only `-C` forms
- The read-only subcommands covered are the same nine already allowed without `-C`: diff, log, show, status, ls-files, fetch, worktree, branch, rev-parse — both with arguments and with no arguments
- Write and network subcommands stay denied in the `-C` form — push, clean, checkout, reset, merge cannot be reached through the new entries
- A bare catch-all `-C` rule is deliberately not added, so no permission scope beyond read-only git appears
- The execution prompt now tells the model its working directory is the cloned worktree and to run plain git from it instead of using `-C`
- Unit tests lock the behavior in: read-only `-C` commands are accepted, the bare `git diff` form still works, write commands are rejected, and removing the new entries flips the read-only `-C` rows back to rejected
- No other allowlist entries, the workdir scheme, or the heartbeat watchdog are touched
</summary>

<objective>
Execution-phase PR reviews complete even when the review model targets the cloned worktree with `git -C <workdir>`: the execution allowlist permits the read-only `-C` git forms, the execution prompt steers the model back to plain `git` run from the worktree cwd, and unit tests lock the new boundary in place. The write/network boundary is unchanged and no other allowlist entries are reworked.
</objective>

<context>
Read CLAUDE.md for project conventions. Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega test conventions, coverage rules
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` wrapping, never `fmt.Errorf`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and bullet style

Read these files fully before making changes:
- `pkg/factory/factory.go` — the `executionTools` var block (lines 66–81): nine read-only git subcommand entries (`Bash(git diff:*)` … `Bash(git rev-parse:*)`), plus `Task`/`Read`/`Grep`/`Glob`, `Bash(command -v:*)`, `Bash(jq:*)`, `Bash(rm -rf:*)`. This is where the scoped `-C` entries are added.
- `pkg/factory/factory_test.go` — the `Describe("ExecutionTools")` block (lines 24–51); external test package `factory_test`; imports `context`, `reflect`, `agentlib`, `claudelib`, `delivery`, `factory`, `git`, `githubauth`, `libkafkamocks`, `libtime`, ginkgo, gomega. New code adds `regexp` and `strings` imports.
- `pkg/factory/export_test.go` — line 16: `var ExecutionTools = executionTools` (test-only export of the package-private allowlist; the external test package reads it as `factory.ExecutionTools`).
- `pkg/prompts/execution.go` — the `prefilledArgsHeaderTemplate` const (lines 22–31), passed to `fmt.Sprintf(prefilledArgsHeaderTemplate, baseRef, reviewMode)` at line 136 with exactly two verbs.
- `pkg/prompts/execution_test.go` — the `Describe("BuildExecutionInstructions")` block; the "happy path" case (lines 52–79) calls `prompts.BuildExecutionInstructions(ctx, claudelib.ClaudeConfigDir(tmpDir), "standard", "main", true, sampleFindings, "")` and asserts `workflow` contains `TARGET_BRANCH**: main` and `mode**: standard`.
- `CHANGELOG.md` — currently has NO `## Unreleased` section; latest version heading is `## v0.4.2`.
- The `claudelib` dependency: `github.com/bborbe/agent v0.81.1` (go.mod line 10). Its `AllowedTools` type (`claude/allowed-tools.go`) is `[]string` with only `ParseAllowedTools(string) AllowedTools` and `(AllowedTools) String() string` — there is NO `Allowed(ctx, …)` matcher method; command matching happens inside the Claude CLI, which a unit test cannot invoke. The regression-lock test therefore uses a small test-local helper that models the CLI's matching semantics (see requirement 3).
</context>

<requirements>
1. **Add scoped `-C` entries to `executionTools` in `pkg/factory/factory.go`.** Change the `executionTools` var block (lines 66–81) from its current form to the form below. Keep every existing entry byte-identical; only ADD the 18 `-C` entries. Do NOT add a bare `Bash(git -C:*)` entry.

   Old (existing entries — unchanged):
   ```go
   executionTools = claudelib.AllowedTools{
   	"Task",
   	"Read", "Grep", "Glob",
   	"Bash(git diff:*)",
   	"Bash(git log:*)",
   	"Bash(git show:*)",
   	"Bash(git status:*)",
   	"Bash(git ls-files:*)",
   	"Bash(git fetch:*)",
   	"Bash(git worktree:*)",
   	"Bash(git branch:*)",
   	"Bash(git rev-parse:*)",
   	"Bash(command -v:*)",
   	"Bash(jq:*)",
   	"Bash(rm -rf:*)",
   }
   ```

   New (add the scoped `-C` entries for each read-only subcommand, in glob form; a with-args form `Bash(git -C * <subcmd> *)` AND a bare no-arg form `Bash(git -C * <subcmd>)` per subcommand):
   ```go
   executionTools = claudelib.AllowedTools{
   	"Task",
   	"Read", "Grep", "Glob",
   	"Bash(git diff:*)",
   	"Bash(git log:*)",
   	"Bash(git show:*)",
   	"Bash(git status:*)",
   	"Bash(git ls-files:*)",
   	"Bash(git fetch:*)",
   	"Bash(git worktree:*)",
   	"Bash(git branch:*)",
   	"Bash(git rev-parse:*)",
   	"Bash(git -C * diff *)",
   	"Bash(git -C * log *)",
   	"Bash(git -C * show *)",
   	"Bash(git -C * status *)",
   	"Bash(git -C * ls-files *)",
   	"Bash(git -C * fetch *)",
   	"Bash(git -C * worktree *)",
   	"Bash(git -C * branch *)",
   	"Bash(git -C * rev-parse *)",
   	"Bash(git -C * diff)",
   	"Bash(git -C * log)",
   	"Bash(git -C * show)",
   	"Bash(git -C * status)",
   	"Bash(git -C * ls-files)",
   	"Bash(git -C * fetch)",
   	"Bash(git -C * worktree)",
   	"Bash(git -C * branch)",
   	"Bash(git -C * rev-parse)",
   	"Bash(command -v:*)",
   	"Bash(jq:*)",
   	"Bash(rm -rf:*)",
   }
   ```

   This must NOT appear anywhere: `Bash(git -C:*)` (a bare catch-all would also permit `git -C <workdir> push`/`clean`/`checkout`/`reset`/`merge`, breaking the write/network boundary).

2. **Add worktree-cwd guidance to `pkg/prompts/execution.go`.** Insert a new bullet line into the `prefilledArgsHeaderTemplate` const (lines 22–31), immediately after the `"- **mode**: %s\n"` line and before the `"\n"` blank-line terminator of the bullet list. Result:
   ```go
   const prefilledArgsHeaderTemplate = "## Pre-filled arguments\n\n" +
   	"The procedure below describes a `/coding:pr-review` slash command that takes\n" +
   	"`<target-branch>` and a mode argument. Those arguments have already been\n" +
   	"resolved for this run — do NOT prompt for them, do NOT re-derive them:\n\n" +
   	"- **TARGET_BRANCH**: %s\n" +
   	"- **mode**: %s\n" +
   	"- **Working directory**: your cwd is already the cloned worktree at the PR head — " +
   	"run plain `git` from it, never `git -C <path>`; `git -C` is not needed.\n\n" +
   	"Now follow the procedure below as if the slash command had been invoked with\n" +
   	"those arguments. The procedure references sub-agents via the `Task` tool;\n" +
   	"dispatch them as written.\n\n" +
   	"---\n\n"
   ```
   CRITICAL constraints on the added line:
   - It must contain NO `%` character (the template is passed to `fmt.Sprintf(prefilledArgsHeaderTemplate, baseRef, reviewMode)` with exactly two verbs at line 136; a stray `%` breaks the format at runtime).
   - It must not contain the substrings `description:` or `allowed-tools:` (the happy-path test at `pkg/prompts/execution_test.go:74` asserts `workflow` does NOT contain `description: Test plugin`; the frontmatter-stripping logic must still see the added line as body).
   - The assembled `workflow` must still contain `TARGET_BRANCH**: main` and `mode**: standard` (happy-path assertions at lines 71–72).

3. **Add the `allowedBy` test helper to `pkg/factory/factory_test.go`.** The real matcher lives inside the Claude CLI, so the test models its semantics. Add these imports to the existing import block: `regexp` and `strings`. Then add these two functions at the bottom of the file:
   ```go
   // allowedBy reports whether command would be permitted for toolName by the
   // given Claude-Code --allowedTools entries. It models the CLI's semantics for
   // the three forms this suite uses: "Tool" (bare tool grant), "Tool(prefix:*)"
   // (literal command prefix), and "Tool(pattern)" (glob where * matches any
   // characters, including spaces). Test-only — the real matcher lives inside the
   // Claude CLI, which a unit test cannot invoke.
   func allowedBy(tools claudelib.AllowedTools, toolName, command string) bool {
   	for _, entry := range tools {
   		if entry == toolName {
   			return true
   		}
   		if !strings.HasPrefix(entry, toolName+"(") || !strings.HasSuffix(entry, ")") {
   			continue
   		}
   		pattern := strings.TrimSuffix(strings.TrimPrefix(entry, toolName+"("), ")")
   		if rest, ok := strings.CutSuffix(pattern, ":*"); ok {
   			if strings.HasPrefix(command, rest) {
   				return true
   			}
   			continue
   		}
   		re := regexp.MustCompile("^" + globToRegexp(pattern) + "$")
   		if re.MatchString(command) {
   			return true
   		}
   	}
   	return false
   }

   // globToRegexp converts a Claude-Code tool pattern to an anchored regexp body,
   // treating '*' as zero-or-more of any character and every other byte as
   // literal.
   func globToRegexp(pattern string) string {
   	var b strings.Builder
   	for _, part := range strings.Split(pattern, "*") {
   		b.WriteString(regexp.QuoteMeta(part))
   		b.WriteString(".*")
   	}
   	return strings.TrimSuffix(b.String(), ".*")
   }
   ```

4. **Add the regression-lock test.** Inside the existing `Describe("ExecutionTools")` block in `pkg/factory/factory_test.go` (after the "keeps the anti-injection boundary" `It`), add a new `It` that table-drives `allowedBy` against `factory.ExecutionTools`:

   - with-args read-only rows (want `true`): the observed incident command `git -C /work/32d9ce02-d843-5ed5-9104-552c3ac37aa7 diff 3762333d68eed6fba2fc5208ec8bbac407532108...HEAD -- export/append/pkg/message-handler-event.go`; `git -C <workdir> log --oneline -5`; and one row per read-only subcommand with arguments (`show`, `status`, `ls-files`, `fetch`, `worktree`, `branch`, `rev-parse`).
   - no-arg rows (want `true`): `git -C <workdir> status` and `git -C <workdir> branch`.
   - bare-form row (want `true`): `git diff 3762333d68eed6fba2fc5208ec8bbac407532108...HEAD -- file.go`.
   - denied rows (want `false`): `git -C <workdir> push origin main`, and one each for `clean`, `checkout`, `reset`, `merge`.
   - For each row assert `Expect(allowedBy(factory.ExecutionTools, "Bash", tc.command)).To(Equal(tc.want))`.
   - Presence/absence assertions: `factory.ExecutionTools` `ContainElement` each of the 18 `-C` entries, and does NOT contain `Bash(git -C:*)`.
   - Regression-lock block: build `baseline` = every entry of `factory.ExecutionTools` that does NOT contain `"git -C"`, then assert on `baseline` that all `-C` read-only rows (with-args and no-arg) return `false` while the bare `git diff ...` row still returns `true` — proving the `-C` matches depend on the new entries (removing the scoped entries flips them to denied, per spec Acceptance Criterion 2).

5. **Add the worktree-guidance test to `pkg/prompts/execution_test.go`.** Inside the existing `Describe("BuildExecutionInstructions")` block, add a new `Describe("worktree cwd guidance")` with one `It`. It must call `writePlugin(fakePlugin)` (defined at line 46), then build instructions exactly like the happy-path case — `prompts.BuildExecutionInstructions(ctx, claudelib.ClaudeConfigDir(tmpDir), "standard", "main", true, sampleFindings, "")` — and assert `workflow := instructions[0].Content` contains both `"Working directory"` and `"never ` + "`git -C <path>`" + "`"` (i.e. the literal text `` never `git -C <path>` ``). Do NOT modify any existing `It`.

6. **Update `CHANGELOG.md`.** There is currently no `## Unreleased` section; create one in the correct place — `# Changelog` title → preamble (Keep a Changelog / Semantic Versioning lines) → `## Unreleased` → `## v0.4.2`. Add exactly one bullet:
   ```markdown
   ## Unreleased

   - fix: allow read-only `git -C <workdir>` forms (`diff`, `log`, `show`, `status`, `ls-files`, `fetch`, `worktree`, `branch`, `rev-parse`) in the execution-phase allowlist (`pkg/factory`) so reviews that target the worktree with `-C` no longer die on `permission_denied`, and steer the execution prompt (`pkg/prompts`) to run plain `git` from the worktree cwd
   ```

7. **Verify no stale references.** Grep the repo (`docs/`, `README.md`, `CLAUDE.md`) for any statement that the execution phase denies the `git -C` form or lists the git allowlist in a way this change contradicts; correct any that are now wrong. If none are found, no action.
</requirements>

<constraints>
- Add only scoped `-C` entries to `executionTools` in `pkg/factory/factory.go` — one glob-form entry per read-only subcommand (`Bash(git -C * <subcmd> *)`, plus a bare form `Bash(git -C * <subcmd>)` for no-arg invocations); do NOT add a bare `Bash(git -C:*)` and do not rework or remove existing entries.
- The `AllowedTools` matching semantics are unchanged (new entries only; the glob form `Bash(git -C * <subcmd> *)` is verified to match `git -C <path> <subcmd> <args>` while denying other subcommands).
- The write/network boundary is preserved: `git -C <workdir> push` / `clean` / `checkout` / `reset` / `merge` must remain denied.
- The execution prompt guidance (`pkg/prompts/execution.go`) is updated to tell the model the cwd is the worktree and to use plain `git` (never `git -C`); the guidance must not change the allowlist-driven security model (the scoped entries are the deterministic backstop, the guidance reduces `-C` usage).
- New test uses Ginkgo v2 / Gomega with the existing `AllowedTools` test helper (no live Claude, no network).
- No changes to the execution workdir scheme (`/work/<taskid>`), the re-trigger/dedup semantics, or the heartbeat watchdog.
- The no-space `git -C/path` form is out of scope — `Bash(git -C:*)` matches the spaced form the model uses (observed evidence); do not attempt to match the no-space form.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`, no bare `return err`.
</constraints>

<verification>
Run `make precommit` — must pass.

- `grep -n 'Bash(git -C \* diff \*)' pkg/factory/factory.go` — must return line ≥ 1 (the scoped with-args entry exists)
- `grep -n 'Bash(git -C \*' pkg/factory/factory.go` — must return 18 lines (9 with-args + 9 bare `-C` entries)
- `grep -n 'Bash(git -C:\*)' pkg/factory/factory.go` — must return NO matches (the bare catch-all is forbidden)
- `grep -n -i 'never.*git -C\|git -C.*not' pkg/prompts/execution.go` — must return line ≥ 1 (the cwd guidance exists)
- `grep -n 'allowedBy' pkg/factory/factory_test.go` — must show the helper and its table rows
- `grep -n 'git -C.*push' pkg/factory/factory_test.go` — must show the write-boundary denied row
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the new ExecutionTools `-C` scoping cases and the worktree-guidance test
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license)
</verification>

<!-- AUDITOR NOTES
1. Spec inconsistency (spec Verification section): the spec's Verification section lists `grep -n 'Bash(git -C:\*)' pkg/factory/factory.go` expecting "returns line ≥ 1", but that grep pattern matches the FORBIDDEN bare `Bash(git -C:*)` entry — it contradicts the spec's own Acceptance Criteria (which require the scoped `Bash(git -C \* diff \*)` form) and Constraints (which forbid `Bash(git -C:*)`). Resolution used here: the AC grep `Bash(git -C \* diff \*)` is the authoritative positive check; the `Bash(git -C:\*)` grep is included as a NEGATIVE check (must return no matches). Reviewer should confirm the spec's Verification section is a typo.
2. Spec's minimal repro (`allowed.Allowed(ctx, "Bash", ...)`) and its phrase "the existing `AllowedTools` test helper" do not exist: `claudelib.AllowedTools` (agent v0.81.1) exposes only `ParseAllowedTools` and `String`; matching happens inside the Claude CLI. This prompt therefore creates a NEW test-local `allowedBy` helper modeling the glob semantics. If the real CLI's `*` semantics differ (e.g. `*` does not cross spaces), the scoped entries may behave differently in production — spec Failure Mode 2 covers this ("fix the entry form, do not loosen the matcher"), and the post-deploy Rung-2 AC is the real-world confirmation.
3. The bare no-arg forms `Bash(git -C * <subcmd>)` are added because the spec's constraint makes them conditional on the matcher, and the no-arg rows (`git -C <workdir> status` / `branch`) prove they are needed under the modeled glob semantics (a with-args-only form would not match no-arg invocations).
-->
