# Zolem

A local mock server for LLM provider APIs. Zolem validates requests against
real OpenAPI/discovery specs and returns synthetic responses, so you can develop
and test integrations against Anthropic, OpenAI, OpenRouter, and Gemini without
burning tokens.

Zolem currently has two supported local execution paths:

- local runtime mode: a local admin server creates in-memory profiles and loopback listeners on demand
- fixed-listener mode: one loopback listener is pinned to one provider/profile at startup

## Supported providers

- Anthropic
- OpenAI
- OpenRouter
- Gemini

## Response modes

| Mode | Description |
|------|-------------|
| `lorem` | Returns lorem-ipsum placeholder text (default) |
| `faker` | Returns randomized fake data |
| `fixture` | Returns responses defined by WASM-matched fixture files |
| `ollama` | Forwards generation to a local Ollama instance via its HTTP API |
| `error` | Local runtime only; always returns a provider-native error |

## Quick start: local runtime mode

Local runtime mode is for local development when you want to create or switch
mock behavior at runtime without editing a config file or restarting Zolem.

Start the local admin server:

```bash
go run ./cmd/zolem -local-admin-addr 127.0.0.1:18090
```

Create a profile:

```bash
go run ./cmd/zolemc -admin-url http://127.0.0.1:18090 \
  profiles create demo -backend lorem
```

Create a listener bound to a provider and profile:

```bash
go run ./cmd/zolemc -admin-url http://127.0.0.1:18090 \
  listeners create openai-demo -addr 127.0.0.1:0 -provider openai -profile demo
```

The response includes a `base_url`. Point your client at that URL and keep the
provider path, method, body, and auth headers unchanged.

Inspect the listener:

```bash
go run ./cmd/zolemc -base-url http://127.0.0.1:19001 listener state
```

Health check the listener:

```bash
go run ./cmd/zolemc -base-url http://127.0.0.1:19001 listener health
```

Call a provider-compatible endpoint:

```bash
go run ./cmd/zolemc -base-url http://127.0.0.1:19001 \
  request -method POST \
  -path /v1/chat/completions \
  -H 'Authorization: Bearer sk-test' \
  -json-body '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'
```

Current local runtime limitations:

- local-only, loopback addresses only
- in-memory only; profiles and listeners disappear on restart
- no auth or TTLs yet
- currently supported local runtime backends: `lorem`, `faker`, `fixture`, `ollama`, `error`
- `fixture` listeners require `-local-fixtures-dir` on the admin server or fixed listener
- `fixture_namespace` can scope a profile to a relative subdirectory under that fixtures root
- `response_model_policy` controls the provider-visible `model` field for local runtime listeners

Local runtime also supports an `error` backend for deterministic client
error-path testing. See
[docs/local-runtime.md](/home/ketan/.codex/memories/worktrees/zolem-high-fidelity-errors/docs/local-runtime.md)
for examples and behavior.

Optional local runtime TLS:

```bash
./scripts/generate-certs.sh

go run ./cmd/zolem \
  -local-admin-addr 127.0.0.1:18443 \
  -local-tls-cert certs/localhost.pem \
  -local-tls-key certs/localhost-key.pem
```

When the admin server is started with local TLS certs, you can request HTTPS
data-plane listeners by including `"tls": true` in the listener payload.

## Quick Start: Fixed Listener Mode

Start one loopback listener pinned to a provider and backend:

```bash
go run ./cmd/zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-profile demo \
  -local-backend lorem
```

For fixture-backed fixed listeners, also pass `-local-fixtures-dir`.

Full guide: [docs/local-runtime.md](/home/ketan/.codex/memories/worktrees/zolem-remove-static-config-mode/docs/local-runtime.md)

## Verification

Run the end-to-end local runtime check:

```bash
./scripts/test-local-runtime.sh
```

To verify fixture mode as well:

```bash
PROFILE_BACKEND=fixture ./scripts/test-local-runtime.sh
```

To verify the HTTPS path as well:

```bash
LOCAL_TLS_CERT=certs/localhost.pem \
LOCAL_TLS_KEY=certs/localhost-key.pem \
LISTENER_TLS=1 \
./scripts/test-local-runtime.sh
```

To verify fixture mode over HTTPS:

```bash
LOCAL_TLS_CERT=certs/localhost.pem \
LOCAL_TLS_KEY=certs/localhost-key.pem \
LISTENER_TLS=1 \
PROFILE_BACKEND=fixture \
./scripts/test-local-runtime.sh
```

## TLS for local development

Zolem supports TLS so clients that require HTTPS work out of the box locally.

### Generate certificates

The `scripts/generate-certs.sh` script uses [mkcert](https://github.com/FiloSottile/mkcert)
to create a locally-trusted certificate covering `localhost`, `127.0.0.1`, and `::1` (IPv6).

```bash
./scripts/generate-certs.sh
```

This writes `certs/localhost.pem` and `certs/localhost-key.pem`, then prints the
config snippet to add to your YAML:

```yaml
server:
  addr: ":8443"
  tls:
    cert: certs/localhost.pem
    key: certs/localhost-key.pem
```

You can override the output directory with `CERT_DIR`:

```bash
CERT_DIR=~/.local/share/zolem/certs ./scripts/generate-certs.sh
```

The same certificate pair can now be used for local runtime mode:

- `-local-admin-addr ... -local-tls-cert ... -local-tls-key ...`
- `-local-addr ... -local-provider ... -local-tls-cert ... -local-tls-key ...`
