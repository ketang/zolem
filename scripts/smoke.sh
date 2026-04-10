#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)

cd "${REPO_ROOT}"

./scripts/test-local-runtime.sh
PROFILE_BACKEND=fixture ./scripts/test-local-runtime.sh

printf 'smoke: ok\n'
