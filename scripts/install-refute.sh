#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE' >&2
usage: scripts/install-refute.sh [--project DIR] [--refute-repo DIR]

Install or update the project-local refute binary for agents working in zolem.

The default refute checkout is:

  ~/project/refute

Override it with REFUTE_REPO or --refute-repo. The binary is installed to:

  <project>/.agents/bin/refute

Options:
  --project DIR      project root, defaults to this repository
  --refute-repo DIR  local refute checkout, defaults to REFUTE_REPO or ~/project/refute
  -h, --help         show this help
USAGE
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
project_dir="${repo_root}"
refute_repo="${REFUTE_REPO:-${HOME}/project/refute}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      project_dir="${2:-}"
      shift 2
      ;;
    --refute-repo)
      refute_repo="${2:-}"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "${project_dir}" ]]; then
  echo "--project requires a directory" >&2
  exit 2
fi

if [[ -z "${refute_repo}" ]]; then
  echo "--refute-repo requires a directory" >&2
  exit 2
fi

if [[ ! -d "${refute_repo}" ]]; then
  echo "Refute checkout not found: ${refute_repo}" >&2
  echo "Clone https://github.com/shatterproof-ai/refute to ${refute_repo}, or set REFUTE_REPO=/path/to/refute." >&2
  exit 1
fi

installer="${refute_repo}/scripts/install-nightly.sh"
if [[ ! -x "${installer}" ]]; then
  echo "Refute installer not found or not executable: ${installer}" >&2
  echo "Update the checkout from https://github.com/shatterproof-ai/refute, or set REFUTE_REPO to a compatible checkout." >&2
  exit 1
fi

bash "${installer}" --project "${project_dir}"
"${project_dir}/.agents/bin/refute" doctor
