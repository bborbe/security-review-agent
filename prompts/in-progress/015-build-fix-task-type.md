---
status: approved
created: "2026-09-16T11:43:14Z"
queued: "2026-09-16T15:06:48Z"
---

<summary>
- The agent gains a new task type, `build-fix`, that it can be spawned on — no new binary, no new scaffolding.
- A `build-fix` task is analyzed end to end: the agent reads the failed Cloud Build log over the Cloud Build REST API, extracts the failure cause, and writes it into the task it was spawned on.
- The task is enriched in place — the failure analysis lands in the task body, the task is unassigned, and it is handed to a human. No second task is filed.
- The agent never modifies code, pushes a branch, or opens a PR — v1 is analysis only.
- The log is fetched over plain HTTP with a scoped service-account token, not by shelling out to a command-line tool — so the fetch is unit-testable and the agent needs only two narrow read grants.
- A task whose `task_type` this binary does not know is still refused, exactly as today; the new route is additive.
- The local task runner picks up the new type automatically, so a `build-fix` task can be analyzed from the command line during development.
- Tests cover the log fetch, the failure-cause extraction, the unassign enrichment, and the dispatch on both entry points.
- README and CHANGELOG both record the new task type and its configuration.
</summary>

<objective>
Add a `build-fix` task type to this binary so that the failed-build tasks emitted by the Google Cloud Build watcher are analyzed automatically: the agent reads the build's log through the Cloud Build REST API, extracts the failure cause, and writes the analysis back into that same task while unassigning it so a human picks it up. This is the second half of the two-agent build-failure chain — the watcher files the task, this agent explains why the build broke, and a human (never this agent) fixes it.
</objective>

<context>
Read CLAUDE.md (repo root) for the project conventions, then read:

- `pkg/task_types.go` — `TaskTypeSecurityReview`. The new constant goes beside it.
- `pkg/task_types_test.go` — the boundary-test shape for a task-type constant (`.Validate(ctx)` + the string value). Mirror it.
- `pkg/factory/factory.go` — `CreateAgentProvider` (the dispatch map and its doc comment), `CreateAgent`, `serviceName`, and the `healthcheck.NewAgent(...)` single-phase construction. This is the file the new route is registered in.
- `pkg/steps_gh_token.go` — `NewGHTokenCheckStep`, the `failed(...)` / `needsInput(...)` result helpers, the `http.Client{Timeout: ...}` + `http.NewRequestWithContext` idiom, the `Authorization` header construction, the `defer func() { _ = resp.Body.Close() }()` form, and the `truncate(s, maxLen)` helper. The new fetch code follows this file.
- `pkg/bitbucket/client.go` and `pkg/githubposter/poster.go` — the other two `http.NewRequestWithContext` + `errors.Wrapf` HTTP clients in this repo; the status-code handling follows them.
- `pkg/steps_override.go` — `TaskTypePROverride` + `overrideStep` + `NewOverrideStep`: the precedent for a code-only step (no Claude, no clone) that mutates the task markdown and returns a terminal `Result`. The new step is the same shape.
- `pkg/steps_gh_token_test.go` and `pkg/steps_override_test.go` — the Ginkgo/Gomega test style for a step (`Describe`, `BeforeEach`, `It`, `agentlib.ParseMarkdown`).
- `pkg/export_test.go` — the `*ForTest` seam pattern (a wrapper that calls an unexported constructor). Add one for the new step's unexported constructor so the external `pkg_test` package can inject a fake fetcher.
- `pkg/factory/factory_test.go` — the `CreateAgentProvider` Describe block to extend.
- `pkg/poster_types.go` — the counterfeiter directive exemplar for a locally-declared interface (`//counterfeiter:generate -o ../mocks/<name>.go --fake-name <Name> . <Interface>`). `pkg/steps_mocks.go` shows the other form, for an interface imported from another module — not what this prompt needs.
- `main.go` and `cmd/run-task/main.go` — both `application` structs (the `libargument` field tags) and both `dispatchAgent` functions. Both call `factory.CreateAgentProvider`.
- `docs/dod.md` — the repo's Definition of Done. Note the Documentation section: README must be updated when a change affects run modes or configuration.
- `docs/architecture.md` — how a task type and its phase routing fit the existing agent; keep the new section consistent with it.
- `CHANGELOG.md` — the `## Unreleased` section placement rule (below the preamble, above the newest `## vX.Y.Z`).

Reference (read-only — the emitting side of the chain and the verified upstream contract):
- The task's frontmatter carries `task_type: build-fix`, `assignee: build-fixer-agent`, `repo`, `commit`, `build_id`, `dashboard`, `status: todo`, `stage`, `refs_resolved`, `task_identifier`. `dir` and `link` are body-table-only fields.
- `repo` and `commit` are frequently empty (`refs_resolved: "false"`) when the watcher's own `gcloud builds describe` failed — the agent must not depend on either.
- The task body carries a markdown table (Dashboard name / Build ID / Repo / Commit / Dir / Trigger URL) and a `## Log fetch` section. **That section is informational to a human reader only — it is NOT the implementation contract.** The agent reads the API, not that command.
- Verified on a real failed Octopus build (`251db52b-c192-4464-8f70-2716d93dbd85`, project `smedia-brogrammers`, region `europe-west3`): the build reports `options.logging: LEGACY` and `logsBucket: gs://159165033271.cloudbuild-logs.googleusercontent.com`, and its log is the GCS object `log-<buildID>.txt` in that bucket (1106 lines, no ANSI escapes). Log lines look like `Step #0 - "check-golang": <text>` and the fatal line is the last one, e.g. `ERROR: build step 0 "<image>" failed: step exited with non-zero status: 2`.

Coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `go-error-wrapping-guide.md` (bborbe/errors wrapping)
- `go-testing-guide.md` (Ginkgo v2 / Gomega, Counterfeiter mocks, external test packages)
- `go-mocking-guide.md` (the counterfeiter directive form)
- `go-glog-guide.md` (`V(n)`-gated logging)
- `go-factory-pattern.md` (Create* prefix, pure composition)
- `go-doc-best-practices.md` (exported symbols need doc comments)
</context>

<requirements>
1. **Define the task type.** Add to `pkg/task_types.go`, beside `TaskTypeSecurityReview`:
   ```go
   // TaskTypeBuildFix is the task type for failed Cloud Build analysis tasks.
   // The google-cloud-build-watcher emits it (as a plain string) for every
   // failed master build; the agent routes it to the code-only build-fix step,
   // which reads the build log, extracts the failure cause, and enriches the
   // task in place. No Claude, no clone, no auto-fix.
   const TaskTypeBuildFix agentlib.TaskType = "build-fix"
   ```
   The literal MUST stay `"build-fix"` — it is the value the watcher already writes into the task frontmatter, and it must satisfy the agentlib `TaskType` runtime regex `^[a-z0-9-]+$`.

2. **Boundary test for the constant.** Extend `pkg/task_types_test.go` with a `Describe("TaskTypeBuildFix", ...)` block mirroring the existing `TaskTypeSecurityReview` block:
   - `pkg.TaskTypeBuildFix.Validate(ctx)` returns nil — the level-1 boundary test proving the literal satisfies the runtime validator (a constant-value assertion alone does not prove this).
   - `string(pkg.TaskTypeBuildFix)` equals `"build-fix"` — the wire contract with the watcher.

3. **The log fetcher seam.** Create `pkg/build_log_fetcher.go` declaring the fetch contract:
   ```go
   //counterfeiter:generate -o ../mocks/build-log-fetcher.go --fake-name BuildLogFetcher . BuildLogFetcher

   // BuildRef identifies one Cloud Build whose log is to be fetched.
   type BuildRef struct {
       BuildID string
       Project string
       Region  string
   }

   // BuildLogFetcher returns the log text of one Cloud Build.
   type BuildLogFetcher interface {
       FetchLog(ctx context.Context, ref BuildRef) (string, error)
   }
   ```
   Exported symbols carry doc comments (repo convention). Add the constructors:
   ```go
   // NewCloudBuildLogFetcher returns the production fetcher, authenticating with
   // Google Application Default Credentials.
   func NewCloudBuildLogFetcher() BuildLogFetcher

   // newCloudBuildLogFetcher is the concrete constructor — exposed for tests
   // (export_test.go) so they can point it at httptest servers and a static token.
   // A nil tokenSource means "resolve ADC from the context on each FetchLog".
   func newCloudBuildLogFetcher(cloudBuildBaseURL, storageBaseURL string, tokenSource oauth2.TokenSource) *cloudBuildLogFetcher
   ```
   `NewCloudBuildLogFetcher()` must stay pure — no I/O, no error return — so `CreateAgentProvider` can compose it. Resolve ADC lazily inside `FetchLog` from the run context, so a credential problem surfaces as a runtime result rather than a boot failure.

4. **`FetchLog` over the Cloud Build REST API.** Implementation contract for `cloudBuildLogFetcher.FetchLog(ctx, ref) (string, error)`:
   - **Validate `ref.BuildID` first** with `uuid.Parse` (`github.com/google/uuid`) and return an `errors.Wrapf` on a mismatch. The ID is interpolated into two URL paths, so it must not be attacker-shaped. No other argv/URL-injection guard is needed.
   - **Token.** Use `f.tokenSource` when set, otherwise `google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/devstorage.read_only")` (`golang.org/x/oauth2/google`). `cloud-platform` is the ONLY OAuth scope the Cloud Build API accepts — verified against the live v1 discovery doc, where `projects.locations.builds.get` lists exactly `["https://www.googleapis.com/auth/cloud-platform"]`. A `cloud-platform.read-only` token is refused with HTTP 403 "insufficient authentication scopes", and no test catches it because the httptest servers never validate scopes. Read-only-ness is enforced by the two IAM roles, not by the scope string. Call `.Token()` and send `Authorization: Bearer <access-token>` — a plain `net/http` request, no Google API client library.
   - **Step 1 — read the build.** `GET {cloudBuildBaseURL}/v1/projects/{ref.Project}/locations/{ref.Region}/builds/{ref.BuildID}` with `http.NewRequestWithContext`. Decode the JSON for `logsBucket`, `options.logging`, and `status`. Every decoded field must actually be read — an unread response-struct field is the same `unused`-linter class that fails `make precommit`. Use `status`: a non-terminal value (`QUEUED` / `WORKING`) returns an `errors.Errorf` naming it, so a build that has not finished is distinguishable from a fetch failure.
   - **Step 2 — read the log object.** If `options.logging` is `CLOUD_LOGGING_ONLY`, `STACKDRIVER_ONLY` (its legacy alias), or `NONE`, the log is not bucket-addressable: return an `errors.Errorf` naming the observed value and the build ID (v1 covers bucket-addressable logs only). Otherwise strip the `gs://` prefix from `logsBucket` and `GET {storageBaseURL}/storage/v1/b/{bucket}/o/{url.PathEscape("log-"+ref.BuildID+".txt")}?alt=media`, returning the response body as the log text. The object name is exactly `log-<buildID>.txt`.
   - **HTTP hygiene.** One `*http.Client` with a `Timeout` (package constant — no config knob). Non-2xx on either call returns an `errors.Wrapf` naming the HTTP status and the URL, including a bounded snippet of the response body via the existing `truncate` helper. Close every response body with the repo's `defer func() { _ = resp.Body.Close() }()` form.
   - **Logging.** `glog.V(2).Infof(...)` for the build read and the log fetch (build ID, bucket, latency). Never `fmt.Print*`.
   - The two base URLs are struct fields so tests can point them at `httptest` servers; production passes `https://cloudbuild.googleapis.com` and `https://storage.googleapis.com` — name both explicitly, since every test injects its own host and a wrong production host would otherwise ship silently.

5. **Extract the failure cause.** Add to `pkg/steps_build_fix.go`:
   ```go
   // ExtractFailureCause returns the failure excerpt from a Cloud Build log.
   // maxLines bounds the excerpt; a value below 1 is treated as 1.
   func ExtractFailureCause(ctx context.Context, log string, maxLines int) (string, error)
   ```
   Contract:
   - The failure is the **last** error the log reports. A build log carries many earlier error-shaped lines (`exit status 1`, `make: *** [...] Error 1`, retried steps) before the fatal one, so anchoring on the first match reports the wrong cause. Anchor on the last line matching an error marker and return the window ending at that line.
   - Case-insensitive markers: `error`, `failed`, `failure`, `panic:`, `exit status`, `not found`, `cannot`, `no such file`. Keep the marker set a package-level `var` so it is one list to extend.
   - The excerpt is at most `maxLines` lines, clipped from the front so the fatal line and its trailing context survive.
   - When the log carries no marker, fall back to the last `maxLines` non-empty lines.
   - **Sanitize for the section splitter.** `agentlib.ParseMarkdown` treats any line beginning with `# ` or `## ` as a section heading regardless of fences, and a Go/cgo build log legitimately contains lines like `# github.com/foo/bar`. Escape a leading `#` on every excerpt line (prefix one space) so re-parsing the enriched task cannot split `## Failure Analysis` apart.
   - An empty log returns `("", nil)` — never an error; the caller decides what an empty analysis means.
   - The function is pure: no I/O, no clock, no globals besides the marker list. It is table-tested in requirement 10, and the injected cap is asserted to be honored (see requirement 7's constructor).

6. **Configuration for the project and region.** The task frontmatter does NOT carry `project` or `region` — only the body's informational command does. Do NOT parse the body: read them from configuration, mirroring the watcher's own `CLOUD_BUILD_PROJECT` / `CLOUD_BUILD_REGION`.
   - Add to the `application` struct in `main.go` **and** in `cmd/run-task/main.go`:
     ```go
     CloudBuildProject string `required:"false" arg:"cloud-build-project" env:"CLOUD_BUILD_PROJECT" usage:"GCP project holding the Cloud Build history (default smedia-brogrammers)" default:"smedia-brogrammers"`
     CloudBuildRegion  string `required:"false" arg:"cloud-build-region"  env:"CLOUD_BUILD_REGION"  usage:"GCP region holding the Cloud Build history (default europe-west3)" default:"europe-west3"`
     ```
     The defaults match the watcher's deploy config and the runbook's project/region, so the local CLI works with no extra environment.
   - Thread both values into `factory.CreateAgentProvider` as two **appended** parameters (`cloudBuildProject, cloudBuildRegion string`), after `maxDuration`. Update every call site in the same change: `main.go`'s `dispatchAgent`, `cmd/run-task/main.go`'s `dispatchAgent`, and the `CreateAgentProvider` call in `pkg/factory/factory_test.go`. Do not reorder existing parameters.

7. **The step.** In `pkg/steps_build_fix.go` implement `buildFixStep` with the `agentlib.Step` contract (`Name`, `ShouldRun`, `Run`):
   ```go
   // NewBuildFixStep constructs the single, code-only build-fix step.
   func NewBuildFixStep(cloudBuildProject, cloudBuildRegion string) agentlib.Step

   // newBuildFixStep is the concrete constructor — exposed for tests
   // (export_test.go) so they can inject a fake fetcher and a small excerpt cap.
   func newBuildFixStep(fetcher BuildLogFetcher, cloudBuildProject, cloudBuildRegion string, maxLines int) *buildFixStep
   ```
   `NewBuildFixStep` passes `NewCloudBuildLogFetcher()` and a package-constant default cap (`defaultMaxExtractedLines`). The struct holds `fetcher`, `cloudBuildProject`, `cloudBuildRegion`, and `maxLines` — and `maxLines` MUST be read by `Run` (it is passed to `ExtractFailureCause`); an unread field fails the `unused` linter and therefore `make precommit`.
   - `Name()` returns `"build-fix"` (lower-kebab-case, matching the existing step names).
   - `ShouldRun` always returns true — the analysis is idempotent because `ReplaceSection` overwrites the previous `## Failure Analysis`.
   - `Run` sequence:
     1. Read `build_id` from the frontmatter with `buildID, ok := md.Frontmatter.String("build_id")`. Missing or empty → `needsInput(...)` naming the missing field, and do not fetch. A task without a build ID is a semantic problem for a human, not a transient failure to retry.
     2. `fetcher.FetchLog(ctx, BuildRef{BuildID: buildID, Project: s.cloudBuildProject, Region: s.cloudBuildRegion})`. A returned error with an empty log → `failed(...)` naming the build ID (transient; the controller retries). A returned error with a non-empty log → continue, and record in the enrichment that the fetch was partial.
     3. `ExtractFailureCause(ctx, log, s.maxLines)`. An extraction error → `failed(...)`.
     4. Enrich the markdown in place, in this order:
        - `md.ReplaceSection(agentlib.Section{Heading: "## Failure Analysis", Body: <body>})`. The body is a short paragraph naming the build ID and the dashboard (read `dashboard` from the frontmatter), followed by a fenced `text` block containing the extracted excerpt. Keep it deterministic — no timestamps, no wall-clock — so the same log produces the same body.
        - **Unassign the task:** `md.Frontmatter["assignee"] = ""`. In this fleet `assignee` names *which agent* handles the task (`build-fixer-agent`), so clearing it is the documented escalation signal that hands the task to a human; the framework records the pre-clear owner as `previous_assignee` on its own write. Do NOT resolve, guess, or invent a human owner, and do NOT derive an assignee from `repo` (which is empty on many of these tasks anyway).
     5. Return `&agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review", Message: <build ID + one-line cause summary>}`. The task is enriched and handed to a human — this agent never advances a build-fix task to `done` and never attempts a fix.
   - Split the body across small helpers so no single function trips the repo's `funlen` limit (80 lines / 50 statements) or `gocyclo`; the repo linter enforces both.
   - Use `glog.V(2).Infof(...)` for the step's progress lines. Error creation goes through `github.com/bborbe/errors` — never `fmt.Errorf`.

8. **Test seams.** Extend `pkg/export_test.go`, following the file's existing naming:
   ```go
   // NewBuildFixStepForTest constructs a buildFixStep with an injected fetcher
   // and excerpt cap so pkg_test can exercise the step without any network call.
   func NewBuildFixStepForTest(fetcher BuildLogFetcher, cloudBuildProject, cloudBuildRegion string, maxLines int) agentlib.Step {
       return newBuildFixStep(fetcher, cloudBuildProject, cloudBuildRegion, maxLines)
   }

   // NewCloudBuildLogFetcherForTest constructs the production fetcher against
   // injected API hosts and a static token, so pkg_test can drive it with httptest.
   func NewCloudBuildLogFetcherForTest(cloudBuildBaseURL, storageBaseURL string, tokenSource oauth2.TokenSource) BuildLogFetcher {
       return newCloudBuildLogFetcher(cloudBuildBaseURL, storageBaseURL, tokenSource)
   }
   ```

9. **Register the route.** In `pkg/factory/factory.go`'s `CreateAgentProvider`, build the build-fix agent and add it to the dispatch map:
   ```go
   buildFixAgent := agentlib.NewAgent(
       agentlib.NewPhase(domain.TaskPhaseExecution, prpkg.NewBuildFixStep(cloudBuildProject, cloudBuildRegion)),
   )
   return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
       prpkg.TaskTypeSecurityReview: domainAgent,
       agentlib.TaskTypeHealthcheck: livenessAgent,
       prpkg.TaskTypeBuildFix:       buildFixAgent,
   })
   ```
   - The step is registered under `domain.TaskPhaseExecution` only — the executor's `PHASE` env defaults to `execution`, and the task_type (not the phase name) is what routes here.
   - Update `CreateAgentProvider`'s doc comment to describe the new route (what it does and that it is analysis-only).
   - The build-fix agent uses none of `claudeConfigDir` / `agentDir` / `model` / `ghToken` / `env` / `repoManager` / `reviewMode` / `repoAllowlist` / `currentDateTime` / `maxDuration` — do NOT add a Claude runner or a clone to it.
   - `CreateAgentProvider` stays pure composition: no conditionals, no I/O, no `context.Background()`.

10. **Real task fixture.** Add `pkg/testdata/build-fix-task.md` containing the **verbatim** emitted task file (do not reformat, reorder, or "fix" its values — including the empty `repo` / `commit` and the `stage: master` value):
    ````markdown
    ---
    assignee: build-fixer-agent
    build_id: 88027472-8599-454f-8112-569739235dad
    commit: ""
    dashboard: mdm-duplicate-finder
    refs_resolved: "false"
    repo: ""
    stage: master
    status: todo
    task_identifier: 9411d991-5921-5606-9a46-d9ec5bd3c0cd
    task_type: build-fix
    ---
    # Build Failure: mdm-duplicate-finder

    A master build is failing. Analyze the Cloud Build log and fix the root cause.

    | Field | Value |
    |---|---|
    | Dashboard name | `mdm-duplicate-finder` |
    | Build ID | `88027472-8599-454f-8112-569739235dad` |
    | Repo | `` |
    | Commit | `` |
    | Dir | `duplicate/finder` |
    | Trigger URL | https://console.cloud.google.com/cloud-build/builds;region=europe-west3/88027472-8599-454f-8112-569739235dad?project=159165033271 |

    ## Log fetch

    ```bash
    gcloud builds log 88027472-8599-454f-8112-569739235dad --project=smedia-brogrammers --region=europe-west3
    ```
    ````
    (The nested fence above is a quoting artifact of this prompt — write the fixture as normal markdown.) Load this file in the step tests so the fixture is the real emitted shape, not one reconstructed from a description. Note it is a *frontmatter* fixture: the step reads `build_id` and `dashboard` from it and never reads the `## Log fetch` block.

11. **Fetcher tests.** Add `pkg/build_log_fetcher_test.go` (external test package `pkg_test`, Ginkgo v2 / Gomega) driving `NewCloudBuildLogFetcherForTest` against two `httptest.NewServer` instances (Cloud Build API + storage API) with `oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})`. Cover:
    - happy path: build JSON returns `logsBucket: "gs://test-logs"` + `options.logging: "LEGACY"` → the storage server receives `GET /storage/v1/b/test-logs/o/log-<id>.txt?alt=media` and the returned string equals the served log body;
    - the build request carried `Authorization: Bearer test-token` and the build path is `/v1/projects/<project>/locations/<region>/builds/<id>`;
    - `options.logging: "CLOUD_LOGGING_ONLY"` → error naming that value, and the storage server recorded zero requests;
    - build API 404 → error naming the status;
    - storage API 500 → error naming the status;
    - a non-UUID `BuildID` → error, and both servers recorded zero requests (the validation runs before any HTTP call).

12. **Step and extraction tests.** Add `pkg/steps_build_fix_test.go` (external test package `pkg_test`, same style as `pkg/steps_override_test.go`). Use the generated `mocks.BuildLogFetcher` fake (`&mocks.BuildLogFetcher{}` with `FetchLogReturns(...)`) and the `pkg/testdata/build-fix-task.md` fixture. Cover:
    - `ExtractFailureCause` as a `DescribeTable` over: a log with one `ERROR:` line; a log whose earlier error lines are transient and whose **last** error line is the fatal one (assert the last one wins — use the real fatal line `ERROR: build step 0 "<image>" failed: step exited with non-zero status: 2`); a log with no marker at all (last-non-empty-lines fallback); a log longer than the cap (assert the excerpt is bounded and still contains the fatal line); a log containing a line that starts with `# ` (e.g. `# github.com/foo/bar`) — assert the excerpt escapes it, so re-parsing the enriched task yields exactly one `## Failure Analysis` section and no spurious heading; an empty log (empty string, no error).
    - the cap is honored through the constructor: `NewBuildFixStepForTest(fake, project, region, 3)` yields an excerpt of at most 3 lines on a long log, while a larger cap yields more — this proves the injected `maxLines` field is actually read.
    - the step's happy path: fetcher returns a canned failing log → `Run` returns no error, `Status == agentlib.AgentStatusDone`, `NextPhase == "human_review"`, the markdown has a `## Failure Analysis` section whose body contains the fatal log line and the build ID, and the frontmatter `assignee` is empty.
    - **frontmatter round-trip:** after `Run`, `md.Marshal(ctx)` and re-`agentlib.ParseMarkdown` the result — the re-parsed `assignee` must be empty (the unassign survives serialization, and the framework's own write path sees a cleared owner).
    - the step with a fetcher that returns `("", <error>)`: result `Status == agentlib.AgentStatusFailed`.
    - the step on a fixture with no `build_id`: result `Status == agentlib.AgentStatusNeedsInput` naming the missing field, and `fetcher.FetchLogCallCount() == 0`.
    - the step's idempotency: running twice over the same markdown leaves exactly one `## Failure Analysis` section (assert `len(md.Sections)` is unchanged between runs, or that the heading appears once in the marshaled output).
    - `step.Name() == "build-fix"` and `ShouldRun` returning true.

13. **Dispatch tests, both entry points.** 
    - In `pkg/factory/factory_test.go`'s `CreateAgentProvider` Describe: add a row asserting `provider.Get(ctx, prpkg.TaskTypeBuildFix)` returns a non-nil agent and no error; extend the existing "unknown task_type" row's assertions so the error message contains `"build-fix"` alongside `"security-review"` and `"healthcheck"` (the provider lists every accepted task type in its miss message); leave the `pr-review` / `pr-override` refusal rows untouched — both must keep being refused.
    - In `cmd/run-task/main_internal_test.go`: add a row that `app.dispatchAgent(ctx, "<fixture with task_type: build-fix>", nil, "token", "/tmp/repos", "/tmp/work")` returns a non-nil agent and no error. This locks the local-CLI dispatch so the new task type cannot silently regress on that entry point.

14. **Documentation.** 
    - `README.md`: document the `build-fix` task type — what it consumes, what it writes, that it is analysis-only, and that it never posts or fixes anything; add `CLOUD_BUILD_PROJECT` / `CLOUD_BUILD_REGION` to the configuration table. Keep the existing security-review content intact.
    - `CHANGELOG.md`: add one bullet under `## Unreleased` (create the section directly below the preamble block and above the newest `## vX.Y.Z` heading if it does not exist):
      `- feat: add a build-fix task type that reads the failed Cloud Build log over the Cloud Build REST API, extracts the failure cause, and enriches the task in place with the analysis while unassigning it for a human (analysis only — no auto-fix)`

15. **Self-check.** Before finishing: re-run `<verification>` and confirm every command passes; walk each numbered requirement above against the change and confirm the analysis-only boundary holds — the new code path must contain no code modification, no branch push, no PR creation, and no second task filed.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- v1 is **analysis only**. The agent must never modify code, push a branch, or open a pull request, and must never create a second task — it enriches the task it was spawned on and stops.
- `task_type` is exactly `"build-fix"` and the watcher's `assignee` literal `"build-fixer-agent"` is not renamed — do not invent different names.
- The log is fetched over HTTPS with a bearer token from Application Default Credentials. Read-only-ness is enforced by IAM, not by the scope string: the runtime identity holds exactly two read grants, `roles/cloudbuild.builds.viewer` and `roles/storage.objectViewer` on the Cloud Build logs bucket. Never an editor or owner role. The OAuth scope list is exactly `cloud-platform` (the only scope the Cloud Build API accepts) plus `devstorage.read_only` — no `*_write` / `full_control` scope, and not `cloud-platform.read-only` (the Cloud Build API rejects it).
- Do NOT shell out to `gcloud` (or any other CLI) — no `os/exec` in the new code path, no subprocess environment handling.
- Do NOT parse `project` / `region` out of the task body's `## Log fetch` block — they come from configuration only.
- Do NOT derive an assignee from `repo` or any other task field, and do not resolve a human owner. Clearing `assignee` IS the escalation.
- `pr-review` and `pr-override` stay refused; the `healthcheck` route stays.
- The build-fix agent takes no Claude runner, no clone, and no GitHub token.
- Do NOT add a config knob beyond `CLOUD_BUILD_PROJECT` / `CLOUD_BUILD_REGION` — the HTTP timeout and the excerpt cap are package constants.
- Factory functions stay pure composition — no conditionals, no I/O, no `context.Background()`.
- `context.Context` is threaded through every I/O call; no `context.Background()` outside `main` and tests.
- Error creation and wrapping go through `github.com/bborbe/errors` (`errors.Wrapf` / `errors.Errorf` / `errors.Wrap`) — never `fmt.Errorf` and never a bare `return err`.
- Logging is `glog` with `V(n)`-gated `Info` — never `fmt.Print*`.
- Exported types, functions, and interfaces carry doc comments.
- Write the import that uses `golang.org/x/oauth2/google` and `github.com/google/uuid` BEFORE running `go mod tidy` — tidy drops a dependency that nothing imports yet. Tidy promotes both to direct requirements.
- Tests use Ginkgo v2 / Gomega with Counterfeiter mocks from `mocks/`; no live network call in any test — every HTTP interaction goes through `httptest`.
- New code has good test coverage (target >= 80%); every added code path is exercised.
- The `.dark-factory.yaml` workflow (direct, pr: false) is untouched.
- Do NOT touch `pkg/verdict.go`, `pkg/steps_override.go`, or the security agent's phase/tool wiring.
- The deploy config (Makefile + svc / sts / kafkauser yaml under the separate `sm-octopus/agent` repo) is out of scope for this prompt — do not create or edit any file outside this repository.
</constraints>

<verification>
- `go generate -mod=mod ./...` — exits 0; `mocks/build-log-fetcher.go` now exists (the new mock is generated before any test compiles).
- `go test -mod=mod -cover ./pkg/... ./cmd/...` — exits 0; the new rows are present (build-fix constant boundary, fetcher httptest rows, extract-failure-cause table, cap-honored row, unassign round-trip, needs-input/failed/idempotency rows, factory dispatch row, run-task dispatch row).
- `grep -c 'TaskTypeBuildFix' pkg/factory/factory.go` — prints a non-zero count (the route is registered, not merely declared).
- `grep -n 'os/exec' pkg/build_log_fetcher.go pkg/steps_build_fix.go` — prints nothing (no subprocess anywhere in the new path).
- `grep -n 'auth/cloud-platform\|devstorage.read_only' pkg/build_log_fetcher.go` — prints both scopes (Cloud Build accepts only `cloud-platform`; the read-only boundary is the two IAM roles, not the scope string). `grep -n 'full_control\|_write' pkg/build_log_fetcher.go` — prints nothing (no write-capable scope).
- `grep -n 'fmt.Errorf\|fmt.Print\|println' pkg/build_log_fetcher.go pkg/steps_build_fix.go` — prints nothing (errors go through `github.com/bborbe/errors`, logging through `glog`).
- `grep -n 'human_review' pkg/steps_build_fix.go` — prints the terminal routing line (the step hands the enriched task to a human, never to `done`).
- `grep -n 'CLOUD_BUILD_PROJECT' main.go cmd/run-task/main.go` — prints a line in each file (both entry points configure the project).
- `make precommit` — exits 0.
</verification>
