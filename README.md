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
| `fixture` | Returns static or templated responses selected by CEL or WASM fixture matchers |
| `ollama` | Forwards generation to a local Ollama instance via its HTTP API |
| `wasm` | Runs a profile-supplied WebAssembly content generator |
| `error` | Local runtime only; always returns a provider-native error |

## Installation

**Supported platforms:**
| | linux/amd64 | linux/arm64 | darwin/arm64 |
|---|---|---|---|
| Binary | ✓ | ✓ | ✓ |
| Docker | ✓ | ✓ | — |

### Binary (recommended)

Download the archive for your platform from
[github.com/ketang/zolem/releases/latest](https://github.com/ketang/zolem/releases/latest).

| Platform | Archive |
|----------|---------|
| Linux amd64 | `zolem-<version>-linux-amd64.tar.gz` |
| Linux arm64 | `zolem-<version>-linux-arm64.tar.gz` |
| macOS arm64 | `zolem-<version>-darwin-arm64.tar.gz` |

Extract and place both binaries on your `PATH`:

```bash
tar -xzf zolem-<version>-<os>-<arch>.tar.gz
sudo mv zolem zolemc /usr/local/bin/
```

Verify checksums:

```bash
sha256sum -c checksums.txt
```

See [Artifact verification](#artifact-verification) for cosign signature and SBOM instructions.

### Docker

```bash
docker pull ghcr.io/ketang/zolem:v0.1.0   # pinned
docker pull ghcr.io/ketang/zolem:latest    # latest release
```

Available platforms: `linux/amd64`, `linux/arm64`.

See [Docker examples](#docker-examples) below for `docker run` usage.

### From source

Requires Go 1.26+.

```bash
go install github.com/ketang/zolem/cmd/zolem@latest
go install github.com/ketang/zolem/cmd/zolemc@latest
```

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

## Quick Start: Fixed Listener Mode

Start one loopback listener pinned to a provider and backend:

```bash
zolem \
  -local-addr 127.0.0.1:18080 \
  -local-provider anthropic \
  -local-profile demo \
  -local-backend lorem
```

For fixture-backed fixed listeners, also pass `-local-fixtures-dir`.

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

## Agent tooling

Zolem agents can use [refute](https://github.com/shatterproof-ai/refute) for
symbol-aware Go refactors. Install or update the repo-local binary from the
expected local checkout at `~/project/refute`:

```bash
./scripts/setup-refute.sh
```

This writes `.agents/bin/refute` and runs `version` plus `doctor`. If the
checkout is missing, clone `https://github.com/shatterproof-ai/refute` to
`~/project/refute` or pass `--source /path/to/refute`.

Check readiness before refactoring:

```bash
.agents/bin/refute doctor
```

Agents should preview changes with `--dry-run --json`, apply only after the
preview is correct, and then run zolem's relevant verification gate.

Verify the zolem wrapper behavior with:

```bash
./scripts/test-refute-setup.sh
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

## Shatter

Zolem is configured for a full Shatter scan of the non-test Go source under
`cmd/` and `internal/`.

```bash
make shatter
```

By default this uses `~/project/shatter/target/release/shatter`. Set
`SHATTER_BIN=/path/to/shatter` to use a different binary. Reports are written
under `shatter-report/`, with generated cache and artifact state ignored by git.

Full scans require Docker. The scan wrapper runs Shatter targets with
`SHATTER_SANDBOX_BACKEND=docker` and defaults
`SHATTER_SANDBOX_DOCKER_IMAGE` to `golang:1.26-bookworm`, so the container has
the Go toolchain required by targets that compile code. Override
`SHATTER_SANDBOX_DOCKER_IMAGE` if the harness needs extra runtime packages.

The setup check verifies full source discovery without executing functions:

```bash
./scripts/test-shatter-setup.sh
```

The sandbox wrapper check verifies the full-scan invocation passes Docker
sandbox settings to Shatter and rejects host writes to the repo or `/tmp`:

```bash
./scripts/test-shatter-full-scan-sandbox.sh
```

## Docker examples

Basic local runtime mode (admin server):

```bash
docker run --rm -p 18090:18090 \
  ghcr.io/ketang/zolem:v0.1.0 \
  -local-admin-addr 0.0.0.0:18090
```

With a fixture directory mounted:

```bash
docker run --rm -p 18090:18090 \
  -v $PWD/fixtures:/fixtures \
  ghcr.io/ketang/zolem:v0.1.0 \
  -local-admin-addr 0.0.0.0:18090 \
  -local-fixtures-dir /fixtures
```

With TLS certs mounted:

```bash
docker run --rm -p 18443:18443 \
  -v $PWD/certs:/certs \
  ghcr.io/ketang/zolem:v0.1.0 \
  -local-admin-addr 0.0.0.0:18443 \
  -local-tls-cert /certs/localhost.pem \
  -local-tls-key /certs/localhost-key.pem
```

Fixed-listener mode in a container:

```bash
docker run --rm -p 18080:18080 \
  ghcr.io/ketang/zolem:v0.1.0 \
  -local-addr 0.0.0.0:18080 \
  -local-provider openai \
  -local-backend lorem
```

Run `zolemc` against a containerized admin server from the host:

```bash
zolemc -admin-url http://127.0.0.1:18090 profiles create demo -backend lorem
```

## Artifact verification

<details>
<summary>Checksum, cosign signature, and SBOM verification</summary>

### Checksums

```bash
sha256sum -c checksums.txt
```

### Cosign signature

Archives are signed via GitHub Actions OIDC. Verify with [cosign](https://github.com/sigstore/cosign):

```bash
cosign verify-blob \
  --bundle zolem-<version>-<os>-<arch>.tar.gz.bundle \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp "https://github.com/ketang/zolem/.github/workflows/release.yml@refs/tags/.*" \
  zolem-<version>-<os>-<arch>.tar.gz
```

Exit code `0` means the signature is valid.

### SBOMs

Each archive has a paired `.sbom` file in [CycloneDX](https://cyclonedx.org/) format. Inspect with [syft](https://github.com/anchore/syft):

```bash
syft zolem-<version>-<os>-<arch>.tar.gz.sbom
```

</details>
