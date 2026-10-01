#!/usr/bin/env bash
# Unit test for scripts/nightly-state.sh. No network; pure env-in, name-out.
set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
B=bbbb

# classify HAS_DRAFT HAS_RELEASE RELEASE_COMMIT NIGHTLY_DIGEST CANDIDATE_DIGEST FORCE
classify() {
  HAS_DRAFT="$1" DRAFT_COMMIT="${DRAFT_COMMIT:-dddd}" HAS_RELEASE="$2" \
    RELEASE_COMMIT="$3" NIGHTLY_DIGEST="$4" CANDIDATE_DIGEST="$5" \
    BUILD_COMMIT="$B" FORCE_REPUBLISH="$6" ./scripts/nightly-state.sh
}

expect() {
  local want="$1" got
  shift
  got="$(classify "$@")"
  if [ "$got" != "$want" ]; then
    echo "FAIL: classify $* => $got, want $want" >&2
    fail=1
  fi
}

expect S-release-deleted true false "" "" "" false
expect S-image-moved true true "$B" sha256:aa sha256:aa false
expect S-staged true true "$B" sha256:aa sha256:bb false
expect S-done false true "$B" sha256:aa sha256:aa false
expect S-ready false true "$B" sha256:aa sha256:aa true
expect S-ready false true cccc sha256:aa sha256:aa false
expect S-ready false true unknown sha256:aa sha256:aa false
expect S-bootstrap false false "" "" "" false
# Force never overrides the recovery states.
expect S-release-deleted true false "" "" "" true
expect S-image-moved true true "$B" sha256:aa sha256:aa true
expect S-staged true true "$B" sha256:aa sha256:bb true
# Missing digests are never "equal" (empty != empty for a draft+release).
expect S-staged true true "$B" "" "" false

# Exhaustive: exactly one valid state for every combination.
valid=" S-release-deleted S-image-moved S-staged S-done S-ready S-bootstrap "
for d in true false; do
  for r in true false; do
    for eq in true false; do
      for rcb in true false; do
        for force in true false; do
          nd=sha256:aa
          cd_=sha256:aa
          [ "$eq" = true ] || cd_=sha256:bb
          rc=cccc
          [ "$rcb" = true ] && rc="$B"
          out="$(classify "$d" "$r" "$rc" "$nd" "$cd_" "$force")"
          if [ "$(printf '%s\n' "$out" | wc -l)" -ne 1 ] || [[ "$valid" != *" $out "* ]]; then
            echo "FAIL: D=$d R=$r eq=$eq Rc=B:$rcb force=$force => '$out'" >&2
            fail=1
          fi
        done
      done
    done
  done
done

[ "$fail" -eq 0 ] || exit 1
echo "nightly-state tests passed"
