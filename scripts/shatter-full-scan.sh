#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/shatter-scan-lib.sh
source "${SCRIPT_DIR}/shatter-scan-lib.sh"

shatter_scan_setup

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

shatter_scan_check_guards
