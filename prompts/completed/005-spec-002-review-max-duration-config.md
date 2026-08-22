---
status: completed
spec: [002-pr-reviewer-soft-time-budget-and-salvage]
summary: Added REVIEW_MAX_DURATION soft time budget (default 25m, floor 60s) to both entry points with startup validation, threaded it through RunConfig, and documented it in README/CHANGELOG with tests; fixed a depguard prefix-match config bug and a funlen overflow that blocked precommit
execution_id: github-pr-review-agent-salvage-exec-005-spec-002-review-max-duration-config
dark-factory-version: dev
created: "2026-08-21T08:07:20Z"
queued: "2026-08-21T10:14:18Z"
started: "2026-08-21T10:15:09Z"
completed: "2026-08-21T10:21:31Z"
branch: dark-factory/pr-reviewer-soft-time-budget-and-salvage
---

# Add REVIEW_MAX_DURATION soft time budget config to both entry points

<summary>
- Operators can now configure the soft per-phase time budget via a `REVIEW_MAX_DURATION` env var in both entry points (the Kubernetes pod and the local `run-task` CLI)
- The default is 25 minutes when the variable is unset — below the executor's 30-minute hard deadline with headroom for salvage and delivery
- Startup fails fast if the value cannot be parsed as a duration, or is below the 60-second floor, so a misconfiguration can never silently disable the budget
- The env usage string documents the operator invariant that the soft budget must stay below the Kubernetes Job deadline
- The resolved value is plumbed through the shared run configuration so the phase steps can enforce it in a later prompt
- A README row documents the new variable next to the other configuration knobs
- No phase behavior changes yet — this prompt only adds and validates the configuration
</summary>

<objective>
Add a validated, env-driven `REVIEW_MAX_DURATION` soft time budget (default 25m, floor 60s) to both the pod and local-CLI entry points and thread it through the shared `RunConfig`, so every Claude phase run can be bounded below the Kubernetes Job hard deadline. Invalid or below-floor values fail startup rather than silently disabling the budget.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, argument/v2 struct tags, factory wiring).

Read `main.go`:
- The `application` struct (the argument/v2 tag pattern: `required:` / `arg:` / `env:` / `usage:` / `default:`) — see `ReviewMode string ... default:"standard"` and `Phase domain.TaskPhase ... default:"execution"` for the exact tag form to copy.
- `func (a *application) Run(ctx context.Context, _ libsentry.Client) error` — the startup sequence where the validation call goes (after the `start := ...` / phase log line, mirroring the `resolveAuth` error path that records `jobMetrics.RecordRun(agentlib.AgentStatusFailed)` + `jobMetrics.RecordDuration(time.Since(start))` before returning the error).
- The `factory.RunConfig{...}` literal inside `Run` — where `MaxReviewDuration` gets wired.

Read `cmd/run-task/main.go`:
- Its `application` struct (same tag pattern) and `func (a *application) Run(...)` — validation goes at the top of `Run`; wire `MaxReviewDuration` into the `factory.RunConfig{...}` literal there too.

Read `pkg/factory/runner.go`:
- The `RunConfig` struct — add the new field here.
- `RunAgent` — the wiring that consumes it (note: this prompt does NOT yet change step constructor signatures; the step-level threading lands with the budget wrap in the next prompt, which reads the field — a struct field must never be left unread across prompts because `unused` lint fails `make precommit`).

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors.Errorf(ctx, ...)` style
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, coverage rules
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md` — `github.com/bborbe/time` conventions
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` rules

Verify the library contracts before writing (already true of this repo's pinned versions):
- `libtime "github.com/bborbe/time"` — `type Duration stdtime.Duration` with `func (d Duration) Duration() stdtime.Duration` and `func (d Duration) String() string`; `func ParseDuration(ctx context.Context, value interface{}) (*Duration, error)`. `libtime.Duration` implements `encoding.TextUnmarshaler`, which is exactly what `libargument "github.com/bborbe/argument/v2"` uses to parse both the env var and the `default:` tag.
- `libargument.ParseEnv(ctx, data, environ) error` and `libargument.DefaultValues(ctx, data) (map[string]interface{}, error)` + `libargument.Fill(ctx, data, values) error` are the test seams the AC 1 unit test drives.
- `prpkg "github.com/bborbe/github-pr-review-agent/pkg"` is already imported by both `main.go` and `cmd/run-task/main.go` — the shared validation helper lives there with no import cycle.

Read `README.md` lines 43-48 — the Configuration table row to extend.
Read `CHANGELOG.md` — the top of the file (currently no `## Unreleased`; newest heading is `## v0.4.5`).
</context>

<requirements>
1. **Add the shared validation helper.** Create `pkg/max_review_duration.go` (package `pkg`):
   ```go
   // MinReviewMaxDuration is the floor for REVIEW_MAX_DURATION. A value below
   // this would kill a Claude phase run almost immediately; operators must
   // instead keep the soft budget below the K8s Job ActiveDeadlineSeconds
   // (executor default 1800s) with headroom for salvage + Kafka delivery.
   const MinReviewMaxDuration = libtime.Duration(60 * time.Second)

   // ValidateReviewMaxDuration fails fast when the configured soft time budget is
   // below the 60s floor (which also rejects zero and negative values). Called by
   // both entry points at startup so a malformed or near-zero REVIEW_MAX_DURATION
   // cannot silently disable the budget or route every review to human_review.
   func ValidateReviewMaxDuration(ctx context.Context, d libtime.Duration) error
   ```
   The body returns `errors.Errorf(ctx, "REVIEW_MAX_DURATION must be at least %s, got %s", MinReviewMaxDuration, d)` when `d.Duration() < MinReviewMaxDuration.Duration()`, else nil. Imports: `context`, `time`, `github.com/bborbe/errors`, `libtime "github.com/bborbe/time"`.

2. **Add the field to the pod entry point.** In `main.go`, add a field to the `application` struct, next to the other review-config fields (near `ReviewMode`):
   ```go
   // MaxReviewDuration is the soft time budget for each Claude phase run
   // (planning, execution, ai_review), applied below the K8s Job hard deadline.
   // REVIEW_MAX_DURATION is validated at startup: it must parse as a duration
   // and be >= 60s. The default 25m sits below the executor's default
   // ZombieJobTimeoutSeconds (1800s) with headroom for salvage + Kafka delivery;
   // operators who lower zombieJobTimeoutSeconds must keep this below it.
   MaxReviewDuration libtime.Duration `required:"false" arg:"review-max-duration" env:"REVIEW_MAX_DURATION" usage:"Soft time budget per Claude phase run; keep below the K8s Job ActiveDeadlineSeconds / ZombieJobTimeoutSeconds (default 1800s) with headroom for salvage + Kafka delivery; must be >= 60s" default:"25m"`
   ```
   `main.go` already imports `libtime "github.com/bborbe/time"` — no new import.

3. **Validate at startup in the pod entry point.** In `main.go` `Run`, immediately after the existing `glog.V(2).Infof("maintainer-agent-pr-reviewer started phase=%s", a.Phase)` line, add:
   ```go
   if err := prpkg.ValidateReviewMaxDuration(ctx, a.MaxReviewDuration); err != nil {
   	jobMetrics.RecordRun(agentlib.AgentStatusFailed)
   	jobMetrics.RecordDuration(time.Since(start))
   	return err
   }
   glog.V(2).Infof("review max duration=%s", a.MaxReviewDuration)
   ```
   Mirror the `resolveAuth` error path exactly (records the failed metric + duration before returning). `agentlib`, `jobMetrics`, `start`, and `prpkg` are all already in scope.

4. **Add the field to the local CLI entry point.** In `cmd/run-task/main.go`, add the same `MaxReviewDuration libtime.Duration` field to its `application` struct with the IDENTICAL tag from requirement 2. Add the import `libtime "github.com/bborbe/time"` (cmd/run-task does not import it yet).

5. **Validate at startup in the local CLI entry point.** In `cmd/run-task/main.go` `Run`, at the very top (before `ParseRepoAllowlist`), add:
   ```go
   if err := prpkg.ValidateReviewMaxDuration(ctx, a.MaxReviewDuration); err != nil {
   	return err
   }
   glog.V(2).Infof("review max duration=%s", a.MaxReviewDuration)
   ```
   `prpkg` is already imported.

6. **Thread through `RunConfig`.** In `pkg/factory/runner.go`, add to the `RunConfig` struct (with a doc comment explaining the soft per-phase budget and its default):
   ```go
   // MaxReviewDuration is the soft time budget for each Claude phase run,
   // applied below the K8s Job hard deadline. Default 25m (REVIEW_MAX_DURATION).
   MaxReviewDuration libtime.Duration
   ```
   `libtime "github.com/bborbe/time"` is already imported in that file.

7. **Wire from both entry points.** In `main.go` `Run`, add `MaxReviewDuration: a.MaxReviewDuration,` to the `factory.RunConfig{...}` literal. Do the same in `cmd/run-task/main.go` `Run`. (The step-constructor threading and the actual deadline enforcement land in the next prompt — this prompt deliberately stops at `RunConfig` so no unread struct field is ever introduced.)

8. **Unit tests for AC 1.** Add `pkg/max_review_duration_test.go` (external package `pkg_test`, Ginkgo v2 / Gomega, following the style of `pkg/review_test.go`):
   - A `Describe("ValidateReviewMaxDuration")` table: `25m` → nil; `10m` → nil; `60s` → nil; `59s` → error; `0` → error; `-1m` → error. Assert `pkg.ValidateReviewMaxDuration(ctx, d)` directly.
   - A `Describe("review max duration resolution")` block proving the argument/v2 wiring on the real `application` struct — this is the boundary test (struct tag → typed field). It must live in a NEW INTERNAL test file `main_internal_test.go` with `package main` (the `application` type is unexported; external `main_test.go` cannot reference it). Contents:
     - `It("resolves to 1500s (25m) when unset")` — `defaults, err := libargument.DefaultValues(context.Background(), &application{}); err = libargument.Fill(context.Background(), app, defaults)` then `Expect(app.MaxReviewDuration.Duration()).To(Equal(1500 * time.Second))`.
     - `It("equals the parsed value when set")` — `libargument.ParseEnv(context.Background(), &application{}, []string{"REVIEW_MAX_DURATION=30m"})` then assert `app.MaxReviewDuration.Duration() == 1800*time.Second`.
     - `It("fails startup on an unparseable value")` — `libargument.ParseEnv(context.Background(), &application{}, []string{"REVIEW_MAX_DURATION=abc"})` returns an error.
     - Register the suite with its own `func TestXxx(t *testing.T)` calling `RegisterFailHandler(Fail)` + `RunSpecs(t, "Main Config Suite")` (a second suite alongside `main_test.go`'s "Main Suite" is fine — Ginkgo v2 supports multiple `RunSpecs` in one binary). Imports: `context`, `testing`, `time`, `libargument "github.com/bborbe/argument/v2"`, ginkgo, gomega.
   - Below-floor wiring: assert `libargument.ParseEnv(ctx, &application{}, []string{"REVIEW_MAX_DURATION=5s"})` succeeds (parse) AND `pkg.ValidateReviewMaxDuration(ctx, app.MaxReviewDuration)` returns an error (the startup-fail path for a near-zero budget).

9. **Document the variable in README.** In `README.md`'s Configuration table (the rows ending with `REVIEW_MODE` at line 47), add a row:
   ```markdown
   | `REVIEW_MAX_DURATION` | Soft time budget per Claude phase run (default `25m`, floor `60s`); keep below the K8s Job `ActiveDeadlineSeconds` |
   ```

10. **Update `CHANGELOG.md`.** The `## Unreleased` section currently does NOT exist (latest heading is `## v0.4.5`). Create it in the correct place — `# Changelog` title → preamble → `## Unreleased` → `## v0.4.5` — with exactly one bullet:
    ```markdown
    - feat: add `REVIEW_MAX_DURATION` soft time budget env (default `25m`, floor `60s`) to both entry points, validated at startup, and thread it through `RunConfig`
    ```
    If `## Unreleased` already exists (a prior prompt in this batch added it), APPEND this bullet to it; do not create a second section.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- One `REVIEW_MAX_DURATION` for all phase runs — no per-phase budgets (spec Non-goal).
- No opt-out flag that disables the budget — the 25m default applies whenever the var is unset, and a below-floor value fails startup rather than disabling the budget (spec Non-goal + Security/Abuse).
- The K8s hard deadline (`ActiveDeadlineSeconds` / `ZombieJobTimeoutSeconds`) is NOT changed — it lives in the executor/CRD domain (`bborbe/agent`); the soft budget lives below it (spec Non-goal + Constraint). The operator invariant is documented in the env field usage string (requirement 2).
- Do NOT change step constructor signatures in this prompt — the step-level threading and deadline enforcement land in the next prompt, which reads the field. Introducing an unread struct field here would fail the `unused` linter in `make precommit`.
- Do NOT touch `docs/architecture.md` — it describes the three-phase contract this change preserves, and no AC requires editing it.
- Errors use `github.com/bborbe/errors` with context wrapping; no `fmt.Errorf`.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- `grep -n 'REVIEW_MAX_DURATION' main.go` — must return line ≥ 1 (the env tag; AC 1 evidence).
- `grep -n 'REVIEW_MAX_DURATION' cmd/run-task/main.go` — must return line ≥ 1.
- `grep -n 'ValidateReviewMaxDuration' pkg/max_review_duration.go main.go cmd/run-task/main.go` — must show the helper plus the validation call in BOTH entry points.
- `grep -n 'MaxReviewDuration' pkg/factory/runner.go` — must show the `RunConfig` field.
- `grep -n 'REVIEW_MAX_DURATION' README.md` — must return line ≥ 1.
- `go test -mod=mod ./pkg/... . ./cmd/run-task/... -count=1` — must pass, including the new `ValidateReviewMaxDuration` table and the `main_internal_test.go` resolution suite (1500s unset, 1800s set, unparseable fails, below-floor fails validation).
</verification>

<!-- AUDITOR NOTES
1. AC 1's "resolved duration is 1500s when unset" needs a test that drives the real `application` struct through argument/v2. The struct is unexported in package `main`, so this requires a NEW internal test file `main_internal_test.go` with `package main` (external `main_test.go` cannot reference `application`). This is the first internal test file in the repo; it coexists with the existing external `main_test.go` (Ginkgo v2 runs both suites in one binary). If the auditor prefers to avoid a new package, an alternative is asserting `libtime.ParseDuration(ctx, "25m")` resolves to 1500s at the pkg level plus the grep for the `default:"25m"` tag — but that does not traverse the struct-tag boundary, so the internal test is the honest form of the AC.
2. Decomposition decision: the spec's Suggested Decomposition assigns "thread into the claude run context" to this prompt. This prompt threads the value as far as `RunConfig` only; the step-constructor threading + `context.WithTimeout` enforcement land in the NEXT prompt (budget-kill routing) because that prompt reads the fields immediately. Landing the fields here unread would trip the `unused` linter (U1000) and fail `make precommit`. The spec's Desired Behavior 1 (budget enforced per phase) is still fully delivered by the prompt pair.
-->
