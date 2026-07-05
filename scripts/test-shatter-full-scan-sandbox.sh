#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
# shellcheck source=scripts/test-shatter-scan-fixture.sh
source "${SCRIPT_DIR}/test-shatter-scan-fixture.sh"

SHATTER_BIN="${FAKE_SHATTER}" \
ZOLEM_SHATTER_ENV_LOG="${ENV_LOG}" \
"${REPO_ROOT}/scripts/shatter-full-scan.sh"

grep -qx 'backend=docker' "${ENV_LOG}"
grep -qx 'image=golang:1.26-bookworm' "${ENV_LOG}"
grep -Eq '^repo_guard=.+' "${ENV_LOG}"
grep -Eq '^tmp_guard=/tmp/.+' "${ENV_LOG}"

if SHATTER_BIN="${FAKE_SHATTER}" \
  ZOLEM_SHATTER_ENV_LOG="${ENV_LOG}" \
  FAKE_SHATTER_WRITE_GUARDS=1 \
  "${REPO_ROOT}/scripts/shatter-full-scan.sh" 2>"${TMP_ROOT}/guard-stderr.log"; then
  printf 'expected shatter full scan to reject host writes\n' >&2
  exit 1
fi

grep -q 'sandbox write guard failed' "${TMP_ROOT}/guard-stderr.log"

printf 'shatter full scan sandbox: ok\n'
