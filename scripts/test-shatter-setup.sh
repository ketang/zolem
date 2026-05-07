#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
SHATTER_BIN=${SHATTER_BIN:-${HOME}/project/shatter/target/release/shatter}
TMP_ROOT=$(mktemp -d)

cleanup() {
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

cd "${REPO_ROOT}"

test -x "${SHATTER_BIN}"
test -f .shatter/config.yaml
test -f shatter.config.json
test -x scripts/shatter-full-scan.sh

mkdir -p "${TMP_ROOT}/.shatter"
cp -a cmd internal go.mod go.sum "${TMP_ROOT}/"
cp -a .shatter/config.yaml "${TMP_ROOT}/.shatter/config.yaml"

"${SHATTER_BIN}" scan --project-dir "${TMP_ROOT}" --dry-run --all --language go "${TMP_ROOT}" >/tmp/zolem-shatter-dry-run.txt

summary=$(grep -Eo 'Summary: [0-9]+ function\(s\) across [0-9]+ file\(s\)' /tmp/zolem-shatter-dry-run.txt | head -n1)
functions=$(printf '%s\n' "${summary}" | awk '{print $2}')
files=$(printf '%s\n' "${summary}" | awk '{print $5}')

if [[ -z "${functions}" || -z "${files}" || "${functions}" -lt 160 || "${files}" -lt 33 ]]; then
  printf 'unexpected shatter dry-run summary: %s\n' "${summary}" >&2
  exit 1
fi

grep -q 'runLocalAdmin' /tmp/zolem-shatter-dry-run.txt
grep -q 'handleChatCompletions' /tmp/zolem-shatter-dry-run.txt
grep -q 'validateFixtureNamespace' /tmp/zolem-shatter-dry-run.txt

printf 'shatter setup: ok\n'
