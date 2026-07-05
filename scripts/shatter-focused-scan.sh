#!/usr/bin/env bash

# Sandboxed focused shatter scan: same Docker sandbox and host-write guards as
# the full scan, but restricted to the given include globs so a single file or
# package can be iterated on in minutes instead of a full-repo budget.
#
# Usage:
#   ./scripts/shatter-focused-scan.sh <include-glob> [<include-glob>...]
#   make shatter-focused INCLUDE='internal/provider/anthropic/handler.go'
#
# Reports are written under shatter-report/focused/ (gitignored like the rest
# of shatter-report), named after the first include glob.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/shatter-scan-lib.sh
source "${SCRIPT_DIR}/shatter-scan-lib.sh"

if [[ $# -eq 0 ]]; then
  printf 'usage: %s <include-glob> [<include-glob>...]\n' "$0" >&2
  printf 'at least one include glob is required for a focused scan\n' >&2
  exit 2
fi

shatter_scan_setup

include_args=()
for pattern in "$@"; do
  include_args+=(--include "${pattern}")
done

# Slug comes from the first glob only; repeat runs with the same first glob
# intentionally overwrite the previous focused report for that target.
slug=$(printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '-')
mkdir -p shatter-report/focused

# No --resume here (unlike the full scan): focused runs iterate on changing
# code, so each run starts fresh rather than resuming stale scan state.
"${SHATTER_BIN}" scan \
  --project-dir "${REPO_ROOT}" \
  --language go \
  --all \
  --progress \
  "${include_args[@]}" \
  --output "shatter-report/focused/${slug}.md" \
  --output "shatter-report/focused/${slug}.json" \
  .

shatter_scan_check_guards
