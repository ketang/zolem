#!/usr/bin/env bash
# Staged publication of the nightly release (zolem-0an). Run from the nightly
# workflow's publish job at the repo root with GH_TOKEN, GITHUB_REPOSITORY,
# and gh/docker/cosign/goreleaser/jq on PATH.
#
# Nothing public changes until promotion. `gh release upload --clobber` is only
# ever run against the draft nightly-candidate release, because it deletes an
# existing asset before uploading its replacement.
#
# Known limitation: promotion deletes the old public release (4.3) and then
# publishes the draft (4.4). Both are retried, but if the run dies between them
# the public nightly release is missing until the next successful run (the
# next run recovers via S-release-deleted).
#
# Env: STOP_AT (none|before_promote|after_4_1|after_4_3) and FORCE_REPUBLISH
# (true|false) are test controls; the workflow only passes non-default values
# to the repository owner's workflow_dispatch runs.
set -euo pipefail

STOP_AT="${STOP_AT:-none}"
FORCE_REPUBLISH="${FORCE_REPUBLISH:-false}"

REPO="$GITHUB_REPOSITORY"
IMG=ghcr.io/ketang/zolem
B="$(git rev-parse HEAD)"
IDENTITY_RE='^https://github.com/ketang/zolem/\.github/workflows/nightly\.yml@'
OIDC_ISSUER=https://token.actions.githubusercontent.com

stop() {
  if [ "$STOP_AT" = "$1" ]; then
    echo "stop_at=$1: stopping" >&2
    exit 1
  fi
}

digest_of() {
  docker buildx imagetools inspect "$1" --format '{{json .Manifest}}' 2>/dev/null | jq -r .digest 2>/dev/null || true
}

# Retry a command a few times; used for the 4.3 -> 4.4 hand-off so a transient
# API error cannot leave the public release deleted.
retry() {
  local n
  for n in 1 2 3 4 5; do
    "$@" && return 0
    echo "retry $n/5: $*" >&2
    sleep $((n * 5))
  done
  return 1
}

notes_for() {
  local when
  when="$(TZ=UTC git show -s --format=%cd --date=format-local:'%Y-%m-%d %H:%M UTC' "$1")"
  echo "$1 $when"
}

# Observe the publication facts (see scripts/nightly-state.sh).
gather_facts() {
  local row body
  HAS_DRAFT=false DRAFT_COMMIT=""
  row="$(gh api --paginate "repos/$REPO/releases" \
    --jq '.[] | select(.draft and .tag_name=="nightly-candidate") | [.id, .target_commitish] | @tsv' | head -n1)"
  if [ -n "$row" ]; then
    HAS_DRAFT=true
    DRAFT_COMMIT="${row#*$'\t'}"
  fi
  HAS_RELEASE=false RELEASE_COMMIT=unknown
  if body="$(gh api "repos/$REPO/releases/tags/nightly" --jq .body 2>/dev/null)"; then
    HAS_RELEASE=true
    if [[ "$body" =~ ^([0-9a-f]{40})([[:space:]]|$) ]]; then
      RELEASE_COMMIT="${BASH_REMATCH[1]}"
    fi
  fi
  NIGHTLY_DIGEST="$(digest_of "$IMG:nightly")"
  CANDIDATE_DIGEST="$(digest_of "$IMG:nightly-candidate")"
  echo "facts: draft=$HAS_DRAFT($DRAFT_COMMIT) release=$HAS_RELEASE($RELEASE_COMMIT)" \
    "nightly=$NIGHTLY_DIGEST candidate=$CANDIDATE_DIGEST build=$B tag=$(git ls-remote origin refs/tags/nightly | cut -f1)"
}

classify() {
  HAS_DRAFT="$HAS_DRAFT" DRAFT_COMMIT="$DRAFT_COMMIT" \
    HAS_RELEASE="$HAS_RELEASE" RELEASE_COMMIT="$RELEASE_COMMIT" \
    NIGHTLY_DIGEST="$NIGHTLY_DIGEST" CANDIDATE_DIGEST="$CANDIDATE_DIGEST" \
    BUILD_COMMIT="$B" FORCE_REPUBLISH="$FORCE_REPUBLISH" \
    ./scripts/nightly-state.sh
}

# Step 1+2: build and stage assets and images. Public state untouched.
build_and_stage() {
  goreleaser release --snapshot --clean --skip=sign
  cd dist
  # Sign every archive, SBOM, and the checksums file.
  for f in zolem-nightly-*.tar.gz zolem-nightly-*.sbom checksums.txt; do
    cosign sign-blob --yes --bundle "$f.bundle" "$f"
  done
  cd ..

  if [ "$HAS_DRAFT" != true ]; then
    gh release create nightly-candidate --draft --prerelease \
      --target "$B" --title "Nightly candidate" --notes "$(notes_for "$B")"
  fi
  # Safe: nightly-candidate is a draft, never the public release.
  gh release upload nightly-candidate --clobber \
    dist/zolem-nightly-*.tar.gz dist/zolem-nightly-*.sbom \
    dist/*.bundle dist/checksums.txt

  for arch in amd64 arm64; do
    docker tag "$IMG:nightly-$arch" "$IMG:nightly-candidate-$arch"
    docker push "$IMG:nightly-candidate-$arch"
  done
  docker buildx imagetools create -t "$IMG:nightly-candidate" \
    "$IMG:nightly-candidate-amd64" "$IMG:nightly-candidate-arm64"
  local d
  d="$(digest_of "$IMG:nightly-candidate")"
  cosign sign --yes "$IMG:nightly-candidate@$d"
}

# Step 3: verify the staged set (draft assets and candidate image).
verify_staged() {
  local id d f row
  id="$(gh api --paginate "repos/$REPO/releases" \
    --jq '.[] | select(.draft and .tag_name=="nightly-candidate") | .id' | head -n1)"
  [ -n "$id" ] || return 1
  rm -rf verify && mkdir verify
  gh api "repos/$REPO/releases/$id" --jq '.assets[] | [.id, .name] | @tsv' > verify.list
  [ -s verify.list ] || return 1
  while IFS=$'\t' read -r aid name; do
    gh api -H 'Accept: application/octet-stream' "repos/$REPO/releases/assets/$aid" > "verify/$name"
  done < verify.list
  (cd verify && sha256sum -c checksums.txt) || return 1
  f=verify/zolem-nightly-linux-amd64.tar.gz
  cosign verify-blob --bundle "$f.bundle" \
    --certificate-identity-regexp "$IDENTITY_RE" \
    --certificate-oidc-issuer "$OIDC_ISSUER" "$f" || return 1
  d="$(digest_of "$IMG:nightly-candidate")"
  [ -n "$d" ] || return 1
  cosign verify "$IMG:nightly-candidate@$d" \
    --certificate-identity-regexp "$IDENTITY_RE" \
    --certificate-oidc-issuer "$OIDC_ISSUER" >/dev/null || return 1
  docker buildx imagetools inspect "$IMG:nightly-candidate" | grep -q 'linux/amd64' || return 1
  docker buildx imagetools inspect "$IMG:nightly-candidate" | grep -q 'linux/arm64' || return 1
}

# Step 4: promote the staged commit $1. Idempotent and resumable.
promote() {
  local commit="$1"
  stop before_promote
  # 4.1: same manifest digest as the candidate, so its signature applies.
  docker buildx imagetools create -t "$IMG:nightly" "$IMG:nightly-candidate"
  stop after_4_1
  # 4.2: the only forced ref operation; moves the tag to the built commit.
  git tag -f nightly "$commit"
  git push -f origin refs/tags/nightly
  # 4.3: delete the old public release only, keeping the tag.
  if gh release view nightly >/dev/null 2>&1; then
    gh release delete nightly --yes
  fi
  stop after_4_3
  # 4.4: publish the draft as the new nightly.
  gh release edit nightly-candidate --tag nightly --draft=false --prerelease \
    --title Nightly --notes "$(notes_for "$commit")"
}

for _ in 1 2 3; do
  gather_facts
  state="$(classify)"
  echo "state: $state"
  case "$state" in
    S-release-deleted | S-image-moved)
      promote "$DRAFT_COMMIT"
      [ "$DRAFT_COMMIT" != "$B" ] || exit 0
      ;;
    S-staged)
      if [ "$DRAFT_COMMIT" = "$B" ] && verify_staged; then
        promote "$B"
        exit 0
      fi
      gh release delete nightly-candidate --yes
      ;;
    S-done)
      exit 0
      ;;
    S-ready | S-bootstrap)
      build_and_stage
      verify_staged
      promote "$B"
      exit 0
      ;;
  esac
done
echo "state machine did not converge" >&2
exit 1
