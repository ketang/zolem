#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
SHATTER_BIN=${SHATTER_BIN:-${HOME}/project/shatter/target/release/shatter}
SHATTER_SANDBOX_BACKEND=${SHATTER_SANDBOX_BACKEND:-docker}
SHATTER_SANDBOX_DOCKER_IMAGE=${SHATTER_SANDBOX_DOCKER_IMAGE:-golang:1.26-bookworm}
ZOLEM_SHATTER_REPO_WRITE_GUARD=${ZOLEM_SHATTER_REPO_WRITE_GUARD:-${REPO_ROOT}/.shatter/sandbox-host-write-guard.$$}
ZOLEM_SHATTER_TMP_WRITE_GUARD=${ZOLEM_SHATTER_TMP_WRITE_GUARD:-/tmp/zolem-shatter-sandbox-host-write-guard.$$}

cd "${REPO_ROOT}"

if [[ ! -x "${SHATTER_BIN}" ]]; then
  printf 'shatter binary not found or not executable: %s\n' "${SHATTER_BIN}" >&2
  printf 'set SHATTER_BIN or build ~/project/shatter first\n' >&2
  exit 1
fi

export SHATTER_SANDBOX_BACKEND
export SHATTER_SANDBOX_DOCKER_IMAGE
export ZOLEM_SHATTER_REPO_WRITE_GUARD
export ZOLEM_SHATTER_TMP_WRITE_GUARD

if [[ -e "${ZOLEM_SHATTER_REPO_WRITE_GUARD}" || -e "${ZOLEM_SHATTER_TMP_WRITE_GUARD}" ]]; then
  printf 'sandbox write guard path already exists; refusing to run\n' >&2
  printf 'repo guard: %s\n' "${ZOLEM_SHATTER_REPO_WRITE_GUARD}" >&2
  printf 'tmp guard: %s\n' "${ZOLEM_SHATTER_TMP_WRITE_GUARD}" >&2
  exit 1
fi

mkdir -p shatter-report

"${SHATTER_BIN}" scan \
  --project-dir "${REPO_ROOT}" \
  --language go \
  --all \
  --resume auto \
  --progress \
  --output shatter-report/full-scan.md \
  --output shatter-report/full-scan.json \
  .

guard_failed=0
if [[ -e "${ZOLEM_SHATTER_REPO_WRITE_GUARD}" ]]; then
  printf 'sandbox write guard failed: target wrote inside zolem repo: %s\n' "${ZOLEM_SHATTER_REPO_WRITE_GUARD}" >&2
  guard_failed=1
fi
if [[ -e "${ZOLEM_SHATTER_TMP_WRITE_GUARD}" ]]; then
  printf 'sandbox write guard failed: target wrote to host /tmp: %s\n' "${ZOLEM_SHATTER_TMP_WRITE_GUARD}" >&2
  guard_failed=1
fi
if [[ "${guard_failed}" -ne 0 ]]; then
  exit 1
fi
