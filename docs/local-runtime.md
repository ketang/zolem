# Local Runtime Mode

Local runtime mode lets you create mock behavior at runtime through a localhost
control plane instead of restarting Zolem for each setup change.

This mode is designed for local development only right now:

- profiles are stored in memory
- listeners are stored in memory
- all addresses must bind to loopback
- there is no auth or TTL enforcement yet
- the current local runtime backends are `lorem`, `faker`, `fixture`, and `ollama`
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

- `backend`: `lorem`, `faker`, `fixture`, or `ollama`
- `fixture_namespace`: optional relative subdirectory under `-local-fixtures-dir`
- `response_model_policy`: `echo_request`, `force_literal`, or `force_backend`
- `response_model`: required when `response_model_policy` is `force_literal`
- `backend_model`: used when `response_model_policy` is `force_backend`; also selects the model sent to Ollama when `backend` is `ollama`
- `ollama_upstream`: base URL of the Ollama server (default `http://localhost:11434`); must be `http` or `https`

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
- `fixture` profiles only become usable when the admin server or fixed listener was started with `-local-fixtures-dir`
- `fixture_namespace` must be a normalized relative subdirectory such as `team-a` or `team-a/smoke`
- `response_model_policy=force_literal` requires `response_model`
- `response_model_policy=force_backend` uses `backend_model` when present and otherwise falls back to the request model

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

Each fixture still needs the normal files under a subdirectory:

- `meta.yaml`
- `response.json`
- `match.wasm`

Minimal example:

```text
my-fixtures/
└── anthropic-demo/
    ├── meta.yaml
    ├── response.json
    └── match.wasm
```

Example `meta.yaml`:

```yaml
id: anthropic-demo
provider: anthropic
version: v1
status: 200
```

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

If the request matches a fixture, Zolem returns `response.json`. If no fixture
matches, provider behavior falls back to generated output.

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

Use [scripts/generate-certs.sh](/home/ketan/.codex/memories/worktrees/zolem-local-runtime-config-design/scripts/generate-certs.sh) to generate local certificates with `mkcert`.

It creates:

- `certs/localhost.pem`
- `certs/localhost-key.pem`

Those files can be used for:

- the local admin server
- fixed local listener mode
- dynamic local runtime listeners created with `"tls": true`
