#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
TMP_ROOT=$(mktemp -d)

cleanup() {
  if [[ -f "${ENV_LOG:-}" ]]; then
    repo_guard=$(sed -n 's/^repo_guard=//p' "${ENV_LOG}" | head -n1)
    tmp_guard=$(sed -n 's/^tmp_guard=//p' "${ENV_LOG}" | head -n1)
    [[ -n "${repo_guard}" ]] && rm -f -- "${repo_guard}"
    [[ -n "${tmp_guard}" ]] && rm -f -- "${tmp_guard}"
  fi
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

FAKE_SHATTER="${TMP_ROOT}/shatter"
ENV_LOG="${TMP_ROOT}/shatter-env.log"

cat >"${FAKE_SHATTER}" <<'FAKE'
#!/usr/bin/env bash

set -euo pipefail

{
  printf 'backend=%s\n' "${SHATTER_SANDBOX_BACKEND:-}"
  printf 'image=%s\n' "${SHATTER_SANDBOX_DOCKER_IMAGE:-}"
  printf 'repo_guard=%s\n' "${ZOLEM_SHATTER_REPO_WRITE_GUARD:-}"
  printf 'tmp_guard=%s\n' "${ZOLEM_SHATTER_TMP_WRITE_GUARD:-}"
} >"${ZOLEM_SHATTER_ENV_LOG}"

if [[ "${FAKE_SHATTER_WRITE_GUARDS:-0}" == "1" ]]; then
  printf 'repo write escaped sandbox\n' >"${ZOLEM_SHATTER_REPO_WRITE_GUARD}"
  printf 'tmp write escaped sandbox\n' >"${ZOLEM_SHATTER_TMP_WRITE_GUARD}"
fi
FAKE
chmod +x "${FAKE_SHATTER}"

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
