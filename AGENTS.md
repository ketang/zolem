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

Install or update it from the local checkout at `~/project/refute`:

```bash
./scripts/install-refute.sh
```

The upstream project is https://github.com/shatterproof-ai/refute. If the local
checkout is somewhere else, set `REFUTE_REPO=/path/to/refute` or pass
`--refute-repo /path/to/refute`. If the checkout is missing, clone that upstream
repository before installing.

Before refactoring:

```bash
.agents/bin/refute doctor
```

Always preview first:

```bash
.agents/bin/refute rename --dry-run --json \
  --file <path.go> \
  --line <line> \
  --name <oldName> \
  --new-name <newName>
```

If the preview is correct, apply with the same command without `--dry-run`, then
run the relevant zolem verification gate.

## CLI Help

- When changing user-facing CLI behavior, flags, configuration, local runtime
  workflows, or fixture authoring behavior, update the relevant flag help text
  in `cmd/zolem/main.go` in the same change as README/docs updates.

## E2E Testing

- If a change affects observable runtime behavior, add or extend end-to-end coverage in the same change unless there is a documented reason not to.
- Prefer extending existing cross-process tests under `cmd/zolem` or existing smoke scripts under `scripts/` before introducing a new E2E harness.
- Local runtime features should usually be tested through the admin API plus the listener data plane together.
- Follow [docs/testing-e2e.md](/home/ketan/.codex/memories/worktrees/zolem-high-fidelity-errors/docs/testing-e2e.md) for the repo standard.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
