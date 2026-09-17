---
status: draft
created: "2026-09-17T08:44:50Z"
---

# Fetch build-fix logs from the Cloud Logging API instead of the build's GCS logs bucket

<summary>
- The agent can finally read a failed build's log: it asks Cloud Logging for the build's log entries instead of trying to download a file from a storage bucket.
- The previous mechanism never worked in production — the bucket is owned by Google and cannot be shared with the agent's service account, so this is a repair, not an enhancement.
- The agent still asks the Cloud Build API first, to confirm the build has finished; an unfinished build still fails with exactly the same message as today.
- Credentials are untouched: the same service-account key from the environment, the same fallback when it is absent, the same log line naming which source was chosen.
- The log query is deliberately time-bounded: without a start timestamp the logging service can answer with "no entries" plus a continuation token, which looks like success and silently produces an empty analysis. That trap is closed by design and pinned by a test.
- The build's log-routing mode no longer matters — builds whose logs were previously unreadable are now readable, and the whole bucket-addressability branch is deleted.
- Reading stays bounded: the same response-size limit applies to the new responses, and the page walk is capped so a misbehaving server cannot spin forever.
- Everything downstream is unchanged — same failure-cause extraction, same task enrichment, same hand-off to a human, still no code modification and nothing posted anywhere.
- Tests cover the multi-page walk, the empty-page-with-continuation-token trap, the structured-payload fallback, the request shape the logging API actually requires, and the failure paths; the bucket-specific tests are replaced.
- The README, the architecture document, and the changelog all describe the new fetch.
</summary>

<objective>
Replace the build-fix log-fetch mechanism with the Cloud Logging API. Today the fetcher reads the log from the build's Cloud Build `logsBucket` over the Cloud Storage JSON API, and that path is dead: the bucket is Google-managed and the agent's service account cannot be granted `storage.objects.get` on it (proven — `testIamPermissions` as that service account returns an empty permission set even with a project-level `roles/storage.objectViewer` binding). The end state: the fetcher confirms the build is finished over the Cloud Build API exactly as today, then reads the build's log entries from Cloud Logging, with the bucket path, its logging-mode branch, and its tests gone.
</objective>

<context>
Read CLAUDE.md (repo root) for the project conventions and docs/dod.md (the repo's Definition of Done — this prompt is validated against it), then read:

- `pkg/build_log_fetcher.go` — the whole file; this is the file being changed. Note `FetchLog` (the two-hop flow and the terminal-status check), `cloudBuildResource` (the decoded subset: `Status`, `LogsBucket`, `Options.Logging`), `fetchBuild` (hop 1, unchanged), `fetchLogObject` and `logBucketFor` (hop 2 — both deleted by this change), `get` (the single authenticated GET helper that owns the body cap and the non-2xx error), `credentialsFromEnv` / `accessToken` (the credential path — unchanged), `isTerminalBuildStatus`, and the package constants.
- `pkg/build_log_fetcher_test.go` — the existing spec file, whose bucket-specific specs are replaced. Note `apiStub` / `newAPIStub` (the httptest double recording request URIs and `Authorization` headers and serving one canned response), `restoreEnv` (the hermetic env-var helper), the `Describe("cloudBuildLogFetcher", ...)` block with its `buildID` / `project` / `region` constants, the body-cap specs that drive the cap through `NewCloudBuildLogFetcherWithCapForTest`, and the separate `Describe("credentialsFromEnv", ...)` block (leave that block's three specs working).
- `pkg/export_test.go` — the `*ForTest` seam pattern. `NewCloudBuildLogFetcherForTest` and `NewCloudBuildLogFetcherWithCapForTest` are the two seams this change adapts.
- `pkg/steps_build_fix.go` — `buildFixStep.fetchLog`: a fetch error with a **non-empty** log is treated as a partial result and the analysis proceeds on what came back; a fetch error with an empty log is returned as an error and the controller retries. This is the contract the page cap must feed into.
- `pkg/steps_gh_token.go` — `truncate(s string, maxLen int) string`, used for the bounded response-body snippet in the non-2xx error message.
- `pkg/factory/factory.go` — `CreateAgentProvider` composes `NewBuildFixStep(cloudBuildProject, cloudBuildRegion)`. Confirm this change needs no edit there.
- `README.md` — the `## Build-fix tasks` section (its "How the log is fetched" numbered list names the bucket path, the logging-mode branch, and the runtime identity's two IAM grants) and the `## Configuration` table (`GCP_KEY_JSON` row).
- `docs/architecture.md` — the `## The build-fix Task Type` section (the paragraph describing the fetcher).
- `CHANGELOG.md` — the `## Unreleased` section placement rule (below the preamble, above the newest `## vX.Y.Z`).

Facts about the environment (verified upstream — the watcher repo is NOT mounted in this container, so do not look for it):

- The fleet passes the GCP service-account key as an **environment variable** named `GCP_KEY_JSON`, never as a file.
- The container you are running in has **no GCP credentials and no live access to the Google APIs**. Implement the request shape exactly as specified below; do not attempt a live call, and do not re-derive the spec from memory. Everything you need to get right is written out here, and the tests are what lock it.
- The build's Cloud Build `logsBucket` is Google-managed (`gs://<project-number>.cloudbuild-logs.googleusercontent.com`). The agent's service account cannot be granted read access to it — that is why the current path fails and why it is being removed rather than repaired.

**Verified REST spec for the new hop** (measured live against the real API for project `smedia-brogrammers`, region `europe-west3`, and re-verified against the public Cloud Logging v2 reference):

- Endpoint: `POST https://logging.googleapis.com/v2/entries:list`
- Headers: `Authorization: Bearer <access-token>`, **`X-Goog-User-Project: <project>`**, `Content-Type: application/json`
- Request body:

  ```json
  {"resourceNames":["projects/<project>"],
   "filter":"timestamp>=\"<RFC3339 start>\" AND resource.type=\"build\" AND resource.labels.build_id=\"<buildID>\"",
   "orderBy":"timestamp asc",
   "pageSize":1000}
  ```

- The response carries `entries[]` and `nextPageToken`. Continue with the token until it is absent, and flatten each entry's `textPayload` (falling back to `jsonPayload.message`) joined by newlines, in returned order.

**The load-bearing gotcha** — the public reference documents it verbatim: *"If a value for `nextPageToken` appears and the `entries` field is empty, it means that the search found no log entries so far but it did not have time to search all the possible log entries. Retry the method with this value for `pageToken` to continue the search. Alternatively, consider speeding up the search by changing your filter to specify a single log name or resource type, or to narrow the time range of the search."* The reference adds: *"To make queries faster, you can make the filter more selective by using restrictions on indexed fields as well as limit the time range of the query by adding range restrictions on the `timestamp` field."* An empty `entries` array alongside a valid `nextPageToken` therefore looks exactly like a permissions failure and is not one; the `timestamp>=` clause is what keeps the search bounded, and without it the fetch silently yields an empty log. The same reference recommends `orderBy: "timestamp desc"` for recently ingested entries (*"the default value of `timestamp asc` may take a long time to fetch matching logs that are only recently ingested"*). `timestamp asc` is still correct here — the flattened log must be chronological, and the filter already applies both remedies the reference recommends (a resource-type restriction and a `timestamp>=` range bound) — so record that rationale in a code comment, or a future reader may "fix" the `orderBy` and silently reverse the log order.

Coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `go-error-wrapping-guide.md` (bborbe/errors wrapping)
- `go-testing-guide.md` (Ginkgo v2 / Gomega, external test packages)
- `go-glog-guide.md` (`V(n)`-gated logging)
- `go-doc-best-practices.md` (exported symbols need doc comments)
- `changelog-guide.md` (the `## Unreleased` entry style)
</context>

<requirements>
1. **Swap the second hop's base URL and constructor parameter in `pkg/build_log_fetcher.go`.**

   ```go
   // loggingAPIBaseURL is the production Cloud Logging v2 REST host, used to
   // read the build's log entries.
   loggingAPIBaseURL = "https://logging.googleapis.com"
   ```

   Replace the `storageAPIBaseURL` constant with it, rename the struct field `storageBaseURL` → `loggingBaseURL`, and rename the second parameter of `newCloudBuildLogFetcher` from `storageBaseURL` to `loggingBaseURL`:

   ```go
   func newCloudBuildLogFetcher(cloudBuildBaseURL, loggingBaseURL string, tokenSource oauth2.TokenSource) *cloudBuildLogFetcher
   ```

   Keep the arity and the constructor split exactly as they are: `NewCloudBuildLogFetcher()` stays pure (no I/O, no error return) and passes `cloudBuildAPIBaseURL, loggingAPIBaseURL, nil`; a `nil` token source still means "resolve `GCP_KEY_JSON` / ADC from the environment on each `FetchLog`"; both base URLs are still `strings.TrimSuffix(..., "/")`-normalised fields so tests can point them at `httptest` servers.

   Delete the now-dead code entirely: `storageAPIBaseURL`, the `storageBaseURL` field, `fetchLogObject`, `logBucketFor`, and the `LogsBucket` / `Options` fields of `cloudBuildResource`. An unused package-level constant fails the `unused` linter in `make precommit`, so nothing may be left behind.

2. **Keep hop 1 exactly as it is, plus one new field.** The build read stays `GET {cloudBuildBaseURL}/v1/projects/{project}/locations/{region}/builds/{buildID}` with `url.PathEscape` on each interpolated segment and the same `http.NewRequestWithContext` + bearer-token form. Decode the subset the fetcher now needs:

   ```go
   type cloudBuildResource struct {
       Status    string `json:"status"`
       StartTime string `json:"startTime"`
   }
   ```

   `isTerminalBuildStatus` and its `QUEUED` / `WORKING` / `PENDING` list stay. The non-terminal error message stays **verbatim**: `"cloud build %s has not finished (status=%s)"` with the build ID and the observed status. Do **not** send `X-Goog-User-Project` on this hop — the Cloud Build call is unchanged. Every decoded field must be read (the `unused` linter class), which `Status` (terminal check) and `StartTime` (filter bound) both are.

3. **Read the log from Cloud Logging.** Add a `fetchLogEntries` method (or an equivalently named single method — one hop, one method) that issues, for the build's `ref.Project` and `ref.BuildID`:

   - Method and URL: `POST {loggingBaseURL}/v2/entries:list`
   - Headers, exactly:
     - `Authorization: Bearer <access-token>` — the same `token.Type()+" "+token.AccessToken` form the existing helper uses
     - `X-Goog-User-Project: {ref.Project}` — the quota-project header. It is mandatory for this call; without it the request is not authorized for the project. Same value as the `projects/…` entry in `resourceNames`.
     - `Content-Type: application/json`
   - Body: a JSON object with `resourceNames: ["projects/<ref.Project>"]`, `filter`, `orderBy: "timestamp asc"`, `pageSize: 1000`, and `pageToken` on every page after the first. Declare `pageSize` as a named package constant (e.g. `loggingPageSize = 1000`), not a magic number and not a config knob. Serialize `pageToken` so it is omitted while empty.
   - The filter is exactly three clauses joined by ` AND `:

     ```
     timestamp>="<lower bound>" AND resource.type="build" AND resource.labels.build_id="<buildID>"
     ```

     `resource.labels.build_id` is the exact selector; `resource.type="build"` narrows the search; the `timestamp>=` clause is the load-bearing one (see requirement 4).

4. **The timestamp clause is mandatory and must never be silently omitted.** The lower bound is the build's `startTime` (the RFC3339 string hop 1 decoded), falling back to `createTime` when `startTime` is empty — so decode `createTime` too if you take the fallback. Pass the API's timestamp string through verbatim: do not re-parse it, do not reformat it, do not add a margin constant. When both are empty, return an `errors.Errorf` naming the build ID and the missing timestamps and make **no** HTTP call — never fall through to a filter without the `timestamp>=` clause, because that is precisely the case the API answers with an empty entry list plus a valid `nextPageToken` (the documented behavior quoted in `<context>`), which is indistinguishable from a real failure and produces an empty `## Failure Analysis` instead of an error.

5. **Page until the token is absent — and never treat an empty page as the end.** The loop's continuation condition is the presence of `nextPageToken` and nothing else. An empty `entries` array on a page is not a stop condition, not an error, and not a signal that the log is empty: the API legitimately returns an empty page with a token while it is still searching, and the documented remedy is to retry with that token. Do not `break` on `len(entries) == 0`, do not return early on it, and do not special-case it. Keep the loop flat — `nestif`, `gocognit`, `gocyclo` and `funlen` (80 lines / 50 statements) are enforced by `make precommit`, so factor the per-page work into small helpers instead of nesting.

6. **Flatten the entries into the log text.** The response decodes to package-level unexported types (e.g. `logEntriesResponse` / `logEntry`) shaped as:

   ```json
   {"entries":[{"textPayload":"Step #0 - \"check-golang\": building",
               "jsonPayload":{"message":"structured line"}}],
    "nextPageToken":"<opaque>"}
   ```

   Contract:
   - Per entry, the text is `textPayload`; when that is empty, the text is `jsonPayload.message`.
   - `jsonPayload` is a free-form JSON object, so decode it so that a payload that is not an object, or whose `message` is not a JSON string, cannot fail the whole response — such an entry simply contributes no text. A single odd entry must not abort the fetch of a 1000-entry page.
   - Accumulate the per-entry texts **across all pages** and join them once with `"\n"`, in the order the API returned them (which is chronological, given `orderBy: "timestamp asc"`). One join rule for the whole log — no page-boundary special case, no trailing newline, no trimming.
   - An empty result (no entries at all across all pages) returns an empty string with **no** error — the caller decides what an empty analysis means (`ExtractFailureCause` already returns `("", nil)` for an empty log). Do not invent an error for it.

7. **Reuse the response-body bound on both hops through one shared execution helper.** The `maxBodyBytes` cap and the `io.LimitReader` read (one byte past the cap so an exactly-maximal body is distinguishable from an oversized one) must apply to the Cloud Logging responses too, and the non-2xx handling must stay: read the body first, then fail with an error naming the **actual HTTP method used**, the URL, the status code, and a bounded snippet via `truncate(..., errorBodySnippetLen)`. Refactor the existing `get` helper into one shared execution helper that both hops call (the `dupl` linter is enabled, and two near-identical helpers would be flagged) — the build hop keeps its `GET` error text, the logging hop reports `POST`. Keep `cloudBuildHTTPTimeout` as the `http.Client` timeout, keep the `maxBodyBytes` **field** (not the constant directly) so tests can drive the oversize guard with a few bytes, and keep the oversize message's `exceeds <n> bytes` wording.

8. **Cap the page walk.** Add a package constant `maxLogPages` (100) as a runaway guard: a page walk that keeps returning a token must terminate instead of spinning forever inside a Kubernetes Job. On hitting the cap, return the text accumulated so far **together with** an error naming the cap — `buildFixStep.fetchLog` treats a non-empty log plus an error as a partial result and proceeds, while an empty log plus an error is retried by the controller, which is the correct behavior for both cases. `maxLogPages` is also a struct field (beside `maxBodyBytes`) so tests can drive it with a small value. Do not add any other bound, retry, backoff, or timeout knob.

9. **Keep the credential path untouched.** `credentialsFromEnv` and `accessToken` keep their current shape: `GCP_KEY_JSON` → `google.CredentialsFromJSON`, ADC only when that env var is unset, the same two `glog.V(2).Infof` lines naming which source was chosen, the same wrap messages (`"parse %s service-account key"`, `"resolve google application default credentials"`, `"mint cloud build access token"`), and the same injected-token-source precedence (a non-nil `f.tokenSource` is used directly and `credentialsFromEnv` is not consulted). The OAuth scope list stays exactly `cloudBuildScope` + `storageReadScope` — `cloud-platform` already covers the Cloud Logging API, and narrowing the list is a separate change; do not add, remove, or rename a scope. Update `storageReadScope`'s doc comment only so it no longer claims the fetcher reads a log object out of a bucket, and correct `cloudBuildScope`'s doc comment so it no longer names `roles/storage.objectViewer` as one of the runtime identity's two IAM grants — the read boundary is now `roles/cloudbuild.builds.viewer` plus `roles/logging.viewer` (which carries `logging.logEntries.list`). Resolution stays lazy (inside `FetchLog`, per call): do not read the environment in the constructors and do not change any production signature.

10. **Keep the flow, the logs, and the seam.**
    - `FetchLog` keeps its order: validate `ref.BuildID` with `uuid.Parse` first and return the existing `"invalid cloud build id %q"` error (it is interpolated into a URL path and a filter string, so it must not be attacker-shaped); mint the token; read the build; fail on a non-terminal status; then read the entries. Keep the `BuildLogFetcher` interface and its counterfeiter directive unchanged, so `mocks/build-log-fetcher.go` needs no regeneration.
    - `glog.V(2).Infof` on the build read (build ID, status, latency — the current line's `logging=%s` field goes away with the removed struct field) and on the log fetch (build ID, page count, entry count, byte count of the returned log, latency). Never `fmt.Print*`.
    - Adapt the two seams in `pkg/export_test.go`: `NewCloudBuildLogFetcherForTest(cloudBuildBaseURL, loggingBaseURL string, tokenSource oauth2.TokenSource) BuildLogFetcher` (same arity, renamed parameter and doc comment) and a caps seam that can drive both guards with small values — `NewCloudBuildLogFetcherWithCapsForTest(cloudBuildBaseURL, loggingBaseURL string, tokenSource oauth2.TokenSource, maxBodyBytes int64, maxLogPages int) BuildLogFetcher` (replacing `NewCloudBuildLogFetcherWithCapForTest`).

11. **Tests in `pkg/build_log_fetcher_test.go`.** Extend the existing `apiStub` double so it can serve a **sequence** of responses (one per request, so a multi-page walk can be driven), record the request **method**, the request **bodies**, and the `X-Goog-User-Project` header — while its current single-response use for the build hop keeps working. Then, in the external `pkg_test` package, in the file's existing Ginkgo/Gomega style:
    - **Request shape (the boundary this change crosses).** A spec that asserts the logging hop's full request, because the httptest double is the only place the external contract can be checked without live GCP access: the recorded URI is `/v2/entries:list`, the method is `POST`, `Authorization` is `Bearer test-token`, `X-Goog-User-Project` equals the project, `Content-Type` is `application/json`, and the decoded body has `resourceNames == ["projects/smedia-brogrammers"]`, `orderBy == "timestamp asc"`, `pageSize == 1000`, and a `filter` containing all three clauses — `timestamp>=`, `resource.type="build"`, and `resource.labels.build_id="<buildID>"`. A shape test that only asserts the returned string does not satisfy this.
    - **The timestamp bound comes from the build.** The build stub returns a `startTime`, and the filter in the recorded body contains that exact timestamp string; a second spec with an empty `startTime` and a `createTime` proves the fallback; a third with both empty proves the fetch fails with an error naming the build ID and issues **zero** logging requests.
    - **Multi-page walk.** Page 1 returns entries plus a `nextPageToken`, page 2 returns more entries with no token: the returned log is the concatenation of both pages' entries joined by newlines in order, with no missing or duplicated separator at the page boundary, and exactly two logging requests were made (the second carrying the first page's `pageToken`).
    - **The empty-page-with-token trap.** A page whose `entries` array is empty **and** whose `nextPageToken` is set, followed by a page with entries: the returned log must contain the second page's entries. This is the regression lock for requirement 5 — a loop that stops on an empty page returns an empty log and fails here. Assert the entry content, not merely that no error occurred.
    - **`jsonPayload.message` fallback.** An entry with an empty `textPayload` and a `jsonPayload.message` contributes the message text; an entry with neither contributes nothing and must not fail the response.
    - **Logging API failure.** A non-2xx response (e.g. `403` with a JSON error body) makes `FetchLog` return an error naming the status and the URL, with an empty log, and the build stub was still called exactly once.
    - **Page cap.** With a small `maxLogPages`, a stub that keeps returning a token plus one entry terminates and returns the accumulated text together with an error naming the cap.
    - **Rewritten cap specs.** Replace the two GCS-shaped cap specs (`when the log object exceeds the body cap`) with logging-hop equivalents: one driving `maxBodyBytes` so a logging response body over the cap errors with the `exceeds <n> bytes` wording, one accepting a body exactly at the cap. The build hop's metadata JSON must still fit inside the test cap.
    - **Rewritten happy path.** Replace `when the build is bucket-addressable` and `when the build logs are not bucket-addressable` — the bucket-addressability branch no longer exists, so those two `Context` blocks are deleted, not adapted. Keep and keep passing: the non-terminal-status spec (its `WORKING` assertion and its zero-logging-request assertion), the build-API-failure spec, the UUID spec (zero requests on either stub), and the credential specs (both the `Describe("cloudBuildLogFetcher", ...)` ones and the whole `Describe("credentialsFromEnv", ...)` block).
    - No test may make a live network call; every HTTP interaction goes through `httptest`, and every spec that mutates the environment restores it with `restoreEnv` so the suite stays hermetic and order-independent.

12. **Documentation.**
    - `README.md`, `## Build-fix tasks`: rewrite the "How the log is fetched" list to the two hops that exist — (1) `GET /v1/projects/{project}/locations/{region}/builds/{build_id}` on the Cloud Build REST API to confirm the build has finished and read its `startTime`, (2) `POST /v2/entries:list` on the Cloud Logging API with `resourceNames: ["projects/{project}"]`, the `X-Goog-User-Project: {project}` header, the filter `timestamp>="{start}" AND resource.type="build" AND resource.labels.build_id="{build_id}"`, and `nextPageToken` paging. State why the `timestamp>=` bound is required (without it the API can answer with an empty entry list plus a valid continuation token). Delete the `logsBucket` / `CLOUD_LOGGING_ONLY` / `STACKDRIVER_ONLY` / `NONE` paragraph and the claim that the log is not bucket-addressable. Replace the runtime-identity sentence: the read boundary is the identity's IAM grants — the Cloud Build viewer grant already named there for the build, plus `logging.logEntries.list` on the project (`roles/logging.viewer` carries it) and `serviceusage.services.use` for the quota-project header — and never an editor/owner role. Keep the UUID-validation and non-terminal-status sentences. In the `## Configuration` table, correct the `GCP_KEY_JSON` row so it no longer says the key is used to read a log object. Keep the existing scope sentence (`cloud-platform` plus `devstorage.read_only`) unchanged — requirement 9 keeps both scopes in the code, so code and doc must not diverge in the same commit.
    - `docs/architecture.md`, `## The build-fix Task Type`: correct the `pkg/build_log_fetcher.go` paragraph so it describes the two hops (Cloud Build API for the terminal status and the start timestamp, Cloud Logging `entries:list` for the log text), still plain `net/http` with the same bearer token and still unit-testable against `httptest`. Nothing else in that file changes.
    - `CHANGELOG.md`: add one bullet to the existing `## Unreleased` section (add to it — do not create a second one, and do not touch a released section):

      `- fix: read build-fix Cloud Build logs from the Cloud Logging API (\`entries:list\`) instead of the build's Google-managed GCS logs bucket — the bucket cannot be read by the agent's service account, so the previous fetch always failed. The build's terminal status is still read from the Cloud Build API first, and the log query carries a \`timestamp>=\` lower bound because the Logging API otherwise answers with an empty entry list alongside a valid \`nextPageToken\`, which is indistinguishable from an empty log.`

13. **Self-check.** Before finishing: re-run every command in `<verification>` and confirm each passes, then walk each numbered requirement above against the change. Confirm specifically that (a) no request can be built without the `timestamp>=` clause, (b) the page loop's only stop condition is the missing `nextPageToken`, (c) no file outside the list in `<constraints>` was modified, (d) no production signature changed, so no call site needed updating, and (e) `grep -rn --exclude-dir=vendor 'logsBucket\|storage.googleapis.com\|CLOUD_LOGGING_ONLY\|STACKDRIVER_ONLY\|logBucketFor\|fetchLogObject' --include='*.go' --include='*.md' .` prints nothing outside `prompts/`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- **Scope: `pkg/build_log_fetcher.go`, `pkg/build_log_fetcher_test.go`, `pkg/export_test.go`, `README.md`, `docs/architecture.md`, `CHANGELOG.md`.** Nothing else. In particular: no `.dark-factory.yaml`, no `Makefile*`, no `go.mod` change, no new dependency, no deploy/Helm config, and no IAM change (the fleet's grants are managed outside this repo).
- **No new configuration.** No env var, no config field, no flag, no knob — not for the logging host, not for the page size, not for the project, not for a page cap, not for a timeout. Every one of those is a package constant or derived from `ref`.
- **Do NOT change any production signature.** `NewCloudBuildLogFetcher()`, `newCloudBuildLogFetcher(...)`, `BuildLogFetcher`, `BuildRef`, `NewBuildFixStep(...)` and `factory.CreateAgentProvider(...)` keep their shapes; `pkg/steps_build_fix.go`, `pkg/factory/factory.go`, `main.go`, `cmd/run-task/main.go` and `mocks/build-log-fetcher.go` must compile unchanged — if one of them needs an edit, the design has drifted.
- **The credential path is untouched.** `GCP_KEY_JSON` → `google.CredentialsFromJSON`, ADC only when that env var is unset, both branches logging which source was chosen. The OAuth scope list stays exactly `cloudBuildScope` + `storageReadScope`: `cloud-platform` already covers the Cloud Logging API, and narrowing the list is a separate change. Never write key material to disk, never log it, never put it in an error message or a test fixture, and never shell out to `gcloud` or any other CLI (`os/exec`).
- **Read-only identity is load-bearing.** The agent reads only: build metadata and log entries. Never an editor or owner role, never a write scope, and no `*_write` / `full_control` scope anywhere in this file.
- **The `build-fix` boundary is unchanged.** Analysis only: no Claude runner, no clone, no code modification, no branch push, no PR, no second task, never an advance to `done`.
- Do NOT touch the security-review path (`pkg/steps_planning.go`, `pkg/steps_checkout_execution.go`, `pkg/steps_review.go`, `pkg/verdict.go`, `pkg/factory/factory.go`, the phase/tool wiring) or `pkg/steps_build_fix.go`.
- Error creation and wrapping go through `github.com/bborbe/errors` (`errors.Wrap` / `errors.Wrapf` / `errors.Errorf`) — never `fmt.Errorf` and never a bare `return err`. Logging is `glog` with `V(n)`-gated `Info` — never `fmt.Print*`.
- Exported types, functions, and interfaces carry doc comments; `revive`, `funlen` (80 lines / 50 statements), `gocyclo`, `gocognit` (20), `nestif` (4), `maintidx` (20), `dupl`, `unused`, `bodyclose`, `prealloc` and `errcheck` are all enabled in `.golangci.yml` and enforced by `make precommit`.
- `context.Context` is threaded through every I/O call; every request is built with `http.NewRequestWithContext` so caller cancellation terminates the page walk; no `context.Background()` outside `main` and tests.
- Tests use Ginkgo v2 / Gomega in the external `pkg_test` package; env mutations are restored so the suite stays hermetic and order-independent; no live network call in any test.
- New code has good test coverage (target >= 80%); every added branch — including the timestamp fallbacks, the empty-page continuation, the payload fallback, and the page cap — is exercised.
</constraints>

<verification>
- `make precommit` — exits 0 (fmt, generate, test, lint, vet, vuln, license).
- `go test -mod=mod -count=1 ./pkg/ -run TestSuite -args -ginkgo.focus='cloudBuildLogFetcher' -ginkgo.fail-on-empty` — exits 0 and reports a non-zero number of specs run (`-ginkgo.fail-on-empty` turns a mis-named focus into a failure instead of a false pass). Note this focus matches only the fetcher block; the `credentialsFromEnv` block runs under `make precommit`'s full-suite pass.
- `grep -n 'logging.googleapis.com' pkg/build_log_fetcher.go` — prints the production Cloud Logging host, so a wrong host cannot ship silently.
- `grep -n 'X-Goog-User-Project' pkg/build_log_fetcher.go` — prints the quota-project header being set.
- `grep -n 'timestamp>=' pkg/build_log_fetcher.go` — prints the filter's time bound in production code, not only in the tests.
- `grep -n 'io.LimitReader' pkg/build_log_fetcher.go` — prints the bounded body read shared by both hops.
- `grep -n 'maxLogPages' pkg/build_log_fetcher.go pkg/build_log_fetcher_test.go` — prints the page cap in both the production code and a spec.
- `grep -n 'maxResponseBodyBytes' pkg/build_log_fetcher.go` — prints the production body cap still wired as the default.
- `grep -n 'cloud-platform' pkg/build_log_fetcher.go` — prints the unchanged OAuth scope.
- `! grep -q 'storage.googleapis.com\|logsBucket\|CLOUD_LOGGING_ONLY\|STACKDRIVER_ONLY\|logBucketFor\|fetchLogObject' pkg/build_log_fetcher.go` — the GCS hop and the whole bucket-addressability branch are gone from production code.
- `! grep -q 'storage.googleapis.com\|logsBucket\|CLOUD_LOGGING_ONLY\|bucket-addressable\|storageBaseURL\|when the storage API fails' pkg/build_log_fetcher_test.go` — the GCS-specific specs and the old storage-hop seam were replaced, not left behind.
- `! grep -q 'CLOUD_LOGGING_ONLY\|STACKDRIVER_ONLY\|logsBucket\|storage.objectViewer' README.md docs/architecture.md` — the docs no longer describe the removed path.
- `grep -l 'entries:list' README.md docs/architecture.md CHANGELOG.md` — prints all three file names (each document names the new call).
- `! grep -q 'fmt.Errorf' pkg/build_log_fetcher.go` — errors go through `github.com/bborbe/errors`.
- `! grep -q 'os/exec\|gcloud' pkg/build_log_fetcher.go` — no subprocess on this path.
- `! grep -rn 'NewCloudBuildLogFetcherWithCapForTest' --include='*.go' .` — the replaced caps seam has no stale call site.
</verification>
