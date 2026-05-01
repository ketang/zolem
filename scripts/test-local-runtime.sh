#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOCACHE="${GOCACHE:-/tmp/zolem-local-runtime-go-cache}"
mkdir -p "$GOCACHE"

ADMIN_ADDR="${ADMIN_ADDR:-127.0.0.1:18090}"
LOCAL_TLS_CERT="${LOCAL_TLS_CERT:-}"
LOCAL_TLS_KEY="${LOCAL_TLS_KEY:-}"
LISTENER_TLS="${LISTENER_TLS:-0}"
PROFILE_BACKEND="${PROFILE_BACKEND:-lorem}"
ERROR_TYPE="${ERROR_TYPE:-rate_limit}"
FIXTURES_DIR="${FIXTURES_DIR:-}"
ADMIN_SCHEME="http"
CURL_ARGS=(-fsS)
ENDPOINT_CURL_ARGS=(-sS)
TLS_FLAGS=()
PROFILE_NAME="demo"
LISTENER_NAME="runtime-demo"
LISTENER_PROVIDER="openai"
LISTENER_PATH="/v1/chat/completions"
TEMP_FIXTURES_DIR=""

if [[ -n "$LOCAL_TLS_CERT" || -n "$LOCAL_TLS_KEY" ]]; then
  if [[ -z "$LOCAL_TLS_CERT" || -z "$LOCAL_TLS_KEY" ]]; then
    echo "LOCAL_TLS_CERT and LOCAL_TLS_KEY must both be set"
    exit 1
  fi
  ADMIN_SCHEME="https"
  TLS_FLAGS=(-local-tls-cert "$LOCAL_TLS_CERT" -local-tls-key "$LOCAL_TLS_KEY")
fi

if [[ "$LISTENER_TLS" == "1" && "$ADMIN_SCHEME" != "https" ]]; then
  echo "LISTENER_TLS=1 requires LOCAL_TLS_CERT and LOCAL_TLS_KEY"
  exit 1
fi

case "$PROFILE_BACKEND" in
  lorem|faker)
    ;;
  wasm)
    ;;
  error)
    ;;
  fixture)
    LISTENER_PROVIDER="anthropic"
    LISTENER_PATH="/v1/messages"
    if [[ -z "$FIXTURES_DIR" ]]; then
      TEMP_FIXTURES_DIR="$(mktemp -d)"
      FIXTURES_DIR="$TEMP_FIXTURES_DIR"
      mkdir -p "$FIXTURES_DIR/anthropic-demo"
      cat >"$FIXTURES_DIR/anthropic-demo/meta.yaml" <<'EOF'
id: anthropic-demo
provider: anthropic
version: v1
status: 200
EOF
      cat >"$FIXTURES_DIR/anthropic-demo/response.json" <<'EOF'
{"id":"fixture-msg","type":"message","role":"assistant","content":[{"type":"text","text":"fixture text"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}
EOF
      printf '%s' 'AGFzbQEAAAABBwFgAn9/AX0DAgEABQMBAAEHEgIGbWVtb3J5AgAFbWF0Y2gAAAoJAQcAQwAAgD8L' | base64 -d >"$FIXTURES_DIR/anthropic-demo/match.wasm"
    fi
    ;;
  *)
    echo "Unsupported PROFILE_BACKEND: $PROFILE_BACKEND"
    exit 1
    ;;
esac

ADMIN_BASE_URL="$ADMIN_SCHEME://$ADMIN_ADDR"
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
  if [[ -n "$TEMP_FIXTURES_DIR" ]]; then
    rm -rf "$TEMP_FIXTURES_DIR"
  fi
  return $status
}
trap cleanup EXIT

echo "Using repo root: $ROOT"
echo "Using GOCACHE: $GOCACHE"
echo "Using admin address: $ADMIN_ADDR"
echo "Using admin scheme: $ADMIN_SCHEME"
echo "Using profile backend: $PROFILE_BACKEND"
if [[ -n "$FIXTURES_DIR" ]]; then
  echo "Using fixtures dir: $FIXTURES_DIR"
fi

echo
echo "==> Running package tests"
go test ./cmd/zolem
go test ./internal/provider/... ./internal/response/... ./internal/runtime/...

echo
echo "==> Starting local admin server"
go run ./cmd/zolem -local-admin-addr "$ADMIN_ADDR" -local-fixtures-dir "$FIXTURES_DIR" "${TLS_FLAGS[@]}" >"$LOG_FILE" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 40); do
  if curl "${CURL_ARGS[@]}" "$ADMIN_BASE_URL/_zolem/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

curl "${CURL_ARGS[@]}" "$ADMIN_BASE_URL/_zolem/health" >/dev/null

echo
echo "==> Creating demo profile"
WASM_GENERATOR_BASE64='AGFzbQEAAAABFQRgAX8Bf2ACf38AYAJ/fwF/YAF/AAMHBgABAgAAAwUDAQABB08HBm1lbW9yeQIABWFsbG9jAAAHZGVhbGxvYwABCGdlbmVyYXRlAAIKcmVzdWx0X3B0cgADCnJlc3VsdF9sZW4ABAtyZXN1bHRfZnJlZQAFCh0GBQBBgAgLAgALBABBAQsFAEGAEAsEAEEXCwIACwseAQBBgBALF1siSGVsbG8gIiwiZnJvbSBXQVNNLiJd'
curl "${CURL_ARGS[@]}" \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d "$(
    if [[ "$PROFILE_BACKEND" == "error" ]]; then
      printf '{"backend":"%s","error_type":"%s"}' "$PROFILE_BACKEND" "$ERROR_TYPE"
    elif [[ "$PROFILE_BACKEND" == "wasm" ]]; then
      printf '{"backend":"wasm","wasm_module_base64":"%s","wasm_generate_timeout_ms":100,"stream_delay":{"mode":"fixed","ms":0}}' "$WASM_GENERATOR_BASE64"
    else
      printf '{"backend":"%s"}' "$PROFILE_BACKEND"
    fi
  )" \
  "$ADMIN_BASE_URL/_zolem/profiles/$PROFILE_NAME" >/dev/null

echo "==> Creating listener"
listener_payload="{\"addr\":\"127.0.0.1:0\",\"provider\":\"$LISTENER_PROVIDER\",\"profile\":\"$PROFILE_NAME\"}"
if [[ "$LISTENER_TLS" == "1" ]]; then
  listener_payload="{\"addr\":\"127.0.0.1:0\",\"provider\":\"$LISTENER_PROVIDER\",\"profile\":\"$PROFILE_NAME\",\"tls\":true}"
fi
listener_json="$(curl "${CURL_ARGS[@]}" \
  -X PUT \
  -H 'Content-Type: application/json' \
  -d "$listener_payload" \
  "$ADMIN_BASE_URL/_zolem/listeners/$LISTENER_NAME")"

LISTENER_BASE_URL="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["base_url"])' <<<"$listener_json")"
echo "Listener base URL: $LISTENER_BASE_URL"

echo
echo "==> Verifying listener health"
curl "${CURL_ARGS[@]}" "$LISTENER_BASE_URL/_zolem/health" >/dev/null

echo
echo "==> Verifying listener state"
state_json="$(curl "${CURL_ARGS[@]}" "$LISTENER_BASE_URL/_zolem/state")"
python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload["provider"] == sys.argv[1], payload
assert payload["profile"] == sys.argv[2], payload
assert payload["backend"] == sys.argv[3], payload
' "$LISTENER_PROVIDER" "$PROFILE_NAME" "$PROFILE_BACKEND" <<<"$state_json"

echo "==> Calling provider-compatible endpoint"
if [[ "$LISTENER_PROVIDER" == "openai" ]]; then
  response_json="$(curl "${ENDPOINT_CURL_ARGS[@]}" \
    -X POST \
    -H 'Authorization: Bearer sk-test' \
    -H 'Content-Type: application/json' \
    -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
    "$LISTENER_BASE_URL$LISTENER_PATH")"
  python3 -c '
import json, sys
payload = json.load(sys.stdin)
if sys.argv[1] == "error":
    assert payload["error"]["type"] == "rate_limit_error", payload
    assert payload["error"]["code"] == "rate_limit_exceeded", payload
else:
    assert payload["model"] == "gpt-4o", payload
    assert payload["choices"], payload
    if sys.argv[1] == "wasm":
        assert payload["choices"][0]["message"]["content"] == "Hello from WASM.", payload
' "$PROFILE_BACKEND" <<<"$response_json"
else
  response_json="$(curl "${ENDPOINT_CURL_ARGS[@]}" \
    -X POST \
    -H 'x-api-key: test-key' \
    -H 'Content-Type: application/json' \
    -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}' \
    "$LISTENER_BASE_URL$LISTENER_PATH")"
  python3 -c '
import json, sys
payload = json.load(sys.stdin)
if sys.argv[1] == "fixture":
    assert payload["id"] == "fixture-msg", payload
    assert payload["content"][0]["text"] == "fixture text", payload
elif sys.argv[1] == "error":
    if sys.argv[2] == "anthropic":
        assert payload["error"]["type"] == "rate_limit_error", payload
        assert payload["request_id"], payload
    else:
        assert payload["error"]["status"] == "RESOURCE_EXHAUSTED", payload
        assert payload["error"]["details"][0]["reason"] == "RATE_LIMIT_EXCEEDED", payload
else:
    assert payload["model"] == "claude-3-5-sonnet-20241022", payload
    assert payload["content"], payload
' "$PROFILE_BACKEND" "$LISTENER_PROVIDER" <<<"$response_json"
fi

echo
echo "==> Cleaning up listener and profile"
curl "${CURL_ARGS[@]}" -X DELETE "$ADMIN_BASE_URL/_zolem/listeners/$LISTENER_NAME" >/dev/null
curl "${CURL_ARGS[@]}" -X DELETE "$ADMIN_BASE_URL/_zolem/profiles/$PROFILE_NAME" >/dev/null

echo
echo "Local runtime verification passed."
