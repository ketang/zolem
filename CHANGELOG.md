# Changelog

## Unreleased

### Changed

- The `typesafe` provider now returns HTTP 422 (was 400) for request-validation
  failures, matching TypeSafe's documented status, with a FastAPI-style
  `{"detail": [{"loc": ["body"], "msg": "..."}]}` body. The body shape is
  inferred from the official SDK's error parser. The `error` backend's forced
  `invalid_request` stays 400. See `docs/typesafe.md` (zolem-61d3).

## v0.2.0 — 2026-09-23

### Added

- TypeSafe's Jev "System One" API (`POST /v1/systemone`) is now a supported
  provider surface (`provider: typesafe`). It serves the `noul`, `choice`,
  and `score` judgment primitives against a vendored v1 request schema, with
  fixture, error, and synthetic (lorem/faker) backends and full
  type-consistency validation of every answer against the request's
  questions (a `choice` answer must name a real option, probabilities must
  sum to 1, `noul` must be in `[0, 1]`, etc.). No authentication is required
  in the mock. See `docs/typesafe.md` (zolem-jwr).
- Ollama is now a supported provider surface, not only a backend. A listener
  with `provider: ollama` serves Ollama's native API — `POST /api/chat` plus
  `/api/tags`, `/api/version`, `/api/show`, and `/api/ps` — so Ollama clients
  can be developed and tested with no model pulled and no GPU. Streaming is
  NDJSON rather than SSE, `stream` defaults to true when omitted, requests need
  no authentication, and errors use Ollama's flat `{"error": "..."}` envelope.
  Model-management endpoints are not served.

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
