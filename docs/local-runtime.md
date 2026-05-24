# Local Runtime Mode

Local runtime mode lets you create mock behavior at runtime through a localhost
control plane instead of restarting Zolem for each setup change.

This mode is designed for local development only right now:

- profiles are stored in memory
- listeners are stored in memory
- all addresses must bind to loopback
- there is no auth or TTL enforcement yet
- the current local runtime backends are `lorem`, `faker`, `fixture`, `ollama`, `wasm`, and `error`
- TLS is available when you provide local cert and key files

## Concepts

There are two resources:

- profile: describes the response behavior, such as `lorem` or `faker`
- listener: binds one local address to one provider and one profile

Each listener exposes:

- provider-compatible endpoints such as `/v1/chat/completions` or `/v1/messages`
- a local health endpoint at `/_zolem/health`
- a local introspection endpoint at `/_zolem/state`

Profile fields you can use today:

- `backend`: `lorem`, `faker`, `fixture`, `ollama`, `wasm`, or `error`
- `error_type`: required when `backend` is `error`
- `fixture_namespace`: optional relative subdirectory under `-local-fixtures-dir`
- `response_model_policy`: `echo_request`, `force_literal`, or `force_backend`
- `response_model`: required when `response_model_policy` is `force_literal`
- `backend_model`: used when `response_model_policy` is `force_backend`; also selects the model sent to Ollama when `backend` is `ollama`
- `ollama_upstream`: base URL of the Ollama server (default `http://localhost:11434`); must be `http` or `https`
- `wasm_module_base64`: required when `backend` is `wasm`; base64-encoded binary WASM generator module
- `wasm_generate_timeout_ms`: optional timeout for one WASM generation interaction; defaults to 100ms and must be between 1 and 5000 when set
- `stream_delay`: optional streaming delay config for generated chunks

## Error Backend

The `error` backend is for deterministic client error-path testing.

Goals:

- an error profile should deterministically fail every provider request on that listener
- provider error payloads should always be high fidelity when Zolem emits an error
- profile config should stay semantic and provider-agnostic

Profile shape:

```json
{
  "backend": "error",
  "error_type": "authentication"
}
```

Supported `error_type` values:

- `authentication`
- `permission`
- `invalid_request`
- `rate_limit`
- `server_error`

Behavior:

- `backend=error` means the listener always returns an error
- the selected `error_type` maps to a provider-native status code and body
- Zolem owns the exact message and envelope shape for fidelity
- profile config should not override the error message, because custom messages reduce fidelity with the reference provider API
- success backends such as `lorem`, `faker`, and `fixture` are bypassed entirely for error profiles

Example:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"error","error_type":"rate_limit"}' \
  http://127.0.0.1:18090/_zolem/profiles/rate-limit-demo
```

Bind that profile to a listener:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"rate-limit-demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/openai-rate-limit
```

Every request sent to that listener's provider endpoint returns the configured
provider-native error instead of a generated or fixture-backed success response.

## Start The Admin Server

Run:

```bash
go run ./cmd/zolem -local-admin-addr 127.0.0.1:18090
```

To run the admin server over HTTPS:

```bash
./scripts/generate-certs.sh

go run ./cmd/zolem \
  -local-admin-addr 127.0.0.1:18443 \
  -local-tls-cert certs/localhost.pem \
  -local-tls-key certs/localhost-key.pem
```

Health check:

```bash
curl http://127.0.0.1:18090/_zolem/health
```

HTTPS health check:

```bash
curl https://127.0.0.1:18443/_zolem/health
```

Expected response:

```json
{"status":"ok"}
```

## Manage Profiles

Create a `lorem` profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"lorem"}' \
  http://127.0.0.1:18090/_zolem/profiles/demo
```

Create a `faker` profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"faker"}' \
  http://127.0.0.1:18090/_zolem/profiles/faker-demo
```

Create a `fixture` profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"fixture"}' \
  http://127.0.0.1:18090/_zolem/profiles/fixture-demo
```

Create an `error` profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"error","error_type":"rate_limit"}' \
  http://127.0.0.1:18090/_zolem/profiles/error-demo
```

Create a profile that forces the returned `model` field:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"lorem","response_model_policy":"force_literal","response_model":"mock-openai-model"}' \
  http://127.0.0.1:18090/_zolem/profiles/openai-shaped
```

List profiles:

```bash
curl http://127.0.0.1:18090/_zolem/profiles
```

Delete a profile:

```bash
curl -X DELETE http://127.0.0.1:18090/_zolem/profiles/demo
```

Notes:

- a profile cannot be deleted while a listener is still using it
- unsupported backends are rejected at profile creation time
- `error` profiles require `error_type`
- `fixture` profiles only become usable when the admin server or fixed listener was started with `-local-fixtures-dir`
- `error_type` is only valid when `backend=error`
- `fixture_namespace` must be a normalized relative subdirectory such as `team-a` or `team-a/smoke`
- `response_model_policy=force_literal` requires `response_model`
- `response_model_policy=force_backend` uses `backend_model` when present and otherwise falls back to the request model
- `wasm_module_base64` and `wasm_generate_timeout_ms` are only valid when `backend=wasm`

## Manage Listeners

Create a listener on an automatically assigned port:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/openai-demo
```

Example response:

```json
{
  "name": "openai-demo",
  "addr": "127.0.0.1:19001",
  "provider": "openai",
  "profile": "demo",
  "backend": "lorem",
  "base_url": "http://127.0.0.1:19001"
}
```

Create an HTTPS listener when the admin server was started with local TLS certs:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"demo","tls":true}' \
  https://127.0.0.1:18443/_zolem/listeners/openai-demo
```

List listeners:

```bash
curl http://127.0.0.1:18090/_zolem/listeners
```

Delete a listener:

```bash
curl -X DELETE http://127.0.0.1:18090/_zolem/listeners/openai-demo
```

Listener rules:

- the address must be loopback-only
- the provider must currently be `openai`, `anthropic`, or `gemini`
- the referenced profile must already exist
- `tls: true` requires the admin server to have been started with `-local-tls-cert` and `-local-tls-key`
- `fixture` listeners require the admin server to have been started with `-local-fixtures-dir`

## Fixture Backend

The `fixture` backend uses the existing fixture loader and matcher. Start the
admin server with a fixture root:

```bash
go run ./cmd/zolem \
  -local-admin-addr 127.0.0.1:18090 \
  -local-fixtures-dir ./testdata/fixtures
```

### Call Recording

In fixed-listener mode the runtime can append every captured request/response
pair to a JSONL file. One JSON object per line; the file is opened with
`O_APPEND|O_CREATE`, fsynced after each record, and re-opening an existing
file appends rather than truncates.

| Flag | Default | Purpose |
| --- | --- | --- |
| `-local-calls-file <path>` | `""` (disabled) | Path to the JSONL file. Empty disables recording. |
| `-local-record-request-body-cap-bytes <n>` | `262144` | Maximum bytes of request body recorded per call. Excess is counted in `body_truncated_bytes`. |
| `-local-record-response-body-cap-bytes <n>` | `262144` | Maximum bytes of response body recorded per call. Same truncation semantics. |
| `-local-record-stream-event-cap <n>` | `1024` | Maximum SSE events recorded per streamed response. Excess is counted in `events_truncated`. |

Example:

```bash
go run ./cmd/zolem \
  -local-provider anthropic \
  -local-addr 127.0.0.1:8080 \
  -local-calls-file ./zolem-calls.jsonl
```

Each line is a `RecordedCall` (see `cmd/zolem/recording.go`) with monotonic
`call_id`, listener identity, timing, request, and response. Caps only bound
what is recorded — the full request/response is still served to the caller.

Each fixture still needs the normal files under a subdirectory:

- `meta.yaml`
- `response.json` or `response.json.tmpl`

Selection is configured at the namespace level via `fixtures.yaml` (recommended)
or a namespace-level `selector.wasm`. The historical per-fixture `match.cel`
expression inside `meta.yaml` and per-fixture `match.wasm` file are deprecated
(see "Deprecated: per-fixture matchers" below) but continue to work and emit a
startup warning when a namespace has neither `fixtures.yaml` nor
`selector.wasm`.

Minimal example:

```text
my-fixtures/
└── anthropic-demo/
    ├── meta.yaml
    └── response.json
```

Example `meta.yaml`:

```yaml
id: anthropic-demo
provider: anthropic
version: v1
status: 200
match:
  cel: 'body["model"] == "claude-3-5-sonnet-20241022"'
  score: 1
```

### Deprecated: per-fixture matchers

Per-fixture `match.cel` (inside `meta.yaml`) and `match.wasm` are deprecated.
They still load and serve traffic, but Zolem prints a startup warning for each
fixture that uses them when the namespace has no `fixtures.yaml` and no
`selector.wasm`. New fixtures should use a namespace-level `fixtures.yaml`
entry; complex routing logic that does not fit CEL should use a namespace-level
`selector.wasm`.

Migration example. A fixture directory whose `meta.yaml` used to embed a CEL
matcher:

```yaml
# my-fixtures/team-a/anthropic-demo/meta.yaml (deprecated)
id: anthropic-demo
provider: anthropic
version: v1
status: 200
match:
  cel: 'body["model"] == "claude-3-5-sonnet-20241022"'
  score: 1
```

becomes a fixture with no per-fixture matcher plus a namespace-level
`fixtures.yaml`:

```yaml
# my-fixtures/team-a/anthropic-demo/meta.yaml
id: anthropic-demo
provider: anthropic
version: v1
status: 200
```

```yaml
# my-fixtures/team-a/fixtures.yaml
provider: anthropic
version: v1
fixtures:
  - expression: 'body["model"] == "claude-3-5-sonnet-20241022"'
    fixture: anthropic-demo
```

Per-fixture `match.wasm` migrates the same way: remove `match.wasm` from the
fixture directory, then either add the routing expression to `fixtures.yaml`
or, for logic that requires WASM, drop a namespace-level `selector.wasm` next
to the fixture subdirectories.

CEL is the recommended matcher for common request predicates. `match.cel`
must evaluate to a boolean. When it returns `true`, the fixture is a candidate
with `match.score`; when it returns `false`, the fixture is skipped. The score
defaults to `1`, must be finite and non-negative, and participates in the same
highest-score selection used by WASM matchers. Ties still use fixture load
order.

CEL matchers can read:

- `provider` as a string
- `version` as a string
- `labels` as `map(string, string)`
- `body` as the validated request JSON

Use bracket access for request JSON and labels:

```cel
body["model"] == "gpt-4o-mini" &&
labels["tenant"] == "acme" &&
body["messages"][0]["content"] == "refund"
```

For custom scoring or logic that does not fit CEL, the deprecated path is a
per-fixture `match.wasm` file. A fixture cannot define both `match.cel` and
`match.wasm`; Zolem rejects that fixture at load time. A fixture with neither
matcher loads but will never match. New configurations should prefer a
namespace-level `selector.wasm` instead.

For a templated fixture, replace `response.json` with `response.json.tmpl`.
Zolem parses, executes, and validates the rendered JSON when the fixture-backed
listener is created. Bad template syntax or invalid rendered JSON fails startup
or hot reload before the fixture can serve traffic.

Template example:

```json
{
  "id": {{ json .Faker.UUID }},
  "object": "chat.completion",
  "created": 1,
  "model": {{ json .Runtime.BackendModel }},
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": {{ json (printf "fixture %s request %d render %d" .Fixture.ID .Sequence.ProfileRequest .Sequence.TemplateRender) }}
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
}
```

Templated fixture rules:

- templates use Go `text/template`
- use the `json` helper for dynamic values so the rendered response stays valid JSON
- templates can call the full `gofakeit/v7` faker surface through `.Faker`
- templates cannot read request body, query parameters, path parameters, or headers
- Zolem provides the current UTC time as `.Now`
- `.Sequence.ProfileRequest` increments once per request handled by the profile
- `.Sequence.TemplateRender` increments once per templated fixture render for the profile

Template context fields:

- `.Runtime.ListenerName`
- `.Runtime.ListenerProvider`
- `.Runtime.ProfileName`
- `.Runtime.BackendModel`
- `.Runtime.FixtureNamespace`
- `.Runtime.TLS`
- `.Fixture.ID`
- `.Fixture.Provider`
- `.Fixture.Version`
- `.Fixture.Stream`
- `.Fixture.Status`
- `.Template.Seed`

To make faker output deterministic, set `template_seed` in `meta.yaml`:

```yaml
id: openai-templated
provider: openai
version: v1
status: 200
template_seed: 42
```

When `template_seed` is absent, Zolem chooses a fresh seed for each template
render. Setup-time validation uses a fixed validation seed and does not advance
live profile counters.

Example fixture listener flow:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"fixture","fixture_namespace":"team-a"}' \
  http://127.0.0.1:18090/_zolem/profiles/fixture-demo

curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"anthropic","profile":"fixture-demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/anthropic-fixture
```

With that profile, Zolem loads fixtures from:

```text
<local-fixtures-dir>/team-a
```

Then call the normal provider endpoint on the returned `base_url`:

```bash
curl -X POST \
  -H 'x-api-key: test-key' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19101/v1/messages
```

If the request matches a fixture, Zolem returns `response.json` or the rendered
`response.json.tmpl`. If no fixture matches, provider behavior falls back to
generated output.

## WASM Backend

The `wasm` backend lets a profile provide a freestanding WebAssembly content
generator. The generator returns assistant content only; Zolem still owns the
OpenAI, Anthropic, and Gemini response envelopes and streaming wire formats.

Profile shape:

```json
{
  "backend": "wasm",
  "wasm_module_base64": "AGFzbQEAAA...",
  "wasm_generate_timeout_ms": 100,
  "stream_delay": {
    "mode": "fixed",
    "ms": 75
  }
}
```

Create the same profile with `zolemc` from a binary `.wasm` file:

```bash
go run ./cmd/zolemc -admin-url http://127.0.0.1:18090 \
  profiles create wasm-demo \
  -wasm-module-file ./generator.wasm \
  -wasm-timeout-ms 100
```

`-wasm-module-file` reads and base64-encodes the module for the profile payload.
When `-backend` is not explicitly set, it selects `backend=wasm`; explicit
non-WASM backends are rejected with the WASM flags.

For deterministic random streaming pauses:

```json
{
  "backend": "wasm",
  "wasm_module_base64": "AGFzbQEAAA...",
  "stream_delay": {
    "mode": "random",
    "seed": 12345,
    "min_ms": 40,
    "max_ms": 250
  }
}
```

The WASM generator receives the same JSON input shape as fixture `match.wasm`:

```json
{
  "provider": "openai",
  "version": "v1",
  "labels": {},
  "body": {}
}
```

The module must return UTF-8 JSON bytes that decode to `[]string`. For
non-streaming requests, Zolem joins the strings into one assistant message. For
streaming requests, Zolem emits each string as one provider-native content
delta, including empty strings.

Required exports:

```text
memory
alloc(len: i32) -> i32
dealloc(ptr: i32, len: i32)
generate(input_ptr: i32, input_len: i32) -> i32
result_ptr(handle: i32) -> i32
result_len(handle: i32) -> i32
result_free(handle: i32)
```

Constraints:

- modules must be binary WASM encoded as base64; WAT text is not accepted
- modules must not import anything, including WASI or `env`
- the exported surface must be exactly the required ABI exports above
- Zolem compiles the module at profile write/listener setup time and creates a fresh WASM instance per request
- each instance has a fixed 16 MiB host memory limit
- result bytes are capped at 1 MiB
- decoded result arrays are capped at 4096 strings
- empty arrays and empty strings are valid
- WASM validation, runtime, timeout, or result-shape failures return a Zolem internal error response

## Ollama Backend

The `ollama` backend forwards generation to a local Ollama instance using its
OpenAI-compatible HTTP API. Unlike the other backends, there is no fallback: if
Ollama is unreachable or returns an error, the request fails with a
provider-appropriate 502 error.

Ollama must be running and serving its HTTP API (default `http://localhost:11434`).

Create an ollama profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"ollama","backend_model":"gemma3:4b"}' \
  http://127.0.0.1:18090/_zolem/profiles/ollama-demo
```

Create a listener and call it:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"ollama-demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/openai-ollama

curl -X POST \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19001/v1/chat/completions
```

The request arrives shaped as OpenAI, Anthropic, or Gemini (depending on the
listener's provider), and Zolem translates it into an OpenAI-compatible chat
completion request for Ollama. The response text is wrapped back in the
provider's native envelope.

Streaming works: if the incoming request asks for streaming, Zolem streams from
Ollama and re-emits each token in the provider's SSE format.

Profile options:

- `backend_model`: the model Ollama should use (e.g. `gemma3:4b`). If omitted, the model from the incoming request is forwarded as-is.
- `ollama_upstream`: base URL of the Ollama server. Defaults to `http://localhost:11434`.

To point at a non-default Ollama instance:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"ollama","backend_model":"gemma3:4b","ollama_upstream":"http://192.168.1.50:11434"}' \
  http://127.0.0.1:18090/_zolem/profiles/remote-ollama
```

Fixed listener mode also supports the ollama backend:

```bash
go run ./cmd/zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-profile demo \
  -local-backend ollama
```

Limitations:

- Only text content is translated. Tool calls, function definitions, and multimodal content are not forwarded.
- Gemini `systemInstruction` is not translated (the field is not in Zolem's Gemini request type).

## Response Model Policy

Local runtime listeners can shape the provider-visible `model` field without
changing the incoming request.

Policies:

- `echo_request`: return the same model the client requested
- `force_literal`: always return `response_model`
- `force_backend`: return `backend_model` when set

Example:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"lorem","response_model_policy":"force_backend","backend_model":"gpt-4.1-mini"}' \
  http://127.0.0.1:18090/_zolem/profiles/openai-backend-shaped
```

That profile still serves generated output locally, but its responses will say
`"model": "gpt-4.1-mini"` instead of echoing the incoming request model.

## Use A Listener

After listener creation, use the returned `base_url` as the client base URL.
Everything else should remain provider-shaped.

OpenAI example:

```bash
curl -X POST \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19001/v1/chat/completions
```

OpenAI HTTPS example:

```bash
curl -X POST \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
  https://127.0.0.1:19443/v1/chat/completions
```

Anthropic example:

```bash
curl -X POST \
  -H 'x-api-key: test-key' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19101/v1/messages
```

## Introspection

Listener health:

```bash
curl http://127.0.0.1:19001/_zolem/health
```

Expected response:

```json
{"status":"ok"}
```

Each listener exposes:

```bash
curl http://127.0.0.1:19001/_zolem/state
```

Example response:

```json
{
  "provider": "openai",
  "profile": "demo",
  "backend": "lorem",
  "listener": "127.0.0.1:19001"
}
```

## End-To-End Verification

Run the repo script:

```bash
./scripts/test-local-runtime.sh
```

Run the same flow over HTTPS:

```bash
LOCAL_TLS_CERT=certs/localhost.pem \
LOCAL_TLS_KEY=certs/localhost-key.pem \
LISTENER_TLS=1 \
./scripts/test-local-runtime.sh
```

This script:

- runs the package tests
- starts the local admin server
- creates a demo profile
- creates either an OpenAI or Anthropic listener depending on backend
- verifies `/_zolem/health`
- verifies `/_zolem/state`
- calls a provider-compatible endpoint
- deletes the listener and profile

To verify fixture mode:

```bash
PROFILE_BACKEND=fixture ./scripts/test-local-runtime.sh
```

## Certificates

Use [scripts/generate-certs.sh](../scripts/generate-certs.sh) to generate local certificates with `mkcert`.

It creates:

- `certs/localhost.pem`
- `certs/localhost-key.pem`

Those files can be used for:

- the local admin server
- fixed local listener mode
- dynamic local runtime listeners created with `"tls": true`
