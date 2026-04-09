# Zolem

A local mock server for LLM provider APIs. Zolem validates requests against
real OpenAPI/discovery specs and returns synthetic responses, so you can develop
and test integrations against Anthropic, OpenAI, OpenRouter, and Gemini without
burning tokens.

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

## Quick start

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
