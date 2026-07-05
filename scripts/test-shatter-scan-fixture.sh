# shellcheck shell=bash
# Shared fixture for the shatter scan sandbox tests. Sourced by
# test-shatter-full-scan-sandbox.sh and test-shatter-focused-scan-sandbox.sh.
#
# Provides a fake shatter binary that records the sandbox env and its argv to
# ZOLEM_SHATTER_ENV_LOG, optionally trips the host-write guards, plus a
# cleanup trap for the guards and temp dir.
#
# Sets: TMP_ROOT, FAKE_SHATTER, ENV_LOG

TMP_ROOT=$(mktemp -d)
FAKE_SHATTER="${TMP_ROOT}/shatter"
ENV_LOG="${TMP_ROOT}/shatter-env.log"

shatter_test_cleanup() {
  if [[ -f "${ENV_LOG:-}" ]]; then
    repo_guard=$(sed -n 's/^repo_guard=//p' "${ENV_LOG}" | head -n1)
    tmp_guard=$(sed -n 's/^tmp_guard=//p' "${ENV_LOG}" | head -n1)
    if [[ -n "${repo_guard}" ]]; then rm -f -- "${repo_guard}"; fi
    if [[ -n "${tmp_guard}" ]]; then rm -f -- "${tmp_guard}"; fi
  fi
  rm -rf "${TMP_ROOT}"
}
trap shatter_test_cleanup EXIT

cat >"${FAKE_SHATTER}" <<'FAKE'
#!/usr/bin/env bash

set -euo pipefail

{
  printf 'backend=%s\n' "${SHATTER_SANDBOX_BACKEND:-}"
  printf 'image=%s\n' "${SHATTER_SANDBOX_DOCKER_IMAGE:-}"
  printf 'repo_guard=%s\n' "${ZOLEM_SHATTER_REPO_WRITE_GUARD:-}"
  printf 'tmp_guard=%s\n' "${ZOLEM_SHATTER_TMP_WRITE_GUARD:-}"
  printf 'args=%s\n' "$*"
} >"${ZOLEM_SHATTER_ENV_LOG}"

if [[ "${FAKE_SHATTER_WRITE_GUARDS:-0}" == "1" ]]; then
  printf 'repo write escaped sandbox\n' >"${ZOLEM_SHATTER_REPO_WRITE_GUARD}"
  printf 'tmp write escaped sandbox\n' >"${ZOLEM_SHATTER_TMP_WRITE_GUARD}"
fi
FAKE
chmod +x "${FAKE_SHATTER}"
