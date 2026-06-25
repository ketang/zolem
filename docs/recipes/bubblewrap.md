# No-Egress Client Smoke Tests With Bubblewrap

Use Bubblewrap when you want to prove a real client can reach Zolem but cannot
reach the upstream provider. `bwrap --unshare-net` creates a separate network
namespace with its own loopback device, so `127.0.0.1` inside the sandbox is not
the host loopback. Start Zolem and the client inside the same Bubblewrap command
when the client points at `127.0.0.1`.

Build a local Zolem binary first so the sandboxed command does not need network
access for Go module downloads:

```bash
mkdir -p ./.tmp
go build -o ./.tmp/zolem ./cmd/zolem
```

Codex recipe for the OpenAI Responses WebSocket path:

```bash
bwrap --unshare-net \
  --dev-bind / / \
  --proc /proc \
  --tmpfs /tmp \
  --setenv HOME /tmp/home \
  --setenv CODEX_HOME /tmp/codex-home \
  --setenv OPENAI_API_KEY sk-test \
  --chdir "$PWD" \
  bash -lc '
    set -euo pipefail
    mkdir -p "$HOME" "$CODEX_HOME" /tmp/zolem-fixtures/turn

    cat > /tmp/zolem-fixtures/fixtures.yaml <<'"'"'YAML'"'"'
provider: openai
version: v1-responses
fixtures:
  - expression: '"'"'true'"'"'
    fixture: turn
YAML

    cat > /tmp/zolem-fixtures/turn/meta.yaml <<'"'"'YAML'"'"'
id: turn
provider: openai
version: v1-responses
status: 200
YAML

    cat > /tmp/zolem-fixtures/turn/response.json <<'"'"'JSON'"'"'
[
  {"type":"response.created","sequence_number":0,"response":{"id":"resp_zolem","status":"in_progress","output":[]}},
  {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message","id":"msg_zolem","role":"assistant","content":[],"status":"in_progress"}},
  {"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_zolem","output_index":0,"content_index":0,"delta":"zolem ok"},
  {"type":"response.output_text.done","sequence_number":3,"item_id":"msg_zolem","output_index":0,"content_index":0,"text":"zolem ok"},
  {"type":"response.output_item.done","sequence_number":4,"output_index":0,"item":{"type":"message","id":"msg_zolem","role":"assistant","content":[{"type":"output_text","text":"zolem ok"}],"status":"completed"}},
  {"type":"response.completed","sequence_number":5,"response":{"id":"resp_zolem","status":"completed","output":[]}}
]
JSON

    ./.tmp/zolem \
      -local-provider openai \
      -local-addr 127.0.0.1:19001 \
      -local-backend fixture \
      -local-fixtures-dir /tmp/zolem-fixtures \
      -local-calls-file /tmp/zolem-codex-calls.jsonl \
      >/tmp/zolem-codex.log 2>&1 &
    zolem_pid=$!
    trap '"'"'kill "$zolem_pid" 2>/dev/null || true'"'"' EXIT

    until curl -fsS http://127.0.0.1:19001/_zolem/health >/dev/null; do
      sleep 0.1
    done

    codex exec \
      --ignore-user-config \
      --ephemeral \
      --skip-git-repo-check \
      --enable responses_websockets_v2 \
      -m gpt-5-codex \
      -c '"'"'model_provider="zolem"'"'"' \
      -c '"'"'model_providers.zolem={ name="Zolem", base_url="http://127.0.0.1:19001/v1", wire_api="responses", env_key="OPENAI_API_KEY", supports_websockets=true }'"'"' \
      "Reply exactly: zolem ok"

    grep -q '"'"'"path":"/v1/responses"'"'"' /tmp/zolem-codex-calls.jsonl
    grep -q '"'"'"status":101'"'"' /tmp/zolem-codex-calls.jsonl
  '
```

Claude recipe for the Anthropic Messages path:

```bash
bwrap --unshare-net \
  --dev-bind / / \
  --proc /proc \
  --tmpfs /tmp \
  --setenv HOME /tmp/home \
  --setenv ANTHROPIC_API_KEY sk-test \
  --setenv ANTHROPIC_BASE_URL http://127.0.0.1:19002 \
  --chdir "$PWD" \
  bash -lc '
    set -euo pipefail
    mkdir -p "$HOME"

    ./.tmp/zolem \
      -local-provider anthropic \
      -local-addr 127.0.0.1:19002 \
      -local-backend lorem \
      -local-calls-file /tmp/zolem-claude-calls.jsonl \
      >/tmp/zolem-claude.log 2>&1 &
    zolem_pid=$!
    trap '"'"'kill "$zolem_pid" 2>/dev/null || true'"'"' EXIT

    until curl -fsS http://127.0.0.1:19002/_zolem/health >/dev/null; do
      sleep 0.1
    done

    claude \
      --bare \
      --print \
      --no-session-persistence \
      --model claude-sonnet-4-6 \
      "Reply exactly: zolem ok"

    grep -q '"'"'"path":"/v1/messages"'"'"' /tmp/zolem-claude-calls.jsonl
    grep -q '"'"'"status":200'"'"' /tmp/zolem-claude-calls.jsonl
  '
```

These recipes use fake provider keys, isolated client state, and Zolem's
`-local-calls-file` as the assertion surface. If a client ignores the local base
URL and tries `api.openai.com` or `api.anthropic.com`, the connection fails at
the network namespace boundary before it can leave the machine. The
`--dev-bind / /` mount keeps the example focused on network egress; replace it
with narrower read-only binds if you also want filesystem isolation.
