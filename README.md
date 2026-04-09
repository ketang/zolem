# Zolem

A local mock server for LLM provider APIs. Zolem validates requests against
real OpenAPI/discovery specs and returns synthetic responses, so you can develop
and test integrations against Anthropic, OpenAI, OpenRouter, and Gemini without
burning tokens.

Zolem currently has two ways to run:

- static config mode: one server driven by `zolem.yaml`
- local runtime mode: a local admin server creates in-memory profiles and loopback listeners on demand

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

## Quick start: static config mode

```bash
go build -o zolem ./cmd/zolem
./zolem -config zolem.yaml
```

Example config:

```yaml
server:
  addr: ":8080"
mode: lorem
routes:
  - host: "localhost:8080"
    provider: anthropic
```

## Quick start: local runtime mode

Local runtime mode is for local development when you want to create or switch
mock behavior at runtime without editing a config file or restarting Zolem.

Start the local admin server:

```bash
go run ./cmd/zolem -local-admin-addr 127.0.0.1:18090
```

Create a profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"lorem"}' \
  http://127.0.0.1:18090/_zolem/profiles/demo
```

Create a listener bound to a provider and profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/openai-demo
```

The response includes a `base_url`. Point your client at that URL and keep the
provider path, method, body, and auth headers unchanged.

Inspect the listener:

```bash
curl http://127.0.0.1:19001/_zolem/state
```

Call a provider-compatible endpoint:

```bash
curl -X POST \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19001/v1/chat/completions
```

Current local runtime limitations:

- local-only, loopback addresses only
- in-memory only; profiles and listeners disappear on restart
- no auth or TTLs yet
- currently supported local runtime backends: `lorem`, `faker`
- local runtime listeners are HTTP-only for now; TLS is planned next

Full guide: [docs/local-runtime.md](/home/ketan/.codex/memories/worktrees/zolem-local-runtime-config-design/docs/local-runtime.md)

## Verification

Run the end-to-end local runtime check:

```bash
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

Note:

- the certificate generation flow exists today
- the new local runtime admin/data-plane flow is still HTTP-only in the current branch and will pick up TLS in the next slice
