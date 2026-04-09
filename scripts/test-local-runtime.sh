#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOCACHE="${GOCACHE:-/tmp/zolem-local-runtime-go-cache}"
mkdir -p "$GOCACHE"

ADMIN_ADDR="${ADMIN_ADDR:-127.0.0.1:18090}"
ADMIN_BASE_URL="http://$ADMIN_ADDR"
LOG_FILE="$(mktemp)"
SERVER_PID=""

cleanup() {
  status=$?
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  if [[ $status -ne 0 ]]; then
    echo
    echo "Local runtime integration failed. Server log:"
    cat "$LOG_FILE" || true
  fi
  rm -f "$LOG_FILE"
  return $status
}
trap cleanup EXIT

echo "Using repo root: $ROOT"
echo "Using GOCACHE: $GOCACHE"
echo "Using admin address: $ADMIN_ADDR"

echo
echo "==> Running package tests"
go test ./cmd/zolem
go test ./internal/provider/... ./internal/response/... ./internal/runtime/...

echo
echo "==> Starting local admin server"
go run ./cmd/zolem -local-admin-addr "$ADMIN_ADDR" >"$LOG_FILE" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 40); do
  if curl -fsS "$ADMIN_BASE_URL/_zolem/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

curl -fsS "$ADMIN_BASE_URL/_zolem/health" >/dev/null

echo
echo "==> Creating demo profile"
curl -fsS \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"lorem"}' \
  "$ADMIN_BASE_URL/_zolem/profiles/demo" >/dev/null

echo "==> Creating OpenAI listener"
listener_json="$(curl -fsS \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}' \
  "$ADMIN_BASE_URL/_zolem/listeners/openai-demo")"

LISTENER_BASE_URL="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["base_url"])' <<<"$listener_json")"
echo "Listener base URL: $LISTENER_BASE_URL"

echo
echo "==> Verifying listener state"
state_json="$(curl -fsS "$LISTENER_BASE_URL/_zolem/state")"
python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload["provider"] == "openai", payload
assert payload["profile"] == "demo", payload
assert payload["backend"] == "lorem", payload
' <<<"$state_json"

echo "==> Calling provider-compatible endpoint"
response_json="$(curl -fsS \
  -X POST \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
  "$LISTENER_BASE_URL/v1/chat/completions")"
python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload["model"] == "gpt-4o", payload
assert payload["choices"], payload
' <<<"$response_json"

echo
echo "==> Cleaning up listener and profile"
curl -fsS -X DELETE "$ADMIN_BASE_URL/_zolem/listeners/openai-demo" >/dev/null
curl -fsS -X DELETE "$ADMIN_BASE_URL/_zolem/profiles/demo" >/dev/null

echo
echo "Local runtime verification passed."
