#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)

project_dir="${REPO_ROOT}"
source_dir="${REFUTE_SOURCE_DIR:-${HOME}/project/refute}"
repo="${REFUTE_REPO:-shatterproof-ai/refute}"

usage() {
  cat <<'USAGE' >&2
usage: scripts/setup-refute.sh [--project DIR] [--source DIR] [--repo OWNER/REPO]

Install or update the project-local refute binary for zolem agents.

Defaults:
  --project  current zolem checkout
  --source   ~/project/refute
  --repo     shatterproof-ai/refute

The installed binary is:
  <project>/.agents/bin/refute

If the local refute checkout is missing, clone it from:
  https://github.com/shatterproof-ai/refute
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      project_dir="${2:-}"
      shift 2
      ;;
    --source)
      source_dir="${2:-}"
      shift 2
      ;;
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'unknown argument: %s\n' "$1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "${project_dir}" ]]; then
  printf -- '--project requires a directory\n' >&2
  exit 2
fi

if [[ ! -d "${source_dir}" ]]; then
  printf 'refute checkout not found: %s\n' "${source_dir}" >&2
  printf 'clone it with: git clone https://github.com/shatterproof-ai/refute %s\n' "${source_dir}" >&2
  printf 'or set REFUTE_SOURCE_DIR / pass --source to another checkout.\n' >&2
  exit 1
fi

installer="${source_dir}/scripts/install-nightly.sh"
if [[ ! -x "${installer}" ]]; then
  printf 'refute installer not found or not executable: %s\n' "${installer}" >&2
  printf 'expected a checkout of https://github.com/shatterproof-ai/refute\n' >&2
  exit 1
fi

bash "${installer}" --project "${project_dir}" --repo "${repo}"

refute_bin="${project_dir}/.agents/bin/refute"
if [[ ! -x "${refute_bin}" ]]; then
  printf 'refute install did not create executable: %s\n' "${refute_bin}" >&2
  exit 1
fi

"${refute_bin}" version
"${refute_bin}" doctor
