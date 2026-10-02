#!/usr/bin/env bash
# Classifies the nightly publication state. Pure function: reads facts from the
# environment and prints exactly one state name. See zolem-0an.
#
# Inputs (env):
#   HAS_DRAFT        true|false  a draft release nightly-candidate exists
#   DRAFT_COMMIT     commit the draft targets (Dc)
#   HAS_RELEASE      true|false  a public release nightly exists
#   RELEASE_COMMIT   commit named in its notes (Rc), or "unknown"
#   NIGHTLY_DIGEST   manifest digest of :nightly (empty if absent)
#   CANDIDATE_DIGEST manifest digest of :nightly-candidate (empty if absent)
#   CANDIDATE_COMMIT commit the :nightly-candidate image was built from
#   BUILD_COMMIT     commit being built (B)
#   FORCE_REPUBLISH  true|false  treat S-done as S-ready
#
# States are checked most-advanced first, so exactly one matches.
set -euo pipefail

has_draft="${HAS_DRAFT:-false}"
has_release="${HAS_RELEASE:-false}"
release_commit="${RELEASE_COMMIT:-unknown}"
nightly_digest="${NIGHTLY_DIGEST:-}"
candidate_digest="${CANDIDATE_DIGEST:-}"
draft_commit="${DRAFT_COMMIT:-}"
candidate_commit="${CANDIDATE_COMMIT:-}"
build_commit="${BUILD_COMMIT:-}"
force="${FORCE_REPUBLISH:-false}"

if [ "$has_draft" = true ] && [ "$has_release" != true ]; then
  echo S-release-deleted
elif [ "$has_draft" = true ] && [ -n "$nightly_digest" ] && [ "$nightly_digest" = "$candidate_digest" ] \
  && [ -n "$candidate_commit" ] && [ "$candidate_commit" = "$draft_commit" ]; then
  # Equal digests alone are not enough: after any completed run :nightly and
  # :nightly-candidate share a digest forever, so the candidate image must
  # also have been built from the draft's commit.
  echo S-image-moved
elif [ "$has_draft" = true ]; then
  echo S-staged
elif [ "$has_release" = true ] && [ "$release_commit" = "$build_commit" ] && [ "$force" != true ]; then
  echo S-done
elif [ "$has_release" = true ]; then
  echo S-ready
else
  echo S-bootstrap
fi
