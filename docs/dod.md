# Definition of Done

After completing your implementation, review your own changes against each criterion below. These are quality checks you perform by inspecting your work — not commands to run (linting and tests already ran via `testCommand`). Report any unmet criterion as a blocker.

## Code Quality

- Exported types, functions, and interfaces have doc comments
- Error handling uses `github.com/bborbe/errors` with context wrapping — no `fmt.Errorf`, no bare `return err`
- No debug output (`fmt.Print*`, `println`) — use `glog` with `V(n)`-gated `Info` lines
- Factory functions are pure composition — no conditionals, no I/O, no `context.Background()`
- Follow the Interface → Constructor → Struct → Method pattern
- `context.Context` is threaded through every IO call; no `context.Background()` outside `main`/tests

## No GitHub Posting (load-bearing — this agent never writes to other people's PRs)

- The security-review agent wires a **nil poster and a nil verifier** — there is no code path that posts a review, comment, or approval to any GitHub PR
- The run stops at the `## Review` write-back: findings + binary verdict (`approve` / `request-changes`) are written to the task's `## Review` section in the vault, and nothing is published to the PR
- Clones are throwaway scratch dirs at the PR head, cleaned up after the run; no writes back to the cloned repo
- `REPO_ALLOWLIST` is honored — a PR outside the allowlist is refused, never reviewed
- No new code path may add a poster — a change that reintroduces GitHub posting fails this gate

## Testing

- New code has good test coverage (target >= 80%)
- Changes to existing code have tests covering at least the changed behavior
- Tests use Ginkgo v2 / Gomega with Counterfeiter mocks (`mocks/` dir)
- LLM-dependent steps are tested with a fake runner returning canned verdicts — no live Claude calls in tests
- Verdict-parsing changes update the verdict tests (`pkg/verdict.go` / `pkg/prompts/`)

## Build

- `make precommit` passes (fmt, generate, test, lint, vet, vuln, license)
- No `exclude` or `replace` directives in `go.mod` (break remote install of the shared `github.com/bborbe/maintainer` lib)
- The image still builds: `VERSION=vX.Y.Z make buca` produces `docker.io/bborbe/security-review-agent:vX.Y.Z` (only when a runtime change warrants a manual check)

## Documentation

- README.md is updated if the change affects run modes, configuration, or review/verdict behavior
- CHANGELOG.md has an entry under `## Unreleased`. If that section does not exist yet, create it **below** the preamble block and **above** the newest `## vX.Y.Z` section — never between the `# Changelog` title and the preamble. The final order is always: `# Changelog` → preamble → `## Unreleased` → `## vX.Y.Z` (newest first)
