#!/usr/bin/env bash
# Offline test for scripts/nightly-publish.sh: runs it against stub gh, docker,
# cosign, goreleaser and sleep binaries and a local bare git origin, and checks
# which public operations it performs.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "ok: $*"; }
bad() { echo "FAIL: $*" >&2; fail=1; }

mkdir -p "$T/bin" "$T/st" "$T/repo/scripts"
ST="$T/st"

cat >"$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
ST="$STUB_STATE"
echo "gh $*" >>"$ST/calls"
bump() { local n; n=$(($(cat "$ST/$1" 2>/dev/null || echo 0) + 1)); echo "$n" >"$ST/$1"; echo "$n"; }
case "$*" in
  "api --paginate repos/o/r/releases --jq"*"@tsv"*) [ -f "$ST/draft_row" ] && cat "$ST/draft_row"; exit 0 ;;
  "api --paginate repos/o/r/releases --jq"*) [ -f "$ST/draft_row" ] && cut -f1 "$ST/draft_row"; exit 0 ;;
  "api repos/o/r/releases/tags/nightly"*)
    case "$(cat "$ST/release_mode")" in
      ok) cat "$ST/release_body"; exit 0 ;;
      404) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
      *) echo "gh: Server Error (HTTP 500)" >&2; exit 1 ;;
    esac ;;
  "api repos/o/r/releases/123 --jq"*) cat "$ST/assets.list"; exit 0 ;;
  "api -H"*"releases/assets/"*) cat "$ST/asset.${!###*/}"; exit 0 ;;
  "release view nightly") [ "$(cat "$ST/release_mode")" = ok ]; exit $? ;;
  "release delete nightly --yes")
    [ "$(bump n_delete)" -gt "${DELETE_FAILS:-0}" ]; exit $? ;;
  "release edit"*)
    [ "$(bump n_edit)" -gt "${EDIT_FAILS:-0}" ]; exit $? ;;
  "release delete nightly-candidate --yes") rm -f "$ST/draft_row"; exit 0 ;;
  *) exit 0 ;;
esac
STUB
cat >"$T/bin/docker" <<'STUB'
#!/usr/bin/env bash
ST="$STUB_STATE"
echo "docker $*" >>"$ST/calls"
if [ "$1 $2 $3" = "buildx imagetools inspect" ]; then
  case "$4" in
    *:nightly-candidate) d="$(cat "$ST/candidate_digest")" ;;
    *:nightly) d="$(cat "$ST/nightly_digest")" ;;
    *) d="" ;;
  esac
  case "${5:-} ${6:-}" in
    "--format {{json .Manifest}}") echo "{\"digest\":\"$d\"}" ;;
    "--format {{json .Image}}")
      echo "{\"linux/amd64\":{\"config\":{\"Labels\":{\"org.opencontainers.image.revision\":\"$(cat "$ST/candidate_label")\"}}}}" ;;
    *) echo "linux/amd64 linux/arm64" ;;
  esac
fi
exit 0
STUB
printf '#!/usr/bin/env bash\necho "cosign $*" >>"$STUB_STATE/calls"\n' >"$T/bin/cosign"
printf '#!/usr/bin/env bash\necho "goreleaser $*" >>"$STUB_STATE/calls"\nexit 1\n' >"$T/bin/goreleaser"
printf '#!/usr/bin/env bash\nexit 0\n' >"$T/bin/sleep"
chmod +x "$T/bin/"*

git init -q --bare "$T/origin.git"
git init -q "$T/repo"
cp "$ROOT/scripts/nightly-state.sh" "$T/repo/scripts/"
(
  cd "$T/repo"
  git -c user.name=t -c user.email=t@t commit -q --allow-empty -m c
  git remote add origin "$T/origin.git"
)
B="$(git -C "$T/repo" rev-parse HEAD)"

archive=zolem-nightly-linux-amd64.tar.gz

# reset_state <release_mode> <draft_commit|-> <candidate_label> <assets:full|partial>
reset_state() {
  rm -f "$ST"/* "$T"/repo/verify.list
  git -C "$T/repo" push -q origin --delete nightly 2>/dev/null || true
  git -C "$T/repo" tag -d nightly >/dev/null 2>&1 || true
  echo "$1" >"$ST/release_mode"
  echo "0000000000000000000000000000000000000000 2020-01-01 00:00 UTC" >"$ST/release_body"
  [ "$2" = - ] || printf '123\t%s\n' "$2" >"$ST/draft_row"
  echo sha256:aa >"$ST/nightly_digest"
  echo sha256:aa >"$ST/candidate_digest"
  echo "$3" >"$ST/candidate_label"
  echo archive >"$ST/asset.2"
  printf '%s\t%s\n2\t%s\n' 1 checksums.txt "$archive" >"$ST/assets.list"
  printf '3\t%s.bundle\n' "$archive" >>"$ST/assets.list"
  echo bundle >"$ST/asset.3"
  (cd "$ST" && printf 'archive\n' | sha256sum | sed "s/-/$archive/" >asset.1)
  if [ "$4" = partial ]; then
    printf '2\t%s\n' "$archive" >"$ST/assets.list"
  fi
}

run() { # run [ENV=VAL...]; sets rc
  rc=0
  (cd "$T/repo" && env PATH="$T/bin:$PATH" STUB_STATE="$ST" GITHUB_REPOSITORY=o/r "$@" \
    bash "$ROOT/scripts/nightly-publish.sh") >"$T/out" 2>&1 || rc=$?
}
count() { grep -cF -- "$1" "$ST/calls" || true; }
origin_tag() { git -C "$T/repo" ls-remote origin refs/tags/nightly | cut -f1; }

# 1. Transient failures in 4.3/4.4 are retried (a legitimate S-image-moved).
reset_state ok "$B" "$B" full
run DELETE_FAILS=2 EDIT_FAILS=1
[ "$rc" -eq 0 ] || { bad "retry run exited $rc"; cat "$T/out" >&2; }
[ "$(count 'release delete nightly --yes')" -eq 3 ] || bad "delete not retried to success"
[ "$(count 'release edit')" -eq 2 ] || bad "edit not retried to success"
[ "$(origin_tag)" = "$B" ] || bad "nightly tag not moved to build commit"
ok "4.3/4.4 retried"

# 2. Stale equal digests + draft whose image is from another commit: no promote.
reset_state ok "$B" oldcommit full
run
[ "$rc" -ne 0 ] || bad "stale-digest run should stop at the build (stubbed failure)"
[ "$(count 'release delete nightly --yes')" -eq 0 ] || bad "stale draft deleted public release"
[ "$(count 'release edit')" -eq 0 ] || bad "stale draft was published"
[ -z "$(origin_tag)" ] || bad "stale draft moved the nightly tag"
[ "$(count 'release delete nightly-candidate --yes')" -ge 1 ] || bad "stale draft not discarded"
ok "stale image digest does not promote a draft"

# 2b. Matching image but incomplete draft assets: no promote.
reset_state ok "$B" "$B" partial
run
[ "$(count 'release delete nightly --yes')" -eq 0 ] && [ "$(count 'release edit')" -eq 0 ] && [ -z "$(origin_tag)" ] ||
  bad "incomplete draft was promoted"
ok "incomplete draft is not promoted"

# 3. Non-404 failure reading the public release aborts with no state change.
reset_state 500 "$B" "$B" full
run
[ "$rc" -ne 0 ] || bad "5xx on release lookup should fail the run"
[ "$(count 'release delete')" -eq 0 ] && [ "$(count 'release edit')" -eq 0 ] && [ -z "$(origin_tag)" ] ||
  bad "state changed after a 5xx"
ok "5xx on release lookup aborts"

# 4. retry must actually be used for steps 4.3 and 4.4.
grep -qE '^[[:space:]]+retry delete_public_release$' "$ROOT/scripts/nightly-publish.sh" || bad "4.3 not wrapped in retry"
grep -qE '^[[:space:]]+retry gh release edit ' "$ROOT/scripts/nightly-publish.sh" || bad "4.4 not wrapped in retry"
ok "retry wired into 4.3 and 4.4"

[ "$fail" -eq 0 ] || exit 1
echo "nightly-publish tests passed"
