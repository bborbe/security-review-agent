# security-review-agent

Autonomous **security review agent**. It consumes `security-review` tasks from the
agent-maintenance fleet: it checks out the target PR at its head, runs the Claude
Code security review (`/coding:pr-review --security`) against the diff, and writes
the findings plus the binary verdict (`approve` / `request-changes`) into the task's
`## Review` section. It **never posts to GitHub** — the run ends at the vault
write-back; the verdict is consumed downstream by a human or another agent.

It also consumes `build-fix` tasks (see [Build-fix tasks](#build-fix-tasks)).

Part of the `bborbe` agent-maintenance fleet: the shared library lives in
[`bborbe/maintainer`](https://github.com/bborbe/maintainer) (imported as
`github.com/bborbe/maintainer`), the Helm chart ships there too, and the
published image is `docker.io/bborbe/security-review-agent`. Adapted from the
former `agent/pr-reviewer` (pr-reviewer lineage).

## How it works

1. Consume a `security-review` task (`task_type: security-review`,
   `assignee: security-reviewer-agent`) carrying a PR URL.
2. Fetch PR metadata (source + target branch) via the platform REST API.
3. Clone the repo at the PR head into a scratch dir (throwaway, cleaned up after the run).
4. Run the Claude Code security review (`/coding:pr-review --security`) inside the
   `claude-yolo` container against the diff.
5. Parse the JSON verdict (`approve` / `request-changes`) from the review output.
6. Write the findings + verdict into the task's `## Review` section (vault-first).
   The run ends there — nothing is posted to the PR.

## Build-fix tasks

`task_type: build-fix` — emitted by the google-cloud-build-watcher for every
failed master build (`assignee: build-fixer-agent`). The agent reads the failed
build's log over the Cloud Build REST API, extracts the failure cause, and writes
it into the task's `## Failure Analysis` section, then clears the `assignee` so a
human picks it up. It routes to `human_review` — never to `done`.

**Analysis only.** The agent never modifies code, never pushes a branch, never
opens a PR, and never files a second task; it enriches the task it was spawned on
and stops. There is no Claude runner and no clone on this path.

How the log is fetched — two hops, both authenticated with a bearer token minted
from the `GCP_KEY_JSON` service-account key when that env var is set, falling
back to Application Default Credentials when it is unset (scope `cloud-platform`
— the only scope Cloud Build accepts — plus `devstorage.read_only`):

1. `GET /v1/projects/{project}/locations/{region}/builds/{build_id}` on the
   Cloud Build REST API, to confirm the build has finished and read its
   `startTime`.
2. `POST /v2/entries:list` on the Cloud Logging API with
   `resourceNames: ["projects/{project}"]`, the quota-project header
   `X-Goog-User-Project: {project}`, and the filter
   `timestamp>="{start}" AND resource.type="build" AND resource.labels.build_id="{build_id}"`,
   following `nextPageToken` until it is absent. The entries' `textPayload`
   (falling back to `jsonPayload.message`) are joined by newlines in returned
   order.

The `timestamp>=` bound is required, not an optimization: without a bounded time
range the Logging API can answer with an empty entry list alongside a valid
continuation token, which is indistinguishable from an empty log and would
silently produce an empty failure analysis.

The build ID is validated as a UUID before it is interpolated into either the URL
path or the filter. Read-only-ness is enforced by the runtime identity's IAM
grants — `roles/cloudbuild.builds.viewer` for the build, `logging.logEntries.list`
on the project (carried by `roles/logging.viewer`) for the log entries, and
`serviceusage.services.use` for the quota-project header — never by an
editor/owner role. A build still `QUEUED`/`WORKING` fails with a status-naming
error so an unfinished build is distinguishable from a fetch failure.

`project` and `region` come from configuration (`CLOUD_BUILD_PROJECT` /
`CLOUD_BUILD_REGION`), not from the task body.

## Run modes

| Mode | Entry | Use |
|---|---|---|
| Kubernetes Job | `main.go` (`/main` in the image) | Env-driven; spawned by the agent-task-executor from a Kafka task. This is how it runs in production. |
| Local CLI | `cmd/run-task` | Reads a task file (`--task-file`), runs `--phase`, writes result back to the same file. |
| Local review | `cmd/cli` | Legacy single-shot CLI (pr-reviewer lineage); takes a PR URL positional arg. Not adapted — kept for historical local runs. |

```bash
go run ./cmd/run-task --task-file=/path/to/task.md --phase=execution
```

## Configuration

Env-driven (Kubernetes) — key variables:

| Var | Purpose |
|---|---|
| `APP_ID` / `INSTALLATION_ID` | GitHub App identity for cloning the target repo |
| `PEM_KEY` | GitHub App private key (mounted from a Secret) |
| `REPO_ALLOWLIST` | Repos the agent may clone (e.g. `github.com/bborbe/*,!github.com/bborbe/go-skeleton`) |
| `REVIEW_MODE` | Review depth (e.g. `selector`) |
| `REVIEW_MAX_DURATION` | Soft time budget per Claude phase run (default `25m`, floor `60s`); an overrun routes the phase to `human_review` with a budget-naming message and salvages any streamed partial into the task's `## Salvage` section (never a retry, never posted to GitHub); keep below the K8s Job `ActiveDeadlineSeconds` |
| `CLOUD_BUILD_PROJECT` | GCP project holding the Cloud Build history read by `build-fix` tasks (default `smedia-brogrammers`) |
| `CLOUD_BUILD_REGION` | GCP region holding the Cloud Build history read by `build-fix` tasks (default `europe-west3`) |
| `GCP_KEY_JSON` | GCP service-account key JSON used to read Cloud Build metadata and log entries for `build-fix` tasks; when unset, Application Default Credentials are used |

`REPO_ALLOWLIST` is honored — a PR outside the allowlist is refused, never
reviewed. Clones are throwaway scratch dirs at the PR head; there is no code
path that posts to a PR.

## Verdict contract

The Claude Code review must emit a JSON block:

```json
{"verdict": "approve|request-changes", "reason": "<one-liner>"}
```

Fallback: a heuristic section-header scan (`## Must Fix`, `## Blocking`).
Horizontal rules (`---`) are not treated as must-fix content.

## Smoke-test PR

**https://github.com/bborbe/maintainer/pull/2** — `test: delete-this-pr-never`.
Permanent test fixture (trivial diff). Use it for any local or k8s smoke test.
**Do not close, do not merge.**

## Layout

```
.                    lib imported from github.com/bborbe/maintainer
├── main.go          Kubernetes Job entry (env-driven; /main in the image)
├── cmd/
│   ├── run-task/    local CLI (task-file driven)
│   ├── cli/         legacy PR URL driven local review CLI (pr-reviewer lineage)
│   └── mint-iat/    GitHub App installation-token smoke tool
├── pkg/             URL parse, config, git clone, GitHub/Bitbucket clients,
│                    App auth, review runner, verdict parser
├── docs/            architecture.md · github-app-setup.md · pr-post-back.md
│                    · dod.md · releasing-security-review-agent.md
└── helm chart + shared lib live in bborbe/maintainer
```

See [`docs/architecture.md`](docs/architecture.md) for the three-phase design
(planning → execution → ai_review) and the verdict rubric, and
[`docs/github-app-setup.md`](docs/github-app-setup.md) for the App auth flow.

## Build

```bash
make precommit          # fmt, generate, test, lint, vet, vuln, license
VERSION=vX.Y.Z make buca # build + push docker.io/bborbe/security-review-agent:vX.Y.Z
```
