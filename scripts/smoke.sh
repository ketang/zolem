#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/zolem-smoke.XXXXXX")
BIN_PATH="${WORK_DIR}/zolem"
CONFIG_PATH="${WORK_DIR}/zolem.yaml"
CACHE_DIR="${WORK_DIR}/spec-cache"
FIXTURES_DIR="${WORK_DIR}/fixtures"
LOG_PATH="${WORK_DIR}/zolem.log"
RESPONSE_HEADERS="${WORK_DIR}/response.headers"
RESPONSE_BODY="${WORK_DIR}/response.body"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORK_DIR}"
}

trap cleanup EXIT INT TERM

fail() {
  printf 'smoke: %s\n' "$*" >&2
  if [[ -f "${LOG_PATH}" ]]; then
    printf '\nserver logs:\n' >&2
    cat "${LOG_PATH}" >&2
  fi
  exit 1
}

pick_port() {
  python3 - <<'PY'
import socket

sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
}

write_wasm() {
  local path=$1
  python3 - "$path" <<'PY'
import pathlib
import sys

data = bytes.fromhex(
    "0061736d0100000001070160027f7f017d030201000503010001071202066d656d6f72790200056d6174636800000a09010700430000803f0b"
)
pathlib.Path(sys.argv[1]).write_bytes(data)
PY
}

write_specs() {
  mkdir -p "${CACHE_DIR}"

  cat >"${CACHE_DIR}/anthropic-v1.json" <<'EOF'
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "max_tokens", "messages"],
  "properties": {
    "model": {"type": "string"},
    "max_tokens": {"type": "integer"},
    "messages": {"type": "array"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}
EOF

  cat >"${CACHE_DIR}/openai-v1.json" <<'EOF'
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "messages"],
  "properties": {
    "model": {"type": "string"},
    "messages": {"type": "array"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}
EOF

  cat >"${CACHE_DIR}/gemini-v1.json" <<'EOF'
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["contents"],
  "properties": {
    "contents": {"type": "array"},
    "generationConfig": {"type": "object"}
  },
  "additionalProperties": true
}
EOF

  cp "${CACHE_DIR}/gemini-v1.json" "${CACHE_DIR}/gemini-v1beta.json"
}

write_fixture() {
  local dir=$1
  local meta=$2
  local response=$3

  mkdir -p "${dir}"
  printf '%s' "${meta}" >"${dir}/meta.yaml"
  printf '%s' "${response}" >"${dir}/response.json"
  write_wasm "${dir}/match.wasm"
}

write_fixtures() {
  mkdir -p "${FIXTURES_DIR}"

  write_fixture "${FIXTURES_DIR}/anthropic-messages" \
'id: anthropic-messages
provider: anthropic
version: v1
stream: false
status: 200
' \
'{
  "id": "msg_fixture",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Fixture says hello from anthropic."}],
  "model": "claude-3-5-sonnet-20241022",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 5}
}'

  write_fixture "${FIXTURES_DIR}/openai-chat" \
'id: openai-chat
provider: openai
version: v1
stream: true
status: 200
' \
'{
  "id": "chatcmpl-fixture",
  "object": "chat.completion",
  "created": 1,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "Fixture says hello from openai."},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12}
}'

  write_fixture "${FIXTURES_DIR}/gemini-content" \
'id: gemini-content
provider: gemini
version: v1beta
stream: true
status: 200
' \
'{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [{"text": "Fixture says hello from gemini."}]
      },
      "finishReason": "STOP",
      "index": 0
    }
  ],
  "usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 5, "totalTokenCount": 14},
  "modelVersion": "gemini-2.0-flash"
}'
}

write_config() {
  local port=$1
  cat >"${CONFIG_PATH}" <<EOF
server:
  addr: 127.0.0.1:${port}
mode: fixture
specs:
  cache_dir: ${CACHE_DIR}
  refresh_interval: 1h
fixtures:
  dir: ${FIXTURES_DIR}
  watch: false
routes:
  - host: "*.api.anthropic.zolem.dev"
    provider: anthropic
    labels:
      tenant: "{1}"
  - host: "*.api.openai.zolem.dev"
    provider: openai
    labels:
      tenant: "{1}"
  - host: "*.api.openrouter.zolem.dev"
    provider: openai
    labels:
      tenant: "{1}"
  - host: "*.api.gemini.zolem.dev"
    provider: gemini
    labels:
      tenant: "{1}"
EOF
}

request() {
  local host=$1
  local path=$2
  local body=$3
  shift 3

  curl -sS \
    -D "${RESPONSE_HEADERS}" \
    -o "${RESPONSE_BODY}" \
    -X POST \
    "http://127.0.0.1:${PORT}${path}" \
    -H "Host: ${host}" \
    "$@" \
    --data "${body}"
}

status_code() {
  awk 'NR == 1 { print $2 }' "${RESPONSE_HEADERS}"
}

header_value() {
  local name=$1
  awk -F': ' -v wanted="${name}" 'BEGIN { IGNORECASE = 1 } $1 == wanted { sub(/\r$/, "", $2); print $2; exit }' "${RESPONSE_HEADERS}"
}

assert_status() {
  local expected=$1
  local got
  got=$(status_code)
  [[ "${got}" == "${expected}" ]] || fail "expected status ${expected}, got ${got}"
}

assert_header() {
  local name=$1
  local expected=$2
  local got
  got=$(header_value "${name}")
  [[ "${got}" == "${expected}" ]] || fail "expected header ${name}: ${expected}, got ${got:-<missing>}"
}

assert_json() {
  local mode=$1
  python3 - "${mode}" "${RESPONSE_BODY}" <<'PY'
import json
import sys
from pathlib import Path

mode = sys.argv[1]
body = json.loads(Path(sys.argv[2]).read_text())

if mode == "anthropic_json":
    assert body["type"] == "message"
    assert body["role"] == "assistant"
    assert body["content"][0]["text"] == "Fixture says hello from anthropic."
    assert body["stop_reason"] == "end_turn"
    assert body["usage"]["input_tokens"] == 10
    assert body["usage"]["output_tokens"] == 5
elif mode == "openrouter_json":
    assert body["object"] == "chat.completion"
    assert body["model"] == "gpt-4o"
    assert body["choices"][0]["message"]["role"] == "assistant"
    assert body["choices"][0]["finish_reason"] == "stop"
    assert body["usage"]["prompt_tokens"] == 7
    assert body["usage"]["completion_tokens"] == 5
    assert body["usage"]["total_tokens"] == 12
elif mode == "unmatched_host":
    assert body["zolem_error"] == "no route matched host: unknown.host.dev"
else:
    raise AssertionError(f"unknown mode {mode}")
PY
}

assert_sse() {
  local mode=$1
  python3 - "${mode}" "${RESPONSE_BODY}" <<'PY'
import json
import sys
from pathlib import Path

mode = sys.argv[1]
body = Path(sys.argv[2]).read_text().strip()
records = [chunk.strip() for chunk in body.split("\n\n") if chunk.strip()]
assert records, "expected at least one SSE record"

if mode == "anthropic_stream":
    assert any(record.startswith("event: message_start") for record in records)
    assert any(record.startswith("event: content_block_delta") for record in records)
    assert records[-1].startswith("event: message_stop"), records[-1]
elif mode == "openai_stream":
    assert records[-1] == "data: [DONE]", records[-1]
    payloads = []
    for record in records[:-1]:
      for line in record.splitlines():
        if line.startswith("data: "):
          payloads.append(json.loads(line[6:]))
          break
      else:
        raise AssertionError(f"missing data payload in {record!r}")
    assert payloads[0]["object"] == "chat.completion.chunk"
    assert payloads[0]["choices"][0]["delta"]["role"] == "assistant"
    assert payloads[-2]["choices"][0]["finish_reason"] == "stop"
    assert payloads[-1]["usage"]["total_tokens"] == 12
elif mode == "gemini_stream":
    payloads = []
    for record in records:
      for line in record.splitlines():
        if line.startswith("data: "):
          payloads.append(json.loads(line[6:]))
          break
      else:
        raise AssertionError(f"missing data payload in {record!r}")
    assert payloads[0]["candidates"][0]["finishReason"] == "NONE"
    assert payloads[-1]["candidates"][0]["finishReason"] == "STOP"
    combined = "".join(payload["candidates"][0]["content"]["parts"][0]["text"] for payload in payloads)
    assert combined == "Fixture says hello from gemini."
else:
    raise AssertionError(f"unknown mode {mode}")
PY
}

wait_for_ready() {
  local attempt
  for attempt in $(seq 1 150); do
    if request "acme.api.anthropic.zolem.dev" "/v1/messages" '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}' \
      -H "Content-Type: application/json" \
      -H "x-api-key: sk-test" >/dev/null 2>&1; then
      if [[ $(status_code) == "200" ]]; then
        return 0
      fi
    fi
    sleep 0.2
  done
  fail "service did not become ready"
}

PORT=$(pick_port)
write_specs
write_fixtures
write_config "${PORT}"

go build -o "${BIN_PATH}" ./cmd/zolem
"${BIN_PATH}" -config "${CONFIG_PATH}" >"${LOG_PATH}" 2>&1 &
SERVER_PID=$!

wait_for_ready

request "acme.api.anthropic.zolem.dev" "/v1/messages" '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}' \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-test"
assert_status 200
assert_header "Content-Type" "application/json"
assert_json anthropic_json

request "acme.api.anthropic.zolem.dev" "/v1/messages" '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}' \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-test" \
  -H "Accept: text/event-stream"
assert_status 200
assert_header "Content-Type" "text/event-stream"
assert_header "Cache-Control" "no-cache"
assert_header "Connection" "keep-alive"
assert_header "X-Accel-Buffering" "no"
assert_sse anthropic_stream

request "acme.api.openai.zolem.dev" "/v1/chat/completions" '{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}' \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-test" \
  -H "Accept: text/event-stream"
assert_status 200
assert_header "Content-Type" "text/event-stream"
assert_header "Cache-Control" "no-cache"
assert_header "Connection" "keep-alive"
assert_header "X-Accel-Buffering" "no"
assert_sse openai_stream

request "acme.api.openrouter.zolem.dev" "/v1/chat/completions" '{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}' \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-test"
assert_status 200
assert_header "Content-Type" "application/json"
assert_json openrouter_json

request "acme.api.gemini.zolem.dev" "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse" '{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}' \
  -H "Content-Type: application/json" \
  -H "x-goog-api-key: test-key"
assert_status 200
assert_header "Content-Type" "text/event-stream"
assert_header "Cache-Control" "no-cache"
assert_header "Connection" "keep-alive"
assert_header "X-Accel-Buffering" "no"
assert_sse gemini_stream

request "unknown.host.dev" "/anything" '{}' -H "Content-Type: application/json"
assert_status 502
assert_header "X-Zolem-Error" "true"
assert_json unmatched_host

printf 'smoke: ok\n'
