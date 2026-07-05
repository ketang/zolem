#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
# shellcheck source=scripts/test-shatter-scan-fixture.sh
source "${SCRIPT_DIR}/test-shatter-scan-fixture.sh"

# A focused run must require at least one include glob.
if SHATTER_BIN="${FAKE_SHATTER}" \
  ZOLEM_SHATTER_ENV_LOG="${ENV_LOG}" \
  "${REPO_ROOT}/scripts/shatter-focused-scan.sh" 2>"${TMP_ROOT}/noargs-stderr.log"; then
  printf 'expected focused scan to fail without include globs\n' >&2
  exit 1
fi
grep -qi 'include' "${TMP_ROOT}/noargs-stderr.log"

SHATTER_BIN="${FAKE_SHATTER}" \
ZOLEM_SHATTER_ENV_LOG="${ENV_LOG}" \
"${REPO_ROOT}/scripts/shatter-focused-scan.sh" \
  'internal/provider/anthropic/handler.go' 'internal/provider/openai/*.go'

grep -qx 'backend=docker' "${ENV_LOG}"
grep -qx 'image=golang:1.26-bookworm' "${ENV_LOG}"
grep -Eq '^repo_guard=.+' "${ENV_LOG}"
grep -Eq '^tmp_guard=/tmp/.+' "${ENV_LOG}"
grep -Eq -- '^args=.*--include internal/provider/anthropic/handler\.go .*--include internal/provider/openai/\*\.go' "${ENV_LOG}"
grep -Eq -- '^args=.*--output shatter-report/focused/' "${ENV_LOG}"

# Guard-trip behavior matches the full scan: nonzero exit + message.
if SHATTER_BIN="${FAKE_SHATTER}" \
  ZOLEM_SHATTER_ENV_LOG="${ENV_LOG}" \
  FAKE_SHATTER_WRITE_GUARDS=1 \
  "${REPO_ROOT}/scripts/shatter-focused-scan.sh" 'internal/freeport/freeport.go' \
  2>"${TMP_ROOT}/guard-stderr.log"; then
  printf 'expected shatter focused scan to reject host writes\n' >&2
  exit 1
fi

grep -q 'sandbox write guard failed' "${TMP_ROOT}/guard-stderr.log"

printf 'shatter focused scan sandbox: ok\n'
