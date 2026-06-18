# Ollama Backend Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote Ollama from a fallback text generator to a first-class `"ollama"` backend mode that uses Ollama's HTTP API for generation with no silent fallback.

**Architecture:** Add HTTP-based chat completion methods to `ollama.Client`, wire `"ollama"` as a valid backend in the runtime profile, and update each provider handler to dispatch to Ollama's HTTP API when the backend is `"ollama"` — returning errors instead of falling back to lorem.

**Tech Stack:** Go, net/http, httptest, SSE (text/event-stream)

---

### Task 1: Ollama HTTP Client — Types and Non-Streaming

**Files:**
- Create: `internal/ollama/http.go`
- Create: `internal/ollama/http_test.go`

- [ ] **Step 1: Write the failing test for non-streaming chat completion**

In `internal/ollama/http_test.go`:

```go
package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPChatCompletion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != false {
			t.Errorf("expected stream=false, got %v", req["stream"])
		}
		if req["model"] != "gemma3:4b" {
			t.Errorf("expected model=gemma3:4b, got %v", req["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "Hello from ollama"}},
			},
		})
	}))
	defer srv.Close()

	text, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello from ollama" {
		t.Fatalf("unexpected text: %q", text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ollama/ -run TestHTTPChatCompletion_Success -v`
Expected: compilation error — `HTTPChatCompletion` and `ChatMessage` undefined

- [ ] **Step 3: Implement types and non-streaming chat completion**

In `internal/ollama/http.go`:

```go
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ChatMessage is a minimal message for the OpenAI-compatible chat API.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message ChatMessage `json:"message"`
}

// HTTPChatCompletion sends a non-streaming chat completion request to an
// OpenAI-compatible endpoint and returns the response text.
func HTTPChatCompletion(ctx context.Context, upstream string, messages []ChatMessage, model string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	})
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama backend error (HTTP %d): %s", resp.StatusCode, respBody)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("ollama backend returned unparseable response: %w", err)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("ollama backend returned empty response")
	}

	return chatResp.Choices[0].Message.Content, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ollama/ -run TestHTTPChatCompletion_Success -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ollama/http.go internal/ollama/http_test.go
git commit -m "feat(ollama): add HTTP chat completion for non-streaming"
```

---

### Task 2: Ollama HTTP Client — Error Cases

**Files:**
- Modify: `internal/ollama/http_test.go`

- [ ] **Step 1: Write failing tests for error cases**

Append to `internal/ollama/http_test.go`:

```go
func TestHTTPChatCompletion_ConnectionRefused(t *testing.T) {
	_, err := HTTPChatCompletion(context.Background(), "http://127.0.0.1:1", []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")

	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ollama backend unavailable") {
		t.Fatalf("expected 'ollama backend unavailable', got: %v", err)
	}
}

func TestHTTPChatCompletion_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()

	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")

	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if !strings.Contains(err.Error(), "ollama backend error") {
		t.Fatalf("expected 'ollama backend error', got: %v", err)
	}
}

func TestHTTPChatCompletion_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")

	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Fatalf("expected 'unparseable', got: %v", err)
	}
}

func TestHTTPChatCompletion_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := HTTPChatCompletion(ctx, srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
```

Add `"strings"` to the import block in `http_test.go`.

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/ollama/ -run TestHTTPChatCompletion -v`
Expected: all 4 tests PASS (the implementation from Task 1 already handles these cases)

- [ ] **Step 3: Commit**

```bash
git add internal/ollama/http_test.go
git commit -m "test(ollama): add HTTP chat completion error case coverage"
```

---

### Task 3: Ollama HTTP Client — Streaming

**Files:**
- Modify: `internal/ollama/http.go`
- Modify: `internal/ollama/http_test.go`

- [ ] **Step 1: Write failing test for streaming**

Append to `internal/ollama/http_test.go`:

```go
func TestHTTPChatCompletionStream_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != true {
			t.Errorf("expected stream=true, got %v", req["stream"])
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")

		chunks := []string{"Hello ", "from ", "ollama"}
		for _, chunk := range chunks {
			data, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{"delta": map[string]string{"content": chunk}},
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	var deltas []string
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deltas) != 3 {
		t.Fatalf("expected 3 deltas, got %d: %v", len(deltas), deltas)
	}
	joined := strings.Join(deltas, "")
	if joined != "Hello from ollama" {
		t.Fatalf("unexpected text: %q", joined)
	}
}
```

Add `"fmt"` to the import block in `http_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ollama/ -run TestHTTPChatCompletionStream_Success -v`
Expected: compilation error — `HTTPChatCompletionStream` undefined

- [ ] **Step 3: Implement streaming chat completion**

Add to `internal/ollama/http.go`:

```go
import (
	"bufio"
	// ... existing imports
	"strings"
)

type streamDelta struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// HTTPChatCompletionStream sends a streaming chat completion request and
// invokes fn for each content delta. Returns when the stream ends or on error.
func HTTPChatCompletionStream(ctx context.Context, upstream string, messages []ChatMessage, model string, fn func(delta string) error) error {
	body, err := json.Marshal(chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	})
	if err != nil {
		return fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama backend unavailable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama backend error (HTTP %d): %s", resp.StatusCode, respBody)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var delta streamDelta
		if err := json.Unmarshal([]byte(data), &delta); err != nil {
			continue
		}
		if len(delta.Choices) == 0 || delta.Choices[0].Delta.Content == "" {
			continue
		}
		if err := fn(delta.Choices[0].Delta.Content); err != nil {
			return err
		}
	}
	return scanner.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ollama/ -run TestHTTPChatCompletionStream -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ollama/http.go internal/ollama/http_test.go
git commit -m "feat(ollama): add HTTP streaming chat completion"
```

---

### Task 4: Ollama HTTP Client — Streaming Error Cases

**Files:**
- Modify: `internal/ollama/http_test.go`

- [ ] **Step 1: Write tests for streaming error cases**

Append to `internal/ollama/http_test.go`:

```go
func TestHTTPChatCompletionStream_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()

	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		t.Fatal("callback should not be called")
		return nil
	})

	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if !strings.Contains(err.Error(), "ollama backend error") {
		t.Fatalf("expected 'ollama backend error', got: %v", err)
	}
}

func TestHTTPChatCompletionStream_ConnectionRefused(t *testing.T) {
	err := HTTPChatCompletionStream(context.Background(), "http://127.0.0.1:1", []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		return nil
	})

	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ollama backend unavailable") {
		t.Fatalf("expected 'ollama backend unavailable', got: %v", err)
	}
}

func TestHTTPChatCompletionStream_CallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")

		data, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]string{"content": "hello"}},
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}))
	defer srv.Close()

	callbackErr := errors.New("writer closed")
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		return callbackErr
	})

	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got: %v", err)
	}
}
```

Add `"errors"` to the import block if not already present.

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/ollama/ -run TestHTTPChatCompletionStream -v`
Expected: all 4 streaming tests PASS

- [ ] **Step 3: Commit**

```bash
git add internal/ollama/http_test.go
git commit -m "test(ollama): add streaming error case coverage"
```

---

### Task 5: Runtime — Accept "ollama" Backend

**Files:**
- Modify: `internal/runtime/backend.go`
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/store.go`
- Modify: `internal/runtime/store_test.go` (or create if needed)

- [ ] **Step 1: Write failing test for ollama backend validation**

Check if `internal/runtime/store_test.go` exists. If not, create it. Add:

```go
package runtimecfg

import "testing"

func TestValidateProfile_OllamaBackend(t *testing.T) {
	err := ValidateProfile(RuntimeProfile{
		Name:    "test",
		Backend: "ollama",
	})
	if err != nil {
		t.Fatalf("ollama backend should be valid: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime/ -run TestValidateProfile_OllamaBackend -v`
Expected: FAIL — "profile backend must be lorem, faker, or fixture"

- [ ] **Step 3: Add "ollama" backend constant and update validation**

In `internal/runtime/backend.go`, add the constant:

```go
const (
	BackendHybrid  = "hybrid"
	BackendLorem   = "lorem"
	BackendFaker   = "faker"
	BackendFixture = "fixture"
	BackendOllama  = "ollama"
)
```

In `internal/runtime/store.go`, update `ValidateProfile` (line 162-168):

Change:
```go
	switch profile.Backend {
	case "", "lorem", "faker", "fixture":
		return nil
	default:
		return errors.New("profile backend must be lorem, faker, or fixture")
	}
```

To:
```go
	switch profile.Backend {
	case "", "lorem", "faker", "fixture", "ollama":
		return nil
	default:
		return errors.New("profile backend must be lorem, faker, fixture, or ollama")
	}
```

In `internal/runtime/runtime.go`, add the `OllamaUpstream` field:

```go
type RuntimeProfile struct {
	Name                string `json:"name"`
	Backend             string `json:"backend"`
	BackendModel        string `json:"backend_model"`
	ResponseModelPolicy string `json:"response_model_policy"`
	ResponseModel       string `json:"response_model"`
	FixtureNamespace    string `json:"fixture_namespace"`
	Seed                *int64 `json:"seed,omitempty"`
	OllamaUpstream      string `json:"ollama_upstream,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime/ -run TestValidateProfile_OllamaBackend -v`
Expected: PASS

- [ ] **Step 5: Run all runtime tests**

Run: `go test ./internal/runtime/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/backend.go internal/runtime/runtime.go internal/runtime/store.go internal/runtime/store_test.go
git commit -m "feat(runtime): accept ollama as a valid backend"
```

---

### Task 6: Provider Handlers — Ollama Backend Interface

Each provider handler needs a new interface for HTTP-based ollama generation (distinct from the existing `textGenerator` which returns a string from a flattened prompt). We'll add a `chatGenerator` interface that takes structured messages. All three handlers use the same interface.

**Files:**
- Modify: `internal/provider/anthropic/handler.go`
- Modify: `internal/provider/openai/handler.go`
- Modify: `internal/provider/gemini/handler.go`

- [ ] **Step 1: Define the interface and update Handler struct in anthropic**

In `internal/provider/anthropic/handler.go`, add after the `textGenerator` interface:

```go
type chatGenerator interface {
	ChatCompletion(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error)
	ChatCompletionStream(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error
}
```

Wait — the handlers shouldn't import the ollama package directly. The `ChatMessage` type is simple enough to define locally or use the ollama package. Since the ollama package is already a dependency in the import chain (via startup), and to avoid duplication, import `ollama.ChatMessage` directly.

Actually, looking at the existing pattern: the handlers use a `textGenerator` interface and don't import the ollama package at all. The interface is satisfied by `*ollama.Client`. We should follow the same pattern for the HTTP methods.

Better approach: define a single interface in each handler that covers both non-streaming and streaming:

```go
type ollamaHTTPClient interface {
	ChatCompletion(ctx context.Context, upstream string, messages []ChatMessage, model string) (string, error)
	ChatCompletionStream(ctx context.Context, upstream string, messages []ChatMessage, model string, fn func(delta string) error) error
}
```

But this requires `ChatMessage` to be defined somewhere shared. It's already in `internal/ollama/http.go`. The handlers can import it.

Actually, the cleanest approach: make `HTTPChatCompletion` and `HTTPChatCompletionStream` package-level functions (which they already are in Task 1/3). The handlers don't need an interface for these — they can call the functions directly. The interface is only needed for testing. But that makes testing harder.

Let me reconsider. The existing pattern uses an interface (`textGenerator`) injected into the handler. For the ollama HTTP path, the handler needs to know:
1. Whether the backend is ollama (from runtime context)
2. The upstream URL (from runtime context, via `RuntimeProfile.OllamaUpstream`)
3. The model (from runtime context, via `BackendModel` or fallback)

Since all the config comes from the runtime context, and the HTTP functions are stateless, the handlers can just call the package-level functions from `internal/ollama`. For testing, we wrap them in an interface.

Simplest path: add a `chatGenerator` field to each handler that defaults to calling the ollama package functions. This is the same pattern as `textGenerator` but for structured messages.

In `internal/provider/anthropic/handler.go`:

Add import `"github.com/ketang/zolem/internal/ollama"`.

Add the interface:

```go
type chatGenerator interface {
	NonStreaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error)
	Streaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error
}
```

Update `Handler`:

```go
type Handler struct {
	validator     *specs.Validator
	matcher       *fixture.Matcher
	generator     response.Generator
	ollamaClient  textGenerator
	ollamaHTTP    chatGenerator
	mux           *chi.Mux
}
```

Update `NewHandler`:

```go
func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient textGenerator, ollamaHTTP chatGenerator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaClient: ollamaClient, ollamaHTTP: ollamaHTTP}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/messages", h.handleMessages)
	return h
}
```

- [ ] **Step 2: Apply the same changes to openai and gemini handlers**

Same interface and struct changes in `internal/provider/openai/handler.go` and `internal/provider/gemini/handler.go`. Update `NewHandler` signatures identically.

- [ ] **Step 3: Update all callers of NewHandler**

In `cmd/zolem/startup.go`, update `buildHandler` (line 412-416):

```go
func buildHandler(routes []config.RouteConfig, validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient textGenerator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, generator, ollamaClient, nil)
	openaiH := openai.NewHandler(validator, matcher, generator, ollamaClient, nil)
	geminiH := gemini.NewHandler(validator, matcher, generator, ollamaClient, nil)
```

Update `buildLocalHandler` (line 441-444):

```go
func buildLocalHandler(listenerRuntime runtimecfg.ListenerRuntime, validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, generator, nil, &ollamaHTTPAdapter{})
	openaiH := openai.NewHandler(validator, matcher, generator, nil, &ollamaHTTPAdapter{})
	geminiH := gemini.NewHandler(validator, matcher, generator, nil, &ollamaHTTPAdapter{})
```

Add the adapter in `cmd/zolem/startup.go`:

```go
type ollamaHTTPAdapter struct{}

func (a *ollamaHTTPAdapter) NonStreaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error) {
	return ollama.HTTPChatCompletion(ctx, upstream, messages, model)
}

func (a *ollamaHTTPAdapter) Streaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error {
	return ollama.HTTPChatCompletionStream(ctx, upstream, messages, model, fn)
}
```

Update test helpers in each handler test file to pass `nil` for the new `ollamaHTTP` parameter:

In `internal/provider/anthropic/handler_test.go`, update `newHandlerWithGenerator`:
```go
func newHandlerWithGenerator(t *testing.T, generator testGenerator) *anthropic.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	return anthropic.NewHandler(validator, matcher, lorem, generator, nil)
}
```

Apply the same `nil` addition to test helpers in `openai/handler_test.go` and `gemini/handler_test.go`.

- [ ] **Step 4: Run all tests**

Run: `go test ./... 2>&1`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/anthropic/handler.go internal/provider/openai/handler.go internal/provider/gemini/handler.go cmd/zolem/startup.go internal/provider/anthropic/handler_test.go internal/provider/openai/handler_test.go internal/provider/gemini/handler_test.go
git commit -m "feat: add chatGenerator interface to provider handlers"
```

---

### Task 7: Anthropic Handler — Ollama Backend Dispatch

**Files:**
- Modify: `internal/provider/anthropic/handler.go`
- Modify: `internal/provider/anthropic/handler_test.go`

- [ ] **Step 1: Write failing test for ollama backend non-streaming**

Append to `internal/provider/anthropic/handler_test.go`:

```go
type stubChatGenerator struct {
	text string
	err  error
}

func (g *stubChatGenerator) NonStreaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string) (string, error) {
	return g.text, g.err
}

func (g *stubChatGenerator) Streaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string, fn func(string) error) error {
	if g.err != nil {
		return g.err
	}
	for _, word := range strings.Fields(g.text) {
		if err := fn(word + " "); err != nil {
			return err
		}
	}
	return nil
}

func TestMessages_OllamaBackend_NonStreaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Ollama says hello"}
	h := anthropic.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			Name:           "test",
			Backend:        "ollama",
			OllamaUpstream: "http://localhost:11434",
		},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp anthropic.MessagesResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Content) == 0 || resp.Content[0].Text != "Ollama says hello" {
		t.Fatalf("unexpected response content: %+v", resp.Content)
	}
}
```

Add imports for `runtimecfg "github.com/ketang/zolem/internal/runtime"` and `"github.com/ketang/zolem/internal/ollama"` to the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/anthropic/ -run TestMessages_OllamaBackend_NonStreaming -v`
Expected: FAIL — the handler doesn't check for ollama backend yet, so it falls through to lorem

- [ ] **Step 3: Implement ollama backend dispatch in handleMessages**

In `internal/provider/anthropic/handler.go`, after the fixture check block (after line 90) and before the ollama text generator fallback (line 95), add the ollama backend dispatch:

Replace the section from line 92 to 133 (everything after the fixture block to the end of the function) with:

```go
	inputTokens := estimateInputTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)

	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendOllama {
		h.handleOllamaBackend(w, r, req, responseModel, inputTokens)
		return
	}

	if text, ok := h.generateText(r.Context(), promptFromRequest(req)); ok {
		if req.Stream {
			streamResponse(w, responseModel, tokenize(text), inputTokens)
			return
		}

		resp := MessagesResponse{
			ID:         "msg_zolem_ollama",
			Type:       "message",
			Role:       "assistant",
			Content:    []ContentBlock{{Type: "text", Text: text}},
			Model:      responseModel,
			StopReason: "end_turn",
			Usage:      Usage{InputTokens: inputTokens, OutputTokens: len(strings.Fields(text))},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	tokens := h.generator.Generate(30)
	if req.Stream {
		streamResponse(w, responseModel, tokens, inputTokens)
		return
	}

	text := strings.Join(tokens, "")
	resp := MessagesResponse{
		ID:         "msg_zolem_generated",
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: text}},
		Model:      responseModel,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: inputTokens, OutputTokens: len(tokens)},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
```

Add the `handleOllamaBackend` method and the message conversion helper:

```go
func (h *Handler) handleOllamaBackend(w http.ResponseWriter, r *http.Request, req MessagesRequest, responseModel string, inputTokens int) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(r.Context())
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	model := rt.Profile.BackendModel
	if model == "" {
		model = req.Model
	}

	messages := anthropicToChatMessages(req)

	if req.Stream {
		h.handleOllamaStream(w, r.Context(), upstream, messages, model, responseModel, inputTokens)
		return
	}

	text, err := h.ollamaHTTP.NonStreaming(r.Context(), upstream, messages, model)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_error", "ollama backend error: "+err.Error())
		return
	}

	resp := MessagesResponse{
		ID:         "msg_zolem_ollama",
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: text}},
		Model:      responseModel,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: inputTokens, OutputTokens: len(strings.Fields(text))},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleOllamaStream(w http.ResponseWriter, ctx context.Context, upstream string, messages []ollama.ChatMessage, model, responseModel string, inputTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	msgID := "msg_zolem_" + fmt.Sprintf("%016x", pseudoRandID())

	msgStart, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []any{}, "model": responseModel,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": inputTokens, "output_tokens": 1},
		},
	})
	sse.WriteEvent("message_start", msgStart)
	sse.Flush()

	cbStart, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})
	sse.WriteEvent("content_block_start", cbStart)
	sse.Flush()

	sse.WriteEvent("ping", []byte(`{"type":"ping"}`))
	sse.Flush()

	outputTokens := 0
	err := h.ollamaHTTP.Streaming(ctx, upstream, messages, model, func(delta string) error {
		outputTokens++
		d, _ := json.Marshal(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]string{"type": "text_delta", "text": delta},
		})
		sse.WriteEvent("content_block_delta", d)
		sse.Flush()
		return nil
	})

	if err != nil {
		// Stream already started — can't change status code. Write an error event.
		errData, _ := json.Marshal(map[string]any{
			"type":    "error",
			"error":   map[string]string{"type": "api_error", "message": "ollama backend error: " + err.Error()},
		})
		sse.WriteEvent("error", errData)
		sse.Flush()
		return
	}

	sse.WriteEvent("content_block_stop", []byte(`{"type":"content_block_stop","index":0}`))
	sse.Flush()

	msgDelta, _ := json.Marshal(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": outputTokens},
	})
	sse.WriteEvent("message_delta", msgDelta)
	sse.Flush()

	sse.WriteEvent("message_stop", []byte(`{"type":"message_stop"}`))
	sse.Flush()
}

func anthropicToChatMessages(req MessagesRequest) []ollama.ChatMessage {
	var messages []ollama.ChatMessage
	if req.System != "" {
		messages = append(messages, ollama.ChatMessage{Role: "system", Content: strings.TrimSpace(req.System)})
	}
	for _, msg := range req.Messages {
		text := msg.Content.PlainText()
		if text == "" {
			continue
		}
		messages = append(messages, ollama.ChatMessage{Role: msg.Role, Content: text})
	}
	return messages
}
```

Add imports: `"github.com/ketang/zolem/internal/ollama"`, `"fmt"`, `"github.com/ketang/zolem/internal/response"`. Note that `fmt` and `response` may already be imported — check and add only what's missing.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/anthropic/ -run TestMessages_OllamaBackend -v`
Expected: PASS

- [ ] **Step 5: Run all anthropic tests**

Run: `go test ./internal/provider/anthropic/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/provider/anthropic/handler.go internal/provider/anthropic/handler_test.go
git commit -m "feat(anthropic): dispatch to ollama HTTP backend"
```

---

### Task 8: Anthropic Handler — Ollama Backend Error and Streaming Tests

**Files:**
- Modify: `internal/provider/anthropic/handler_test.go`

- [ ] **Step 1: Write tests for error and streaming cases**

Append to `internal/provider/anthropic/handler_test.go`:

```go
func TestMessages_OllamaBackend_Error(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{err: errors.New("connection refused")}
	h := anthropic.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["type"] != "error" {
		t.Fatalf("expected error response, got: %+v", resp)
	}
}

func TestMessages_OllamaBackend_Streaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Hello world"}
	h := anthropic.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "content_block_delta") {
		t.Fatalf("expected SSE content_block_delta events, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "Hello") {
		t.Fatalf("expected 'Hello' in streaming response, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "message_stop") {
		t.Fatalf("expected message_stop event, got: %s", responseBody)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/provider/anthropic/ -run TestMessages_OllamaBackend -v`
Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add internal/provider/anthropic/handler_test.go
git commit -m "test(anthropic): add ollama backend error and streaming tests"
```

---

### Task 9: OpenAI Handler — Ollama Backend Dispatch

**Files:**
- Modify: `internal/provider/openai/handler.go`
- Modify: `internal/provider/openai/handler_test.go`

- [ ] **Step 1: Write failing test for ollama backend non-streaming**

Append to `internal/provider/openai/handler_test.go`:

```go
type stubChatGenerator struct {
	text string
	err  error
}

func (g *stubChatGenerator) NonStreaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string) (string, error) {
	return g.text, g.err
}

func (g *stubChatGenerator) Streaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string, fn func(string) error) error {
	if g.err != nil {
		return g.err
	}
	for _, word := range strings.Fields(g.text) {
		if err := fn(word + " "); err != nil {
			return err
		}
	}
	return nil
}

func TestChatCompletions_OllamaBackend_NonStreaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Ollama says hello"}
	h := openai.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp openai.ChatCompletionResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Ollama says hello" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
```

Add imports for `runtimecfg "github.com/ketang/zolem/internal/runtime"` and `"github.com/ketang/zolem/internal/ollama"`.

- [ ] **Step 2: Implement ollama backend dispatch in handleChatCompletions**

In `internal/provider/openai/handler.go`, add the same pattern as anthropic. After the fixture block and before the ollama text generator fallback, add:

```go
	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendOllama {
		h.handleOllamaBackend(w, r, req, responseModel, promptTokens)
		return
	}
```

Add the `handleOllamaBackend`, `handleOllamaStream`, and `openaiToChatMessages` methods:

```go
func (h *Handler) handleOllamaBackend(w http.ResponseWriter, r *http.Request, req ChatCompletionRequest, responseModel string, promptTokens int) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(r.Context())
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	model := rt.Profile.BackendModel
	if model == "" {
		model = req.Model
	}

	messages := openaiToChatMessages(req)

	if req.Stream {
		h.handleOllamaStream(w, r.Context(), upstream, messages, model, responseModel, promptTokens)
		return
	}

	text, err := h.ollamaHTTP.NonStreaming(r.Context(), upstream, messages, model)
	if err != nil {
		writeError(w, http.StatusBadGateway, "server_error", "ollama backend error: "+err.Error())
		return
	}

	completionTokens := len(strings.Fields(text))
	resp := ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   responseModel,
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}},
		Usage:   Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, TotalTokens: promptTokens + completionTokens},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleOllamaStream(w http.ResponseWriter, ctx context.Context, upstream string, messages []ollama.ChatMessage, model, responseModel string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	id := fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano())
	created := time.Now().Unix()

	firstChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant", "content": ""}, "finish_reason": nil}},
	}
	data, _ := json.Marshal(firstChunk)
	sse.WriteData(data)
	sse.Flush()

	completionTokens := 0
	err := h.ollamaHTTP.Streaming(ctx, upstream, messages, model, func(delta string) error {
		completionTokens++
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": delta}, "finish_reason": nil}},
		}
		d, _ := json.Marshal(chunk)
		sse.WriteData(d)
		sse.Flush()
		return nil
	})

	if err != nil {
		errChunk := map[string]any{"error": map[string]string{"message": "ollama backend error: " + err.Error(), "type": "server_error"}}
		d, _ := json.Marshal(errChunk)
		sse.WriteData(d)
		sse.Flush()
		return
	}

	stop := "stop"
	finalChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": stop}},
	}
	data, _ = json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()

	usageChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
		"choices": []any{},
		"usage": map[string]int{
			"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
			"total_tokens": promptTokens + completionTokens,
		},
	}
	data, _ = json.Marshal(usageChunk)
	sse.WriteData(data)
	sse.Flush()

	sse.WriteData([]byte("[DONE]"))
	sse.Flush()
}

func openaiToChatMessages(req ChatCompletionRequest) []ollama.ChatMessage {
	messages := make([]ollama.ChatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		if msg.Content == "" {
			continue
		}
		messages = append(messages, ollama.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	return messages
}
```

Add imports: `"github.com/ketang/zolem/internal/ollama"`, `"github.com/ketang/zolem/internal/response"`. Keep existing imports.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/provider/openai/ -v`
Expected: all PASS

- [ ] **Step 4: Add error and streaming tests**

Append to `internal/provider/openai/handler_test.go`:

```go
func TestChatCompletions_OllamaBackend_Error(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{err: errors.New("connection refused")}
	h := openai.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
}

func TestChatCompletions_OllamaBackend_Streaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Hello world"}
	h := openai.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "Hello") {
		t.Fatalf("expected 'Hello' in streaming response, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "[DONE]") {
		t.Fatalf("expected [DONE] in streaming response, got: %s", responseBody)
	}
}
```

- [ ] **Step 5: Run all openai tests**

Run: `go test ./internal/provider/openai/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/provider/openai/handler.go internal/provider/openai/handler_test.go
git commit -m "feat(openai): dispatch to ollama HTTP backend"
```

---

### Task 10: Gemini Handler — Ollama Backend Dispatch

**Files:**
- Modify: `internal/provider/gemini/handler.go`
- Modify: `internal/provider/gemini/handler_test.go`

- [ ] **Step 1: Write failing test for ollama backend non-streaming**

Append to `internal/provider/gemini/handler_test.go`:

```go
type stubChatGenerator struct {
	text string
	err  error
}

func (g *stubChatGenerator) NonStreaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string) (string, error) {
	return g.text, g.err
}

func (g *stubChatGenerator) Streaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string, fn func(string) error) error {
	if g.err != nil {
		return g.err
	}
	for _, word := range strings.Fields(g.text) {
		if err := fn(word + " "); err != nil {
			return err
		}
	}
	return nil
}

func TestGenerateContent_OllamaBackend_NonStreaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Ollama says hello"}
	h := gemini.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp gemini.GenerateContentResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		t.Fatalf("unexpected empty response: %+v", resp)
	}
	if resp.Candidates[0].Content.Parts[0].Text != "Ollama says hello" {
		t.Fatalf("unexpected text: %q", resp.Candidates[0].Content.Parts[0].Text)
	}
}
```

Add imports for `runtimecfg "github.com/ketang/zolem/internal/runtime"` and `"github.com/ketang/zolem/internal/ollama"`.

- [ ] **Step 2: Implement ollama backend dispatch in handleGenerate**

In `internal/provider/gemini/handler.go`, after the fixture block and before the ollama text generator fallback, add:

```go
	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendOllama {
		h.handleOllamaBackend(w, r, req, version, model, stream)
		return
	}
```

Add the `handleOllamaBackend`, `handleOllamaStream`, and `geminiToChatMessages` methods:

```go
func (h *Handler) handleOllamaBackend(w http.ResponseWriter, r *http.Request, req GenerateContentRequest, version, model string, stream bool) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(r.Context())
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	ollamaModel := rt.Profile.BackendModel
	if ollamaModel == "" {
		ollamaModel = model
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), model)
	messages := geminiToChatMessages(req)

	if stream {
		h.handleOllamaStream(w, r.Context(), upstream, messages, ollamaModel, responseModel, promptTokens)
		return
	}

	text, err := h.ollamaHTTP.NonStreaming(r.Context(), upstream, messages, ollamaModel)
	if err != nil {
		writeError(w, http.StatusBadGateway, "INTERNAL", "ollama backend error: "+err.Error())
		return
	}

	completionTokens := len(strings.Fields(text))
	resp := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: text}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: completionTokens,
			TotalTokenCount:      promptTokens + completionTokens,
		},
		ModelVersion: responseModel,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleOllamaStream(w http.ResponseWriter, ctx context.Context, upstream string, messages []ollama.ChatMessage, model, responseModel string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	completionTokens := 0
	err := h.ollamaHTTP.Streaming(ctx, upstream, messages, model, func(delta string) error {
		completionTokens++
		chunk := GenerateContentResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: delta}}, Role: "model"},
				FinishReason: "NONE",
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount: promptTokens,
			},
			ModelVersion: responseModel,
		}
		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
		return nil
	})

	if err != nil {
		writeError(w, http.StatusBadGateway, "INTERNAL", "ollama backend error: "+err.Error())
		return
	}

	// Final chunk with STOP
	finalChunk := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: ""}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: completionTokens,
			TotalTokenCount:      promptTokens + completionTokens,
		},
		ModelVersion: responseModel,
	}
	data, _ := json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()
}

func geminiToChatMessages(req GenerateContentRequest) []ollama.ChatMessage {
	var messages []ollama.ChatMessage
	for _, content := range req.Contents {
		role := content.Role
		if role == "" {
			role = "user"
		}
		if role == "model" {
			role = "assistant"
		}
		var parts []string
		for _, part := range content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) == 0 {
			continue
		}
		messages = append(messages, ollama.ChatMessage{Role: role, Content: strings.Join(parts, " ")})
	}
	return messages
}
```

Add imports: `"github.com/ketang/zolem/internal/ollama"`, `"github.com/ketang/zolem/internal/response"`.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/provider/gemini/ -v`
Expected: all PASS

- [ ] **Step 4: Add error and streaming tests**

Append to `internal/provider/gemini/handler_test.go`:

```go
func TestGenerateContent_OllamaBackend_Error(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{err: errors.New("connection refused")}
	h := gemini.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
}

func TestStreamGenerateContent_OllamaBackend(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Hello world"}
	h := gemini.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:streamGenerateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "Hello") {
		t.Fatalf("expected 'Hello' in streaming response, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "STOP") {
		t.Fatalf("expected STOP in final chunk, got: %s", responseBody)
	}
}
```

- [ ] **Step 5: Run all gemini tests**

Run: `go test ./internal/provider/gemini/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/provider/gemini/handler.go internal/provider/gemini/handler_test.go
git commit -m "feat(gemini): dispatch to ollama HTTP backend"
```

---

### Task 11: Startup Wiring — Ollama Backend in Local Mode

**Files:**
- Modify: `cmd/zolem/startup.go`

- [ ] **Step 1: Update generatorForBackend to accept ollama**

In `cmd/zolem/startup.go`, update `generatorForBackend` (line 479-490):

```go
func generatorForBackend(backend string, deps startupDeps) (response.Generator, error) {
	switch backend {
	case "", "lorem":
		return deps.newLorem(), nil
	case "faker":
		return deps.newFaker(), nil
	case runtimecfg.BackendFixture:
		return deps.newLorem(), nil
	case runtimecfg.BackendOllama:
		return deps.newLorem(), nil // generator is unused for ollama backend; handler dispatches to HTTP client
	default:
		return nil, fmt.Errorf("unsupported local backend %q", backend)
	}
}
```

- [ ] **Step 2: Update localOptions.runtime() to accept ollama backend**

In `cmd/zolem/startup.go`, the `runtime()` method (line 518-552) currently hardcodes valid backends. It sets `backend` from `o.Backend`, which flows into `RuntimeProfile.Backend`. The profile validation (in `store.go`) already accepts `"ollama"` from Task 5. No changes needed here since the profile is validated by `ValidateProfile` which was updated.

However, check that the `runtime()` function doesn't do its own backend validation. Looking at the code — it doesn't; it just passes the backend through. Good.

- [ ] **Step 3: Run all tests**

Run: `go test ./... 2>&1`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/zolem/startup.go
git commit -m "feat(startup): wire ollama backend in local mode"
```

---

### Task 12: Full Integration — Run All Tests

**Files:** None (verification only)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./... 2>&1`
Expected: all packages PASS

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: no issues

- [ ] **Step 3: Verify the build compiles**

Run: `go build ./cmd/zolem/`
Expected: clean build, no errors
