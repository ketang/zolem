#!/usr/bin/env bash
# Runs the locally built snapshot image (never a pulled tag) with the
# non-loopback opt-in flags and exercises it through published ports.
# Expects `goreleaser release --snapshot --clean` to have produced
# dist/artifacts.json.
set -euo pipefail

cd "$(dirname "$0")/.."

image="$(jq -r '[.[] | select(.type == "Docker Image" and .goarch == "amd64")][0].name' dist/artifacts.json)"
if [ -z "$image" ] || [ "$image" = "null" ]; then
  echo "no amd64 Docker Image in dist/artifacts.json" >&2
  exit 1
fi
docker image inspect "$image" >/dev/null

cid="$(docker run -d --rm \
  -p 127.0.0.1:18090:18090 -p 127.0.0.1:18100-18101:18100-18101 \
  "$image" -local-admin-addr 0.0.0.0:18090 -allow-non-loopback-bind \
  -allowed-host zolem.test -listener-port-range 18100-18101)"
trap 'docker logs "$cid" >&2 || true; docker stop "$cid" >/dev/null 2>&1 || true' EXIT

admin=http://127.0.0.1:18090
for _ in $(seq 1 50); do
  curl -fsS "$admin/_zolem/health" >/dev/null 2>&1 && break
  sleep 0.2
done
[ "$(curl -fsS "$admin/_zolem/health")" = '{"status":"ok"}' ] || { echo "health mismatch" >&2; exit 1; }

curl -fsS -X PUT -H 'Content-Type: application/json' -d '{"backend":"lorem"}' "$admin/_zolem/profiles/p1" >/dev/null
base_url="$(curl -fsS -X PUT -H 'Content-Type: application/json' \
  -d '{"addr":"0.0.0.0:18100","provider":"openai","profile":"p1"}' \
  "$admin/_zolem/listeners/l1" | jq -r .base_url)"
if [ "$base_url" != "http://localhost:18100" ]; then
  echo "base_url = $base_url, want http://localhost:18100" >&2
  exit 1
fi

body='{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'
chat() {
  curl -sS -o /dev/null -w '%{http_code}' "$@" -X POST \
    -H 'Content-Type: application/json' -H 'Authorization: Bearer sk-test' \
    -d "$body" "$base_url/v1/chat/completions"
}
[ "$(chat)" = 200 ] || { echo "chat to $base_url did not return 200" >&2; exit 1; }
[ "$(chat -H 'Host: evil.example')" = 403 ] || { echo "foreign Host not rejected" >&2; exit 1; }
echo "docker image test passed"
