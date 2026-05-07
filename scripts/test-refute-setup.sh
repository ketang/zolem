#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
installer="${repo_root}/scripts/install-refute.sh"

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "${tmp}"
}
trap cleanup EXIT

missing_refute="${tmp}/missing-refute"
missing_project="${tmp}/missing-project"
mkdir -p "${missing_project}"

if REFUTE_REPO="${missing_refute}" "${installer}" --project "${missing_project}" >"${tmp}/missing.out" 2>"${tmp}/missing.err"; then
  echo "expected missing refute checkout to fail" >&2
  exit 1
fi

grep -F "Refute checkout not found: ${missing_refute}" "${tmp}/missing.err" >/dev/null
grep -F "Clone https://github.com/shatterproof-ai/refute" "${tmp}/missing.err" >/dev/null

fake_refute="${tmp}/refute"
fake_project="${tmp}/project"
mkdir -p "${fake_refute}/scripts" "${fake_project}"

cat >"${fake_refute}/scripts/install-nightly.sh" <<'FAKE_INSTALLER'
#!/usr/bin/env bash
set -euo pipefail

project=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      project="${2:-}"
      shift 2
      ;;
    *)
      echo "unexpected argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "${project}" ]]; then
  echo "missing --project" >&2
  exit 2
fi

mkdir -p "${project}/.agents/bin"
cat >"${project}/.agents/bin/refute" <<'FAKE_REFUTE'
#!/usr/bin/env bash
case "${1:-}" in
  version)
    echo "refute fake"
    ;;
  doctor)
    echo "doctor ok"
    ;;
  *)
    echo "unexpected refute command: ${1:-}" >&2
    exit 2
    ;;
esac
FAKE_REFUTE
chmod +x "${project}/.agents/bin/refute"
FAKE_INSTALLER
chmod +x "${fake_refute}/scripts/install-nightly.sh"

REFUTE_REPO="${fake_refute}" "${installer}" --project "${fake_project}" >"${tmp}/install.out"

test -x "${fake_project}/.agents/bin/refute"
grep -F "doctor ok" "${tmp}/install.out" >/dev/null
