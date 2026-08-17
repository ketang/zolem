#!/usr/bin/env bash
#
# Project verifier for the bento land-work skill.
#
# Runs zolem's verification gate — the same four steps AGENTS.md
# ("Verification Gate") and .github/workflows/ci.yml require — against the
# candidate worktree land-work is about to land, then reports the result as a
# single JSON object on the final stdout line.
#
# The JSON contract is defined by land-work's project-verifier reference:
#   {"schema_version":1,"status":"passed","selected_checks":[{"name":..., "status":...}]}
# A "failed" status, or any check reported as failed, stops the landing.
#
# Human-readable output from the underlying commands goes to stderr so it stays
# visible in logs without corrupting the machine-readable final line.

set -uo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)

cd "${REPO_ROOT}"

names=()
statuses=()
overall="passed"

run_check() {
  local name="$1"
  shift

  printf '==> %s\n' "${name}" >&2
  if "$@" >&2; then
    names+=("${name}")
    statuses+=("passed")
  else
    names+=("${name}")
    statuses+=("failed")
    overall="failed"
  fi
}

run_check "make check" make check
run_check "go build ./cmd/zolem" go build ./cmd/zolem
run_check "go build ./cmd/zolemc" go build ./cmd/zolemc
run_check "smoke" ./scripts/smoke.sh

# Emit the machine-readable result as the final stdout line.
{
  printf '{"schema_version":1,"status":"%s","selected_checks":[' "${overall}"
  for i in "${!names[@]}"; do
    [ "${i}" -gt 0 ] && printf ','
    printf '{"name":"%s","status":"%s"}' "${names[$i]}" "${statuses[$i]}"
  done
  printf ']}\n'
}

[ "${overall}" = "passed" ] || exit 1
