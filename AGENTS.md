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

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
This repo uses bd (Beads). Run `bd prime` before tracker work.
This block is intentionally minimal; do not re-run `bd setup codex`.
<!-- END BEADS INTEGRATION -->
