# Changelog

## Unreleased

### Documentation

- Marked the April 2026 design document as historical, fixed Anthropic snapshot
  links and test selectors, removed the empty `ERRORS.md` stub, refreshed local
  TLS certificate guidance, and replaced hard-coded Docker example tags with
  current tag guidance.

## v0.1.0 — 2026-05-25

Initial release.

### What's included

**Runtime modes**
- Local runtime mode: in-memory profiles and loopback listeners managed via admin API (`zolem -local-admin-addr`)
- Fixed-listener mode: single loopback listener pinned to a provider and profile at startup (`zolem -local-addr`)

**Response backends**
- `lorem` — lorem-ipsum placeholder text (default)
- `faker` — randomized fake data
- `fixture` — static or templated responses selected by CEL or WASM fixture matchers
- `ollama` — forwards generation to a local Ollama instance
- `wasm` — profile-supplied WebAssembly content generator
- `error` — always returns a provider-native error (local runtime only)

**Providers**
- Anthropic, OpenAI, Gemini (request validation against real OpenAPI/discovery specs)
- OpenRouter spec tracked for parsing; local runtime listeners serve Anthropic, OpenAI, and Gemini

**CLI**
- `zolemc` CLI for admin operations: profile and listener create/inspect, health checks, request replay
- Call recording for fixture and listener inspection

**Matching**
- CEL-based fixture matching
- WASM-based fixture matching

**TLS**
- Optional TLS for local admin server and data-plane listeners

**Distribution**
- Multi-arch binaries: Linux amd64/arm64, macOS arm64
- Multi-arch Docker image: `ghcr.io/ketang/zolem` (linux/amd64, linux/arm64)
- Archives are cosign-signed (GitHub Actions OIDC) with paired `.bundle` files
- CycloneDX SBOMs for each archive

### Known out of scope

- Package manager integrations (Homebrew, Scoop, etc.)
- Man page generation
- Persistent profile/listener storage (in-memory only; state disappears on restart)
- Auth or TTL support on listeners
- Remote (non-loopback) listeners
