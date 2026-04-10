# Ollama Backend Mode

**Issue:** #2 — Add local-LLM passthrough mode
**Date:** 2026-04-09

## Summary

Promote Ollama from a fallback text generator (issue #5) to a first-class backend mode. When a runtime profile selects `backend: "ollama"`, all generation goes through Ollama's HTTP API with no silent fallback to lorem. Streaming is supported. Errors propagate to the caller.

## Architecture

### Backend dispatch

A new `"ollama"` value is added to the backend enum. The handler flow becomes:

```
backend == "ollama"  → ollama HTTP API → provider envelope → respond (or error)
backend == "hybrid"  → fixture? → ollama generate? → lorem (existing, unchanged)
backend == "lorem"   → lorem
backend == "faker"   → faker
backend == "fixture" → fixture
```

When `backend == "ollama"`:
- No fixture matching
- No lorem/faker fallback
- If Ollama fails, the request fails

### Scope

This feature is local runtime mode only. Static config mode is unaffected.

## Ollama HTTP Client

The existing `internal/ollama` package gains HTTP-based generation alongside the current shell-based `Generate()`.

### New methods on `ollama.Client`

```go
func (c *Client) ChatCompletion(ctx context.Context, upstream string, messages []ChatMessage, model string) (string, error)
func (c *Client) ChatCompletionStream(ctx context.Context, upstream string, messages []ChatMessage, model string, fn func(delta string) error) error
```

### ChatMessage

```go
type ChatMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}
```

Each provider handler maps its native request format into `[]ChatMessage` before calling the client. This preserves message structure (roles, multi-turn) instead of flattening to a single prompt string.

### HTTP details

- The client appends `/v1/chat/completions` to the upstream base URL
- Sets `stream: true` or `false` based on the caller
- For streaming, parses the SSE response (`data: {...}` lines) and invokes the callback with each content delta
- No retry logic

### Relationship to existing Generate()

The existing `Generate()` method (shells out to `ollama run`) remains unchanged. It continues to serve the `"hybrid"` backend path where Ollama is a fallback text generator. The new HTTP methods are used exclusively by the `"ollama"` backend mode.

## Config

### RuntimeProfile changes

```go
type RuntimeProfile struct {
    // ... existing fields ...
    OllamaUpstream string `json:"ollama_upstream,omitempty"`
}
```

`OllamaUpstream` is the base URL of the Ollama server (e.g., `http://localhost:11434`). It defaults to `http://localhost:11434` when `backend == "ollama"` and not explicitly set.

### Validation rules

- `backend == "ollama"` is accepted as a valid backend value
- `OllamaUpstream`, if set, must be a valid HTTP/HTTPS URL
- No cross-field requirement: omitting `OllamaUpstream` with `backend == "ollama"` uses the default

### Model selection

The model sent to Ollama is determined by the existing `BackendModel` field on the profile. If `BackendModel` is empty, the model from the global ollama config is used. If neither is set, the model detected at startup (via the eligibility checks from issue #5) is used.

## Request Translation

Each provider handler has a function to map its native request into `[]ChatMessage`:

### Anthropic to ChatMessage

- `req.System` → `ChatMessage{Role: "system", Content: system}`
- `req.Messages` → mapped with roles preserved (`user`/`assistant`), text content extracted

### OpenAI to ChatMessage

- Near pass-through. Messages already have `role` and `content`.

### Gemini to ChatMessage

- `req.SystemInstruction` → `ChatMessage{Role: "system", Content: text}`
- `req.Contents` → mapped (`user` stays `user`, `model` becomes `assistant`)

Only text content is translated. Tool calls, function definitions, and multimodal content are silently dropped.

## Response Translation

### Non-streaming

The handler calls `ChatCompletion()`, gets back the response text, and wraps it in the provider's response envelope. This is the same code path used for ollama/lorem responses today, with real model output instead of generated text.

### Streaming

The handler calls `ChatCompletionStream()` with a callback. Each callback invocation receives a text delta. The callback writes the delta in the provider's native SSE format using the existing `sse.go` writers:

- **Anthropic:** Named events (`content_block_delta`, etc.)
- **OpenAI:** `data: {...}` chunks
- **Gemini:** Content response chunks

The SSE envelope logic (start/stop events, framing) stays in each provider's `sse.go`, unchanged.

## Error Handling

When `backend == "ollama"` and the Ollama HTTP call fails:

| Failure | Response |
|---------|----------|
| Connection refused / unreachable | 502, `"ollama backend unavailable"` |
| Ollama returns an error | 502, include Ollama's error message |
| Malformed response | 502, `"ollama backend returned unparseable response"` |
| Context cancelled | Let propagate (client disconnected) |

All errors use the provider's own error envelope format so the caller's SDK error parsing works:
- Anthropic: `{"type":"error","error":{"type":"api_error","message":"..."}}`
- OpenAI: `{"error":{"message":"...","type":"server_error"}}`
- Gemini: `{"error":{"code":502,"message":"...","status":"INTERNAL"}}`

## Testing

### Ollama HTTP client tests

Use `httptest.Server` as the upstream. Cover:
- Successful non-streaming completion
- Streaming with multiple deltas
- Ollama error responses
- Connection refused (upstream down)
- Context cancellation
- Malformed SSE from upstream

### Handler tests per provider

Mock the ollama client (existing `textGenerator` interface pattern). Verify:
- `[]ChatMessage` construction from each provider's request format
- Response envelope correctness
- Streaming SSE output format
- Error responses when ollama backend fails

### Validation tests

- `backend == "ollama"` passes validation
- `OllamaUpstream` with a valid URL passes
- `OllamaUpstream` with an invalid URL fails

### Integration smoke test

Optional. If Ollama is running, hit the real API. Skip in CI.

## Non-goals

- Tool calling / function calling
- Multimodal content (images, audio)
- General-purpose proxy to arbitrary LLM providers
- Static config mode support
- Retry logic
