# Beads Context

This repo uses bd (Beads) for task tracking. Run `bd prime --export` when you need the complete default reference.

## Session Close

Before calling work done:
1. `git status`
2. `git add <files>`
3. `git commit -m "..."`
4. `git push`

Keep tracker state current, but do not close tracker work until the branch is verified landed on the integration branch.

## Core Commands

- `bd ready` - find unblocked work
- `bd show <id>` - inspect scope, dependencies, and acceptance
- `bd create --title="..." --description="..." --type=task` - file work
- `bd update <id> --claim` - claim active work
- `bd close <id>` - close only after verified landing
- `bd dep add <issue> <depends-on>` - record dependencies

Use bd for task tracking; do not use TodoWrite, TaskCreate, or markdown TODO lists. Use `bd remember "insight"` for persistent project memory; do not create MEMORY.md files. If persistent memories are needed after this override, run `bd memories`.
