#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
SHATTER_BIN=${SHATTER_BIN:-${HOME}/project/shatter/target/release/shatter}

cd "${REPO_ROOT}"

if [[ ! -x "${SHATTER_BIN}" ]]; then
  printf 'shatter binary not found or not executable: %s\n' "${SHATTER_BIN}" >&2
  printf 'set SHATTER_BIN or build ~/project/shatter first\n' >&2
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
