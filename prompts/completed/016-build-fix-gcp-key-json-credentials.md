---
status: completed
summary: Made the build-fix log fetcher resolve its bearer token from the GCP_KEY_JSON service-account key when set, falling back to Application Default Credentials only when unset, with branch-selection and full-path tests plus docs
execution_id: security-review-agent-build-fix-exec-016-build-fix-gcp-key-json-credentials
dark-factory-version: dev
created: "2026-09-16T15:41:08Z"
queued: "2026-09-16T16:00:02Z"
started: "2026-09-16T16:01:05Z"
completed: "2026-09-16T16:05:08Z"
---

# Fix build-fix credential resolution: use the GCP_KEY_JSON service-account key when set, never the node metadata identity

<summary>
- The agent's build-log reader authenticates with the service-account key the fleet already passes in as an environment variable, instead of silently picking up the identity of the machine it runs on.
- When that variable is absent, behaviour is unchanged: credentials are still discovered through the standard Google chain, so local development keeps working.
- A key that cannot be parsed is now a loud, named error instead of a quiet fallback to a broader identity.
- The narrow read-only identity stays exactly as before — the same two read grants, the same OAuth scopes, no write access, and no key material ever written to disk.
- Nothing about the surrounding behaviour changes: same task type, same log fetch, same vault write-back, and no new configuration knob.
- The change is confined to the credential lookup, so both entry points (the Kubernetes Job and the local task runner) pick it up with no wiring change.
- Tests pin all three branches — key present and usable, key present and malformed, key absent — and prove the malformed case cannot silently fall back.
- The repository changelog records the fix.
</summary>

<objective>
Make the `build-fix` log fetcher resolve its bearer token from the `GCP_KEY_JSON` environment variable when that variable is set, falling back to the Application Default Credentials chain only when it is unset. Today the fetcher always calls the ADC chain, and in the deployed pod that silently resolves to the node's service account — a broad identity the operator explicitly does not want this agent to hold. The end state: the agent authenticates as the narrow, read-only identity the deploy config supplies, and an unusable key fails loudly instead of degrading into a broader one.
</objective>

<context>
Read CLAUDE.md (repo root) for the project conventions and docs/dod.md (the repo's Definition of Done — this prompt is validated against it), then read:

- `pkg/build_log_fetcher.go` — the whole file. Focus on `accessToken` (the credential resolution being changed), `cloudBuildScope` / `storageReadScope` (the two scopes that must not change), `newCloudBuildLogFetcher` (the injected-token-source seam, `nil` means "resolve from the environment"), and the `glog.V(2).Infof(...)` / `errors.Wrap*` style used throughout.
- `pkg/build_log_fetcher_test.go` — the existing spec file this change extends. Note `newAPIStub` (the httptest double that records request URIs and Authorization headers), the `Describe("cloudBuildLogFetcher", ...)` block, and the two env-var precedents: the `os.Setenv` + `DeferCleanup` restore at the top, and the spec "surfaces a credential problem as a runtime error, not a boot failure".
- `pkg/export_test.go` — the `*ForTest` seam pattern (a thin exported wrapper calling an unexported constructor/function). `NewCloudBuildLogFetcherForTest` is the one this change needs; add the new seam beside it in the same style.
- `pkg/config.go` — `resolveEnvVar` / `ResolvedGitHubToken`: the repo's `os.Getenv` idiom for reading configuration from the environment.
- `pkg/config_test.go` — how this repo sets and clears environment variables inside a Ginkgo spec so the suite stays hermetic.
- `pkg/steps_build_fix.go` — `NewBuildFixStep` is the only caller of `NewCloudBuildLogFetcher()`. Read it to confirm this change needs no edit there.
- `pkg/factory/factory.go` — `CreateAgentProvider`, which composes `NewBuildFixStep` for the `build-fix` route. Confirm this change needs no edit there either.
- `README.md` — the `## Build-fix tasks` section (it states the fetch uses an ADC bearer token) and the `## Configuration` table (which lists the env vars an operator must set).
- `docs/architecture.md` — the `build-fix` paragraph that names the ADC bearer token.
- `CHANGELOG.md` — the `## Unreleased` section placement rule (below the preamble, above the newest `## vX.Y.Z`).

Facts about the deployed environment (verified upstream; the watcher repo is NOT mounted in this container, so do not look for it):

- The fleet passes the GCP service-account key as an **environment variable** named `GCP_KEY_JSON`, never as a file. The sibling `google-cloud-build-watcher` declares the same env var name and currently writes the value to a temp file only because the `gcloud` CLI needs a file path. This fetcher calls the Cloud Build REST API directly with `net/http`, so it needs no temp file.
- `google.DefaultTokenSource` reads `GOOGLE_APPLICATION_CREDENTIALS`, then a well-known file, then the **GCE metadata server**. In the pod none of the first two exist, so the current code silently authenticates as the node service account.
- The intended runtime identity holds exactly two read grants: `roles/cloudbuild.builds.viewer` and `roles/storage.objectViewer` on the Cloud Build logs bucket. Never an editor or owner role.

Fleet precedent for building a token source from a service-account key JSON (a sibling repo, not mounted — inline it here so the shape is unambiguous):

```go
creds, err := google.CredentialsFromJSON(ctx, []byte(credentialsJSON), "<scope>")
if err != nil {
    return nil, errors.Wrap(ctx, err, "parse service account credentials")
}
return creds.TokenSource, nil
```

Coding plugin docs at `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `go-error-wrapping-guide.md` (bborbe/errors wrapping)
- `go-testing-guide.md` (Ginkgo v2 / Gomega, external test packages)
- `go-glog-guide.md` (`V(n)`-gated logging)
- `go-doc-best-practices.md` (exported symbols need doc comments)
- `changelog-guide.md` (the `## Unreleased` entry style)
</context>

<requirements>
1. **Add the credential resolver to `pkg/build_log_fetcher.go`.** Declare the env-var name as a package constant and a small, directly-testable function that owns the branch:

   ```go
   // gcpKeyJSONEnvVar is the environment variable carrying the GCP
   // service-account key JSON. The fleet passes the key as an env var, never as
   // a file, so no temp file and no GOOGLE_APPLICATION_CREDENTIALS dance.
   const gcpKeyJSONEnvVar = "GCP_KEY_JSON"

   // credentialsFromEnv returns the token source for the Cloud Build and Cloud
   // Storage calls: built from the service-account key in gcpKeyJSONEnvVar when
   // it is set, otherwise resolved through the Application Default Credentials
   // chain (the documented local/dev path).
   func credentialsFromEnv(ctx context.Context) (oauth2.TokenSource, error)
   ```

   Contract — the branch order is the whole point of the change:
   - When `os.Getenv(gcpKeyJSONEnvVar)` is non-empty, build the credentials with exactly `google.CredentialsFromJSON(ctx, []byte(keyJSON), cloudBuildScope, storageReadScope)` (`golang.org/x/oauth2/google`) and return `creds.TokenSource`. Do not call `google.DefaultTokenSource` anywhere on this path — reaching the metadata server while the env var is set is the bug being fixed.
   - When the env var is empty, keep the existing `google.DefaultTokenSource(ctx, cloudBuildScope, storageReadScope)` call and the existing wrap message `"resolve google application default credentials"` (an existing spec asserts on that substring).
   - On a parse failure return a wrapped error whose message names the environment variable **literally** — the constant `gcpKeyJSONEnvVar`, e.g. `errors.Wrapf(ctx, err, "parse %s service-account key", gcpKeyJSONEnvVar)` — so the failure is attributable. The wrap message and any log line MUST NOT contain the key material itself.
   - Both scopes stay exactly `cloudBuildScope` + `storageReadScope`. No new scope, and never a write-capable one.
   - The function performs no I/O beyond reading the environment variable: no file read, no temp file, no `os/exec`, no network call at construction time (`CredentialsFromJSON` parses and returns a lazily-refreshing token source).
   - Emit one `glog.V(2).Infof` line naming which source was selected (`GCP_KEY_JSON` vs. Application Default Credentials) and never the key material. The deployed branch is otherwise unobservable from outside the pod: if the fleet's variable is not literally `GCP_KEY_JSON`, the code silently takes the ADC path and the broad node identity survives with every test green — this log line is the only way to tell which happened.

2. **Route `accessToken` through it.** In `cloudBuildLogFetcher.accessToken`, replace the inline `google.DefaultTokenSource` block with a call to `credentialsFromEnv` when `f.tokenSource == nil`. Keep the injected seam exactly as it is: a non-nil `f.tokenSource` is used directly and `credentialsFromEnv` is not consulted (tests inject `oauth2.StaticTokenSource` and must stay hermetic). Keep minting the token with `tokenSource.Token()` and the existing wrap message `"mint cloud build access token"`. Update `accessToken`'s doc comment to say the source is "injected, `GCP_KEY_JSON`, or ADC" and update `NewCloudBuildLogFetcher`'s / `newCloudBuildLogFetcher`'s doc comments where they say "Application Default Credentials" so they describe the new order.

   Resolution stays **lazy** (inside `FetchLog`, per call): do not read the environment variable in the constructors, and do not change any signature. `NewCloudBuildLogFetcher()`, `newCloudBuildLogFetcher(...)`, `NewBuildFixStep(...)` and `factory.CreateAgentProvider(...)` keep their current shapes, so `pkg/steps_build_fix.go`, `pkg/factory/factory.go`, `main.go` and `cmd/run-task/main.go` need no edit — both entry points inherit the fix through the existing wiring.

3. **Test seam.** Add to `pkg/export_test.go`, in that file's existing style and placement:

   ```go
   // CredentialsFromEnvForTest exposes credentialsFromEnv so pkg_test can assert
   // the env-var / ADC branch selection without a real service-account key.
   func CredentialsFromEnvForTest(ctx context.Context) (oauth2.TokenSource, error) {
       return credentialsFromEnv(ctx)
   }
   ```

4. **Branch-selection specs in `pkg/build_log_fetcher_test.go`.** Add a `Describe("credentialsFromEnv", ...)` block (keep that exact name — the verification block focuses on it) in the existing external `pkg_test` package, using the file's existing Ginkgo/Gomega style. Every spec must set or unset **both** `GCP_KEY_JSON` and `GOOGLE_APPLICATION_CREDENTIALS` explicitly and restore them with `DeferCleanup` (the file's existing precedent), so no spec depends on the ambient environment. Cover:
   - **malformed key cannot silently fall back (the bug being fixed):** set `GCP_KEY_JSON` to malformed JSON (e.g. `not-json`) **and** `GOOGLE_APPLICATION_CREDENTIALS` to `/nonexistent/adc.json` — the path that makes the ADC branch fail with its own distinctive message. Assert: an error occurs; the returned token source is nil; the error message contains `GCP_KEY_JSON`; and the error message does **not** contain `application default credentials`. That last assertion is the regression lock — if the code ever falls back to ADC while the env var is set, the message changes and this spec fails.
   - **usable key is used:** set `GCP_KEY_JSON` to a well-formed credentials JSON that needs no private key, and unset `GOOGLE_APPLICATION_CREDENTIALS`. Use the `authorized_user` shape (the gcloud ADC shape) with dummy values, e.g. `{"type":"authorized_user","client_id":"test-client","refresh_token":"test-refresh"}`. Assert: no error, and a non-nil token source. Do **not** use a `service_account`-shaped fixture and do not embed a private key anywhere — a `"type": "service_account"` literal is what the repo's secret scanners look for.
   - **env var unset still selects ADC:** unset `GCP_KEY_JSON` and set `GOOGLE_APPLICATION_CREDENTIALS` to `/nonexistent/adc.json`. Assert: an error occurs and its message contains `application default credentials` — proving the ADC branch is still the one taken when the env var is absent.

5. **Full-path spec through the production constructor.** In the existing `Describe("cloudBuildLogFetcher", ...)` block, add a spec that drives the whole fetch with no injected token source: build the fetcher with `NewCloudBuildLogFetcherForTest(build.server.URL, storage.server.URL, nil)` (a `nil` token source is the documented "resolve from the environment" case) while `GCP_KEY_JSON` holds malformed JSON. Assert that `FetchLog` returns an error naming `GCP_KEY_JSON`, and that both httptest stubs recorded **zero** requests — the credential failure must happen before any HTTP call. This is the spec that proves the wiring, not just the helper.

6. **Make the existing ADC spec hermetic.** The spec "surfaces a credential problem as a runtime error, not a boot failure" currently sets `GOOGLE_APPLICATION_CREDENTIALS` only. Add an explicit `os.Unsetenv("GCP_KEY_JSON")` (with the same `DeferCleanup` restore) so it can never be perturbed by an ambient `GCP_KEY_JSON`, and extend its assertion so the failure stays attributable to the ADC chain.

7. **Documentation.**
   - `CHANGELOG.md`: add one bullet under `## Unreleased` (the section already exists — add to it, do not create a second one, and do not touch any released section):
     `- fix: resolve the build-fix Cloud Build token from the GCP_KEY_JSON service-account key when it is set and fall back to Application Default Credentials only when it is unset — the pod no longer authenticates as the broad node service account`
   - `README.md`: add a `GCP_KEY_JSON` row to the `## Configuration` table (the GCP service-account key JSON used to read Cloud Build logs and log objects for `build-fix` tasks; when unset, Application Default Credentials are used) and correct the `## Build-fix tasks` sentence that claims the fetch always uses an ADC bearer token so it states the env-var-first order. Leave the security-review content untouched.
   - `docs/architecture.md`: correct the single `build-fix` clause that says the fetcher authenticates with "an ADC bearer token" so it names the env-var-first order. Nothing else in that file changes.

8. **Self-check.** Before finishing: re-run every command in `<verification>` and confirm each passes, then walk each numbered requirement above against the change. Confirm specifically that (a) no code path reaches `google.DefaultTokenSource` while `GCP_KEY_JSON` is set, (b) no key material appears in an error message, a log line, or a test fixture, (c) no file outside the list in `<constraints>` was modified, and (d) no signature changed, so no call site needed updating.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; `make precommit` must exit 0.
- **Scope: `pkg/build_log_fetcher.go`, `pkg/build_log_fetcher_test.go`, `pkg/export_test.go`, `CHANGELOG.md`, `README.md`, `docs/architecture.md`.** Nothing else. In particular: no deploy config, no Helm/chart values, no `.dark-factory.yaml`, no `Makefile*`, no `go.mod` change, no new dependency.
- `GCP_KEY_JSON` is the ONLY new environment variable, and it already exists in the fleet — do NOT add a second env var, a config field, a flag, or any other knob (no "credential source" switch, no key-file path option, no timeout knob).
- **Read-only identity is load-bearing.** The runtime identity holds exactly two grants, `roles/cloudbuild.builds.viewer` and `roles/storage.objectViewer` on the Cloud Build logs bucket — never an editor or owner role. The OAuth scope list stays exactly `cloud-platform` (the only scope the Cloud Build API accepts) plus `devstorage.read_only`; no `*_write` / `full_control` scope, and not `cloud-platform.read-only` (the Cloud Build API rejects it).
- **Never write the key to disk and never log it.** The service-account key comes from the environment and is used in memory only — no temp file, no `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE`, no `GOOGLE_APPLICATION_CREDENTIALS` write. Key material must never appear in an error message, a `glog` line, a test fixture, or a commit.
- Do NOT shell out to `gcloud` (or any other CLI) — no `os/exec` anywhere in this code path.
- Keep `google.CredentialsFromJSON` (the fleet precedent). `SA1019` (deprecated-API) is excluded repo-wide in `.golangci.yml`'s `exclusions.rules`, so it passes `make precommit`; do not swap it for `CredentialsFromJSONWithType` — and note in the CHANGELOG bullet that accepting any credential type present in the env var is a deliberate tradeoff, not an oversight.
- Keep the injected-token-source seam (`newCloudBuildLogFetcher`'s `tokenSource` parameter) intact — every existing spec injects a static token and must stay hermetic. No test may make a live network call; every HTTP interaction goes through `httptest`.
- Do NOT change any function signature: `NewCloudBuildLogFetcher()`, `newCloudBuildLogFetcher(...)`, `NewBuildFixStep(...)` and `factory.CreateAgentProvider(...)` keep their current shapes. `pkg/steps_build_fix.go`, `pkg/factory/factory.go`, `main.go` and `cmd/run-task/main.go` must compile unchanged — if one of them needs an edit, the design has drifted.
- The `build-fix` boundary is unchanged: no Claude runner, no clone, no code modification, no branch push, no PR, no second task, and never an advance to `done`.
- Do NOT touch `pkg/steps_build_fix.go`, `pkg/verdict.go`, `pkg/factory/factory.go`, or the security agent's phase/tool wiring.
- Error creation and wrapping go through `github.com/bborbe/errors` (`errors.Wrap` / `errors.Wrapf` / `errors.Errorf`) — never `fmt.Errorf` and never a bare `return err`.
- Logging is `glog` with `V(n)`-gated `Info` — never `fmt.Print*`.
- Exported types, functions, and interfaces carry doc comments; `revive`, `funlen` (80 lines / 50 statements), `gocyclo` and `nestif` are enforced by `make precommit`.
- `context.Context` is threaded through every I/O call; no `context.Background()` outside `main` and tests.
- Tests use Ginkgo v2 / Gomega in the external `pkg_test` package; env mutations are restored so the suite stays hermetic and order-independent.
- New code has good test coverage (target >= 80%); every added branch is exercised.
</constraints>

<verification>
- `go test -mod=mod -count=1 ./pkg/ -run TestSuite -args -ginkgo.focus='credentialsFromEnv' -ginkgo.fail-on-empty` — exits 0 and reports a non-zero number of specs run (`-ginkgo.fail-on-empty` makes a mis-named focus fail instead of falsely passing). Note this focus matches only the new `Describe("credentialsFromEnv", ...)` block; the full-path spec lives under `Describe("cloudBuildLogFetcher", ...)` and is therefore covered by `make precommit`'s full-suite run, not by this command.
- `make precommit` — exits 0 (fmt, generate, test, lint, vet, vuln, license).
- `grep -n 'GCP_KEY_JSON' pkg/build_log_fetcher.go` — prints the env-var constant (the variable is named in production code, not only in the test).
- `grep -c 'CredentialsFromJSON' pkg/build_log_fetcher.go` — prints a non-zero count (the credential construction is present in the fetcher).
- `grep -n 'GCP_KEY_JSON' README.md CHANGELOG.md` — prints a line in each file (the configuration table and the changelog bullet are present).
- `! grep -q 'CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE\|CreateTemp\|os/exec' pkg/build_log_fetcher.go` — no temp-file credential dance and no subprocess in the fetcher.
- `! grep -q 'fmt.Errorf' pkg/build_log_fetcher.go` — errors go through `github.com/bborbe/errors`.
- `grep -n 'cloud-platform\|devstorage.read_only' pkg/build_log_fetcher.go` — both scopes still present, unchanged.
</verification>
