# Zolem: LLM API Mock Service — Design Spec

**Date:** 2026-04-02  
**Status:** Historical design snapshot

This document records the original approved design direction from April 2026.
It is not the current user-facing runtime reference; use
[`README.md`](../README.md) and [`docs/local-runtime.md`](local-runtime.md) for
current local runtime commands, flags, and fixture behavior.

---

## Overview

Zolem is a Go service that faithfully mocks the APIs of major LLM providers (Anthropic Claude, OpenAI, Google Gemini). Clients point at Zolem by changing only a hostname; all paths, parameters, request/response shapes, error formats, and streaming protocols are replicated exactly. The service supports lorem, faker, and fixture-based response generation.

---

## Architecture

Single Go binary. One HTTP server dispatches to provider-specific handler packages based on the `Host` header (virtual host routing). A shared core handles fixture matching and response generation.

```
zolem binary
│
├── HTTP server (net/http + chi)
│   └── Virtual host dispatch on Host header
│
├── provider/anthropic   — routes, validation, serialization, SSE
├── provider/openai      — routes, validation, serialization, SSE
├── provider/gemini      — routes, validation, serialization, SSE
│
└── core/
    ├── fixture/         — WASM runner, match scoring, fixture loading
    ├── response/        — lorem ipsum generator, SSE chunking utilities
    ├── specs/           — runtime spec fetching, disk cache, versioned store
    └── router/          — virtual host routing table, label extraction
```

**Each provider package owns:**
- Route registration (exact paths matching the real provider API)
- Request unmarshaling into provider/version-specific structs
- Validation against the runtime-fetched OpenAPI spec for that `(provider, version)` pair
- Provider-exact error response serialization
- SSE event serialization (static, hand-written — streaming wire formats don't come from OpenAPI specs)

---

## Response Modes (MVP)

Two modes, selectable per-request via fixture matching or globally via config:

| Mode | Description |
|------|-------------|
| `lorem` | Return valid, well-formed lorem ipsum responses. |
| `faker` | Return deterministic fake business-style responses. |
| `fixture` | Return a canned response defined in a fixture file, selected by WASM match scoring; unmatched requests fall back to the built-in generator path. |

Future modes (not in MVP): local LLM passthrough.

---

## Virtual Host Routing

Clients change only the hostname. Paths remain identical to the real provider API.

The routing table is defined in config and evaluated in order; first match wins. Wildcards in host patterns are captured as named labels and threaded into request context. Labels are available to WASM match functions.

Example host patterns:

```
*.api.anthropic.zolem.dev       → provider: anthropic, labels: {tenant: {1}}
*.api.openai.zolem.dev          → provider: openai,    labels: {tenant: {1}}
*.generativelanguage.zolem.dev  → provider: gemini,    labels: {tenant: {1}}

*.*.api.anthropic.zolem.dev     → provider: anthropic, labels: {env: {1}, tenant: {2}}
```

Additional label levels can be added without code changes — the routing table is purely configuration.

---

## Request Validation

Incoming request bodies are validated at runtime against the provider's OpenAPI spec for the specific API version (detected from the URL path, e.g. `/v1/messages`, `/v1beta/models/...`).

**Spec fetching:**
- Specs are fetched from provider SDK repos and cached to disk on startup
- Refreshed on a configurable interval (default: 6h)
- A bundled set of known-good specs ships with the binary as an offline fallback
- Specs are stored per `(provider, version)` pair
- Spec updates and binary releases are fully decoupled — no redeploy needed when a provider adds a new parameter
- Runtime JSON Schema validator: `santhosh-tekuri/jsonschema` (supports draft 2020-12)

**Spec sources:**
- Anthropic: TypeScript SDK repo
- OpenAI: `openai/openai-openapi` GitHub repo
- Gemini: Google API discovery documents

Validation errors return provider-exact error shapes (see Error Handling).

---

## Fixture Engine

### Storage Layout

```
fixtures/
  my-fixture/
    match.wasm        # WASM match function — returns score (f32)
    response.json     # response body (or response.yaml)
    meta.yaml         # fixture metadata
```

**`meta.yaml`:**
```yaml
id: my-fixture
provider: anthropic
version: v1
stream: true
status: 200
```

### WASM Match Functions

Every fixture declares a WASM match function. The runtime is **wazero** (pure Go, no CGO).

**ABI:**
- Input: serialized request context as JSON bytes written into WASM linear memory
  ```json
  {
    "provider": "anthropic",
    "version": "v1",
    "labels": {"tenant": "acme", "env": "prod"},
    "body": { ...validated request body... }
  }
  ```
- Output: `f32` score — negative means no match; higher is stronger match

**Scoring:**
- All fixtures whose WASM function returns a non-negative score are candidates
- Highest score wins
- Ties: first fixture in filesystem order wins
- No match: falls back to the built-in synthetic generator path

**Fixture loading:**
- Loaded at startup from the configured fixture directory
- Watched for changes with `fsnotify` — new/modified fixtures are hot-reloaded without restart
- WASM modules are compiled once at load time (wazero ahead-of-time compilation)

**Future:** An online editor will support writing match rules in Google's Common Expression Language (CEL), compiled to WASM, removing the need for users to manage WASM toolchains directly.

---

## SSE Streaming

SSE serializers are static, hand-written per provider. The streaming wire formats are stable protocol behavior not captured in OpenAPI specs.

### Anthropic

Uses named SSE events. Fixed sequence per response:

```
event: message_start
data: {"type":"message_start","message":{"id":"...","type":"message","role":"assistant","content":[],"model":"...","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":N,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Lorem "}}

... (one delta per token)

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":N}}

event: message_stop
data: {"type":"message_stop"}
```

Stream ends on `message_stop`. Mid-stream errors use Anthropic's defined `error` event type.

### OpenAI

Data-only events (no `event:` lines). Terminated by the literal string `data: [DONE]`.

```
data: {"id":"chatcmpl-...","object":"chat.completion.chunk","created":N,"model":"...","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-...","object":"chat.completion.chunk","created":N,"model":"...","choices":[{"index":0,"delta":{"content":"Lorem "},"finish_reason":null}]}

... (one chunk per token)

data: {"id":"chatcmpl-...","object":"chat.completion.chunk","created":N,"model":"...","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

Token usage included in a final chunk when `stream_options: {include_usage: true}` is set.

### Gemini

Streaming triggered via a different endpoint path (`:streamGenerateContent`) and URL parameter `?alt=sse`. Each chunk is a complete `GenerateContentResponse`. No explicit terminal marker — stream ends when `finishReason` is not `"NONE"`.

```
data: {"candidates":[{"content":{"parts":[{"text":"Lorem "}],"role":"model"},"finishReason":"NONE","index":0}],"usageMetadata":{"promptTokenCount":N,"totalTokenCount":N}}

... (one chunk per token)

data: {"candidates":[{"content":{"parts":[{"text":"ipsum."}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":N,"candidatesTokenCount":N,"totalTokenCount":N}}
```

**Implementation note:** All providers use `http.Flusher.Flush()` after each chunk. Token pacing (configurable inter-chunk delay) can simulate realistic latency.

---

## Error Handling

### Provider-Fidelity Errors

Malformed requests return provider-exact error shapes:

```
Anthropic:  HTTP 400  {"type":"error","error":{"type":"invalid_request_error","message":"..."}}
OpenAI:     HTTP 400  {"error":{"message":"...","type":"invalid_request_error","code":null}}
Gemini:     HTTP 400  {"error":{"code":400,"message":"...","status":"INVALID_ARGUMENT","details":[...]}}
```

Auth errors use provider-specific status codes (Anthropic: 401, OpenAI: 401, Gemini: 403) with matching shapes.

**Auth behavior:** Zolem does not validate API key values — any non-empty key is accepted. It does validate that the key is present in the correct header for the provider (`x-api-key` for Anthropic, `Authorization: Bearer ...` for OpenAI, `x-goog-api-key` for Gemini). A missing or malformed auth header returns the provider-exact 401/403 response.

### Zolem-Internal Errors

Errors that originate in Zolem itself (no matching route, fixture directory unreadable, spec fetch failure) return:
- `X-Zolem-Error: true` response header
- Plain JSON body: `{"zolem_error": "...", "detail": "..."}`

This lets callers distinguish "my request was invalid" from "the mock server has a problem."

### Mid-Stream Errors

If a fixture response fails after streaming has started:
- Anthropic: emit a provider-defined `error` event, close stream
- OpenAI / Gemini: emit a best-effort error chunk, close connection

---

## Configuration

YAML file, path via `--config` flag or `ZOLEM_CONFIG` env var.

```yaml
server:
  addr: ":8080"
  tls:
    cert: /etc/zolem/tls.crt
    key:  /etc/zolem/tls.key

mode: lorem   # default mode: lorem | faker | fixture

specs:
  cache_dir: /var/zolem/specs
  refresh_interval: 6h

fixtures:
  dir: /var/zolem/fixtures
  watch: true

routes:
  - host: "*.api.anthropic.zolem.dev"
    provider: anthropic
    labels:
      tenant: "{1}"
  - host: "*.api.openai.zolem.dev"
    provider: openai
    labels:
      tenant: "{1}"
  - host: "*.generativelanguage.zolem.dev"
    provider: gemini
    labels:
      tenant: "{1}"
  - host: "*.*.api.anthropic.zolem.dev"
    provider: anthropic
    labels:
      env:    "{1}"
      tenant: "{2}"
```

---

## Testing Strategy

| Layer | Approach |
|-------|----------|
| Provider handler unit tests | Feed raw HTTP requests; assert exact response bytes. Validates request validation and response serialization independently. |
| Fixture engine unit tests | Construct scored match scenarios; assert winning fixture selection. |
| SSE integration tests | Use `httptest.Server`; stream a full response; assert every event in sequence. |
| Spec validation tests | Record real provider responses against live APIs; replay through Zolem; assert response shape equivalence. |
| WASM fixture tests | Ship a small stdlib of reference `.wasm` match functions in the repo for use in tests. |

---

## Out of Scope (MVP)

- Faker-based response mode
- Local LLM passthrough mode
- Multi-tenant management plane (SaaS features: auth, instance provisioning, fixture uploads, online CEL→WASM editor)
- Admin UI
