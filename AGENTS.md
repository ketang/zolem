# Repository Instructions

## Pull Requests

- Never open a pull request unless the user explicitly asks for that exact action in the current turn.
- Do not offer pull request creation as a suggested next step after implementation, testing, or commit.
- Do not infer pull request intent from vague completion language such as "finish", "complete", or "wrap up".

## Ollama Models

- Never download Chinese LLMs for use with Ollama in this repository.

## E2E Testing

- If a change affects observable runtime behavior, add or extend end-to-end coverage in the same change unless there is a documented reason not to.
- Prefer extending existing cross-process tests under `cmd/zolem` or existing smoke scripts under `scripts/` before introducing a new E2E harness.
- Local runtime features should usually be tested through the admin API plus the listener data plane together.
- Follow [docs/testing-e2e.md](/home/ketan/.codex/memories/worktrees/zolem-high-fidelity-errors/docs/testing-e2e.md) for the repo standard.
