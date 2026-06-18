# Zolem

A local mock server for LLM provider APIs. Zolem validates requests against
real OpenAPI/discovery specs and returns synthetic responses, so you can develop
and test integrations against Anthropic, OpenAI, and Gemini without burning
tokens.

Zolem currently has two supported local execution paths:

- local runtime mode: a local admin server creates in-memory profiles and loopback listeners on demand
- fixed-listener mode: one loopback listener is pinned to one provider/profile at startup

## Supported local providers

- Anthropic
- OpenAI
- Gemini

Zolem also tracks OpenRouter's OpenAPI source for spec parsing and validation,
but local runtime listeners currently serve Anthropic, OpenAI, and Gemini.

## Response modes

| Mode | Description |
|------|-------------|
| `lorem` | Returns lorem-ipsum placeholder text (default) |
| `faker` | Returns randomized fake data |
| `fixture` | Returns static or templated responses selected by namespace `fixtures.yaml` expressions or a `selector.wasm` |
| `ollama` | Forwards generation to a local Ollama instance via its HTTP API |
| `wasm` | Runs a profile-supplied WebAssembly content generator |
| `error` | Local runtime only; always returns a provider-native error |

## Installation

The fastest path is `go install` (Go 1.26+):

```bash
go install github.com/ketang/zolem/cmd/zolem@latest
go install github.com/ketang/zolem/cmd/zolemc@latest
```

Pre-built binaries, Docker images, nightly builds, and artifact verification
(checksums, cosign, SBOM) are covered in **[INSTALL.md](INSTALL.md)**.

## Quick start: local runtime mode

Local runtime mode is for local development when you want to create or switch
mock behavior at runtime without editing a config file or restarting Zolem.

Start the local admin server:

```bash
zolem -local-admin-addr 127.0.0.1:18090
# If running from source: go run ./cmd/zolem -local-admin-addr 127.0.0.1:18090
```

Create a profile:

```bash
zolemc -admin-url http://127.0.0.1:18090 \
  profiles create demo -backend lorem
```

Create a listener bound to a provider and profile:

```bash
zolemc -admin-url http://127.0.0.1:18090 \
  listeners create openai-demo -addr 127.0.0.1:0 -provider openai -profile demo
```

The response includes a `base_url`. Point your client at that URL and keep the
provider path, method, body, and auth headers unchanged.

Inspect the listener:

```bash
zolemc -base-url http://127.0.0.1:19001 listener state
```

Health check the listener:

```bash
zolemc -base-url http://127.0.0.1:19001 listener health
```

Call a provider-compatible endpoint:

```bash
zolemc -base-url http://127.0.0.1:19001 \
  request -method POST \
  -path /v1/chat/completions \
  -H 'Authorization: Bearer sk-test' \
  -json-body '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'
```

Inspect or clear the listener's in-memory call history from the admin server:

```bash
zolemc -admin-url http://127.0.0.1:18090 \
  listeners calls list openai-demo -since 0

zolemc -admin-url http://127.0.0.1:18090 \
  listeners calls clear openai-demo
```

The list output shows call ID, method, path, status, latency, and timestamp.
Use `-json` to get the full `{"calls":[...]}` API payload. Streamed responses
show a `~` before the status, such as `~200`, when the status was sent before
streaming completed.

Current local runtime limitations:

- local-only, loopback addresses only
- in-memory only; profiles and listeners disappear on restart
- no auth or TTLs yet
- currently supported local runtime backends: `lorem`, `faker`, `fixture`, `ollama`, `wasm`, `error`
- `fixture` listeners require `-local-fixtures-dir` on the admin server or fixed listener
- `fixture_namespace` can scope a profile to a relative subdirectory under that fixtures root
- fixtures can use either `response.json` or `response.json.tmpl`; templates are validated at setup time and cannot read request body, query, path, or headers
- OpenAI Responses WebSocket fixtures use `version: v1-responses` and a `response.json` array of event objects, one event per outbound WebSocket frame
- `response_model_policy` controls the provider-visible `model` field for local runtime listeners

Local runtime also supports an `error` backend for deterministic client
error-path testing. See
[docs/local-runtime.md](docs/local-runtime.md)
for examples and behavior.

For no-egress Codex and Claude client smoke tests with Bubblewrap, see
[docs/local-runtime.md#no-egress-client-smoke-tests-with-bubblewrap](docs/local-runtime.md#no-egress-client-smoke-tests-with-bubblewrap).

For WASM-generator profiles, pass a compiled binary module through `zolemc`:

```bash
zolemc -admin-url http://127.0.0.1:18090 \
  profiles create wasm-demo \
  -wasm-module-file ./generator.wasm \
  -wasm-timeout-ms 250
```

`-wasm-module-file` reads and base64-encodes the module for the admin API and
selects the `wasm` backend when `-backend` is not set explicitly.
The generator ABI, input shape, and runtime constraints are documented in
[docs/local-runtime.md#wasm-backend](docs/local-runtime.md#wasm-backend).
Build examples for freestanding WASM modules are in
[docs/wasm-modules.md](docs/wasm-modules.md).

Optional local runtime TLS:

```bash
./scripts/generate-certs.sh

zolem \
  -local-admin-addr 127.0.0.1:18443 \
  -local-tls-cert certs/localhost.pem \
  -local-tls-key certs/localhost-key.pem
```

When the admin server is started with local TLS certs, you can request HTTPS
data-plane listeners by including `"tls": true` in the listener payload.

## Quick start: fixed-listener mode

Start one loopback listener pinned to a provider and backend:

```bash
zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-profile demo \
  -local-backend lorem
```

For fixture-backed fixed listeners, also pass `-local-fixtures-dir`.

For error-backed fixed listeners, pass `-local-backend error` together with
`-local-error-type` (`authentication`, `permission`, `invalid_request`,
`rate_limit`, or `server_error`). The listener returns that provider-native
error for every request. `-local-backend error` without `-local-error-type`
is rejected at startup.

Full guide: [docs/local-runtime.md](docs/local-runtime.md)

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
local runtime flags that use those files.

You can override the output directory with `CERT_DIR`:

```bash
CERT_DIR=~/.local/share/zolem/certs ./scripts/generate-certs.sh
```

The same certificate pair can now be used for local runtime mode:

- `-local-admin-addr ... -local-tls-cert ... -local-tls-key ...`
- `-local-addr ... -local-provider ... -local-tls-cert ... -local-tls-key ...`

Running Zolem in Docker (including fixtures and TLS mounts) is covered in
[INSTALL.md](INSTALL.md#option-2--docker).

## Documentation

- [INSTALL.md](INSTALL.md) — installation, Docker, nightly builds, artifact verification
- [docs/](docs/README.md) — full guides: local runtime, fixtures, WASM modules, E2E testing
- [AGENTS.md](AGENTS.md) — contributor and agent tooling (refute, Shatter, verification gate)
