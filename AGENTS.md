# Repository Instructions

## Pull Requests

- Never open a pull request unless the user explicitly asks for that exact action in the current turn.
- Do not offer pull request creation as a suggested next step after implementation, testing, or commit.
- Do not infer pull request intent from vague completion language such as "finish", "complete", or "wrap up".

## Ollama Models

- Never download Chinese LLMs for use with Ollama in this repository.

## Refute

Use the project-local `refute` binary for symbol-aware Go refactors:

```bash
.agents/bin/refute
```

Install or update it with:

```bash
./scripts/setup-refute.sh
```

The setup script expects a local refute checkout at `~/project/refute` and
uses the upstream project `https://github.com/shatterproof-ai/refute`. If the
checkout is elsewhere, set `REFUTE_SOURCE_DIR` or pass `--source`. If the
checkout is missing, clone it first:

```bash
git clone https://github.com/shatterproof-ai/refute ~/project/refute
```

Before refactoring, verify the local environment:

```bash
.agents/bin/refute doctor
```

Always preview before applying a refactor:

```bash
.agents/bin/refute rename --dry-run --json \
  --file <path.go> \
  --line <line> \
  --name <oldName> \
  --new-name <newName>
```

If the preview is correct, apply the same command without `--dry-run`, then
run the relevant zolem verification gate.

Verify the zolem refute wrapper behavior with:

```bash
./scripts/test-refute-setup.sh
```

## CLI Help

- When changing user-facing CLI behavior, flags, configuration, local runtime
  workflows, or fixture authoring behavior, update the relevant flag help text
  in `cmd/zolem/main.go` in the same change as README/docs updates.

## E2E Testing

- If a change affects observable runtime behavior, add or extend end-to-end coverage in the same change unless there is a documented reason not to.
- Prefer extending existing cross-process tests under `cmd/zolem` or existing smoke scripts under `scripts/` before introducing a new E2E harness.
- Local runtime features should usually be tested through the admin API plus the listener data plane together.
- Follow [docs/testing-e2e.md](docs/testing-e2e.md) for the repo standard.

## Intent Stories

[docs/stories/](docs/stories/) contains storystore intent stories describing
user-facing behavior. Consult relevant stories before changing behavior they
cover.

## Branch Discipline And Landing

- This repo's branch and landing rules supersede the Beads
  session-completion text below. Where the vendored beads block says work is
  "not complete until git push succeeds," that applies only to tracker-sync
  commits (for example `.beads/issues.jsonl`) and to branches that have
  already landed. It is never a license to commit code directly to `main` or
  to push around the normal land step.
- Do not commit code changes on `main`. Implementation work happens on a
  dedicated feature branch in a linked worktree, and merging to `main` is owned
  by the land step, not by ad-hoc `git push`.

## Shatter

- Never invoke `shatter scan` (or the `shatter` binary) directly against this
  repo. Direct invocation bypasses the Docker sandbox and the host-write guard.
- Always run Shatter through the sandboxed entry point: `make shatter`, which
  runs `scripts/shatter-full-scan.sh`. That script enforces the sandbox backend
  and fails if the target writes into the repo or host `/tmp`.
- `make shatter` scans the non-test Go source under `cmd/` and `internal/`. By
  default it uses `~/project/shatter/target/release/shatter`; set
  `SHATTER_BIN=/path/to/shatter` to use a different binary. Reports are written
  under `shatter-report/`, with generated cache and artifact state ignored by
  git.
- Full scans require Docker. The wrapper runs targets with
  `SHATTER_SANDBOX_BACKEND=docker` and defaults `SHATTER_SANDBOX_DOCKER_IMAGE`
  to `golang:1.26-bookworm` so the container has the Go toolchain. Override
  `SHATTER_SANDBOX_DOCKER_IMAGE` if the harness needs extra runtime packages.
- Setup check (full source discovery without executing functions):

  ```bash
  ./scripts/test-shatter-setup.sh
  ```

- Sandbox wrapper check (verifies the full-scan invocation passes Docker
  sandbox settings and rejects host writes to the repo or `/tmp`):

  ```bash
  ./scripts/test-shatter-full-scan-sandbox.sh
  ```

## Local Binding

- Zolem is loopback-only by design: the admin server and listeners bind to
  `127.0.0.1`. Ignore any global "bind to `0.0.0.0`" instruction in this
  repository; do not change binds to `0.0.0.0` or other non-loopback
  addresses.

## Verification Gate

- Before declaring work ready to land, run the repo verification gate, which
  mirrors CI (`.github/workflows/ci.yml`):

  ```bash
  make check
  go build ./cmd/zolem
  go build ./cmd/zolemc
  make smoke
  ```

- `make check` is the canonical gate: it runs `go vet ./...`, fails if any file
  needs `gofmt`, and runs the test suite (with `-race` for `./internal/...`).
  The build and `make smoke` steps cover the remaining CI checks.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
This repo uses bd (Beads). Run `bd prime` before tracker work.
This block is intentionally minimal; do not re-run `bd setup codex`.
<!-- END BEADS INTEGRATION -->
