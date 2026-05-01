#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
SHATTER_BIN=${SHATTER_BIN:-${HOME}/project/shatter/target/release/shatter}

cd "${REPO_ROOT}"

test -x "${SHATTER_BIN}"
test -f .shatter/config.yaml
test -f shatter.config.json
test -x scripts/shatter-full-scan.sh

"${SHATTER_BIN}" scan --project-dir "${REPO_ROOT}" --dry-run --all --language go . >/tmp/zolem-shatter-dry-run.txt

grep -q 'cmd/zolem/local_admin.go' /tmp/zolem-shatter-dry-run.txt
grep -q 'internal/provider/openai/handler.go' /tmp/zolem-shatter-dry-run.txt
if grep -q '_test.go' /tmp/zolem-shatter-dry-run.txt; then
  printf 'dry run included test files unexpectedly\n' >&2
  exit 1
fi

printf 'shatter setup: ok\n'
