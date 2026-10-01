# Changelog

## Unreleased

### Changed

- Ollama upstream IP policy is now one shared classifier for profile
  validation and dialing. `0.0.0.0/8`, `::`, multicast, zoned literals,
  Teredo, NAT64 local-use, and NAT64/6to4/IPv4-compatible forms embedding
  blocked addresses are rejected at profile creation even with
  `allow_external_ollama_upstream`. Loopback/private IPv4 embedded via
  NAT64/6to4/IPv4-compatible now requires `allow_external_ollama_upstream`
  (zolem-gbpq).
- Non-streamed fixtures are served verbatim in every provider; only the
  provider's model key is replaced, and only on 2xx bodies that already have
  it, so error envelopes and unmodeled fields survive. Ollama non-streamed
  fixtures no longer get `model` injected when the fixture lacks the key
  (zolem-8ra).

### Added

- Opt-in non-loopback bind for containers: `-allow-non-loopback-bind` lets
  both modes bind `0.0.0.0` or `::` (specific non-loopback IPs stay rejected).
  It requires at least one `-allowed-host` (additive: `localhost` and loopback
  IPs still pass) and, in control-plane mode, `-listener-port-range LOW-HIGH`
  (listeners outside the range get 400; invalid in fixed-listener mode).
  Listeners on a wildcard host report a `localhost` `base_url`. Loopback-only
  stays the default. INSTALL.md's Docker recipes are rewritten around these
  flags, and CI now runs the snapshot image (zolem-l0m).
- `ollama-logprob` backend for the `typesafe` provider: answers `choice`,
  `score`, and `noul` questions with a real local Ollama model, using the
  first token's log probabilities as the probability distribution. Configure
  it with `backend_model` (required), the existing `ollama_upstream`, and the
  new `calibration_temperature` profile field; fixed-listener mode gains
  `-local-backend-model`, `-local-ollama-upstream`, and
  `-local-calibration-temperature`, and `zolemc profiles create` gains
  `-calibration-temperature`. Requires Ollama 0.12.11 or newer and is rejected
  for every other provider. See `docs/typesafe.md` (zolem-w0i).

### Security

- Fixed-listener mode now enforces the Host-header (DNS-rebinding) guard that
  admin mode already applied: requests whose `Host` is not `localhost` or a
  loopback IP get `403`, including the Responses WebSocket upgrade. The new
  repeatable `-allowed-host` flag (both modes) additionally allows an alias such
  as an `/etc/hosts` name or a custom-hostname TLS cert. See
  `docs/fixed-listener.md` (zolem-bdz). Existing fixed-mode users whose clients
  send `Host: 0.0.0.0:<port>`, `localhost.` or an IPv6 zone form now get 403
  and must add the name with `-allowed-host`; rejected requests are not
  recorded in `-local-calls-file`. `-allowed-host` rejects empty and URL-like
  values with exit 2.

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
