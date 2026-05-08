#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
TMP_ROOT=$(mktemp -d)

cleanup() {
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

fake_refute="${TMP_ROOT}/refute"
fake_project="${TMP_ROOT}/project"
missing_source="${TMP_ROOT}/missing-refute"

mkdir -p "${fake_refute}/scripts" "${fake_project}"

cat >"${fake_refute}/scripts/install-nightly.sh" <<'INSTALL'
#!/usr/bin/env bash

set -euo pipefail

project_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      project_dir="${2:-}"
      shift 2
      ;;
    --repo)
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

mkdir -p "${project_dir}/.agents/bin"
cat >"${project_dir}/.agents/bin/refute" <<'REFUTE'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  version)
    printf 'refute test version\n'
    ;;
  doctor)
    printf 'refute test doctor\n'
    ;;
  *)
    printf 'unexpected refute command: %s\n' "${1:-}" >&2
    exit 2
    ;;
esac
REFUTE
chmod +x "${project_dir}/.agents/bin/refute"
INSTALL
chmod +x "${fake_refute}/scripts/install-nightly.sh"

"${REPO_ROOT}/scripts/setup-refute.sh" --project "${fake_project}" --source "${fake_refute}" >"${TMP_ROOT}/setup.log"

test -x "${fake_project}/.agents/bin/refute"
grep -q 'refute test version' "${TMP_ROOT}/setup.log"
grep -q 'refute test doctor' "${TMP_ROOT}/setup.log"

if "${REPO_ROOT}/scripts/setup-refute.sh" --project "${fake_project}" --source "${missing_source}" >"${TMP_ROOT}/missing.log" 2>&1; then
  printf 'expected setup-refute.sh to fail when the refute checkout is missing\n' >&2
  exit 1
fi

grep -q 'https://github.com/shatterproof-ai/refute' "${TMP_ROOT}/missing.log"
grep -q 'REFUTE_SOURCE_DIR' "${TMP_ROOT}/missing.log"

printf 'refute setup: ok\n'
