package main_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalRuntimeErrorBackend_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	t.Run("openai-rate-limit", func(t *testing.T) {
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":    "error",
			"error_type": "rate_limit",
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "Authorization: Bearer sk-test")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var payload struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Error.Type != "rate_limit_error" {
			t.Fatalf("error type: got %q, want rate_limit_error", payload.Error.Type)
		}
		if payload.Error.Code != "rate_limit_exceeded" {
			t.Fatalf("error code: got %q, want rate_limit_exceeded", payload.Error.Code)
		}
	})

	t.Run("anthropic-rate-limit", func(t *testing.T) {
		listenerBaseURL := createRuntimeListener(t, admin, "anthropic", map[string]any{
			"backend":    "error",
			"error_type": "rate_limit",
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/messages", `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "x-api-key: sk-test")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var payload struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Error     struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Type != "error" {
			t.Fatalf("type: got %q, want error", payload.Type)
		}
		if payload.Error.Type != "rate_limit_error" {
			t.Fatalf("error type: got %q, want rate_limit_error", payload.Error.Type)
		}
		if payload.RequestID == "" {
			t.Fatal("expected request_id")
		}
	})

	t.Run("gemini-rate-limit", func(t *testing.T) {
		listenerBaseURL := createRuntimeListener(t, admin, "gemini", map[string]any{
			"backend":    "error",
			"error_type": "rate_limit",
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, "Content-Type: application/json", "x-goog-api-key: test-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var payload struct {
			Error struct {
				Status  string `json:"status"`
				Details []struct {
					Reason string `json:"reason"`
				} `json:"details"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Error.Status != "RESOURCE_EXHAUSTED" {
			t.Fatalf("status text: got %q, want RESOURCE_EXHAUSTED", payload.Error.Status)
		}
		if len(payload.Error.Details) == 0 || payload.Error.Details[0].Reason != "RATE_LIMIT_EXCEEDED" {
			t.Fatalf("details: got %#v, want RATE_LIMIT_EXCEEDED", payload.Error.Details)
		}
	})
}

func TestLocalRuntimeAnthropicSystemContentBlocks_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	listenerBaseURL := createRuntimeListener(t, admin, "anthropic", map[string]any{
		"backend": "lorem",
	})

	resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/messages",
		`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"system":[{"type":"text","text":"be precise","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`,
		"Content-Type: application/json", "x-api-key: sk-test")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
	}
	var payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	mustJSONUnmarshal(t, body, &payload)
	if payload.Type != "message" || payload.Role != "assistant" {
		t.Fatalf("payload identity: got type=%q role=%q", payload.Type, payload.Role)
	}
	if len(payload.Content) == 0 || payload.Content[0].Type != "text" || payload.Content[0].Text == "" {
		t.Fatalf("content: got %#v, want non-empty text block", payload.Content)
	}
}

func TestLocalRuntimeWASMBackend_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
		"backend":                  "wasm",
		"wasm_module_base64":       "AGFzbQEAAAABFQRgAX8Bf2ACf38AYAJ/fwF/YAF/AAMHBgABAgAAAwUDAQABB08HBm1lbW9yeQIABWFsbG9jAAAHZGVhbGxvYwABCGdlbmVyYXRlAAIKcmVzdWx0X3B0cgADCnJlc3VsdF9sZW4ABAtyZXN1bHRfZnJlZQAFCh0GBQBBgAgLAgALBABBAQsFAEGAEAsEAEEXCwIACwseAQBBgBALF1siSGVsbG8gIiwiZnJvbSBXQVNNLiJd",
		"wasm_generate_timeout_ms": 100,
		"stream_delay": map[string]any{
			"mode": "fixed",
			"ms":   0,
		},
	})

	resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "Authorization: Bearer sk-test")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	mustJSONUnmarshal(t, body, &payload)
	if len(payload.Choices) == 0 || payload.Choices[0].Message.Content != "Hello from WASM." {
		t.Fatalf("unexpected WASM response: %s", body)
	}

	streamResp, streamBody := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "Authorization: Bearer sk-test")
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status: got %d, want 200: %s", streamResp.StatusCode, streamBody)
	}
	assertSSEHeaders(t, streamResp.Header)
	if !strings.Contains(string(streamBody), `"content":"Hello "`) || !strings.Contains(string(streamBody), `"content":"from WASM."`) {
		t.Fatalf("stream body did not contain generated chunks:\n%s", streamBody)
	}
	// The bundled WASM module's data segment encodes ["Hello ", "from WASM."]
	// — exactly 2 generator tokens — so the chunker must emit exactly that
	// many token deltas.
	assertOpenAIStreamShape(t, streamBody, 2)
}

type localAdminService struct {
	baseURL string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	errCh   chan error
	logs    *bytes.Buffer
}

func startLocalAdminService(t *testing.T, repoRoot string) *localAdminService {
	t.Helper()

	workDir := t.TempDir()
	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-local-admin-addr", adminAddr)
	cmd.Dir = filepath.Join(repoRoot, "cmd", "zolem")
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(workDir, "gocache"))
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start local admin: %v", err)
	}

	svc := &localAdminService{
		baseURL: "http://" + adminAddr,
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}

	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-svc.done:
			err := <-svc.errCh
			t.Fatalf("local admin exited before readiness: %v\nlogs:\n%s", err, logs.String())
		default:
		}

		resp, _, err := doRequestRaw(client, svc.baseURL, http.MethodGet, "/_zolem/health", "")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return svc
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for local admin readiness\nlogs:\n%s", logs.String())
	return nil
}

func (s *localAdminService) Close() {
	s.cancel()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

func createRuntimeListener(t *testing.T, admin *localAdminService, provider string, profile map[string]any) string {
	t.Helper()

	backend, _ := profile["backend"].(string)
	if backend == "" {
		backend = "lorem"
	}
	profileName := provider + "-" + backend + "-demo"
	listenerName := provider + "-" + backend + "-listener"
	profileBody, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}

	profileResp, body := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName, string(profileBody), "Content-Type: application/json")
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile status: got %d, want 200: %s", profileResp.StatusCode, body)
	}
	t.Cleanup(func() {
		resp, _, err := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/profiles/"+profileName, "")
		if err == nil && resp != nil {
			resp.Body.Close()
		}
	})

	listenerPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"%s","profile":"%s"}`, provider, profileName)
	listenerResp, listenerBody := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener status: got %d, want 200: %s", listenerResp.StatusCode, listenerBody)
	}
	t.Cleanup(func() {
		resp, _, err := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName, "")
		if err == nil && resp != nil {
			resp.Body.Close()
		}
	})

	var payload struct {
		BaseURL string `json:"base_url"`
	}
	mustJSONUnmarshal(t, listenerBody, &payload)
	if payload.BaseURL == "" {
		t.Fatalf("listener base_url missing: %s", strings.TrimSpace(string(listenerBody)))
	}

	stateResp, stateBody := doRequest(t, payload.BaseURL, http.MethodGet, "/_zolem/state", "")
	defer stateResp.Body.Close()
	if stateResp.StatusCode != http.StatusOK {
		t.Fatalf("state status: got %d, want 200", stateResp.StatusCode)
	}
	var state map[string]any
	mustJSONUnmarshal(t, stateBody, &state)
	if state["provider"] != provider {
		t.Fatalf("state provider: got %#v, want %s", state["provider"], provider)
	}
	if state["backend"] != backend {
		t.Fatalf("state backend: got %#v, want %s", state["backend"], backend)
	}

	return payload.BaseURL
}

func TestLocalRuntimeLocalBackends_E2E(t *testing.T) {
	repoRoot := repoRoot(t)

	t.Run("lorem", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})

		t.Run("non-streaming", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			text := openAICompletionContent(t, body)
			// The lorem generator emits a fixed dictionary; the first word is always "lorem".
			if !strings.Contains(strings.ToLower(text), "lorem") {
				t.Fatalf("lorem backend response did not contain a lorem token: %q", text)
			}
		})

		t.Run("streaming", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
			}
			assertSSEHeaders(t, resp.Header)
			// Lorem backend uses Generate(30); chunker frames as 30 token deltas plus 4 framing events.
			assertOpenAIStreamShape(t, body, 30)
		})
	})

	t.Run("faker", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "faker",
		})

		t.Run("non-streaming", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			// No seed flag exists for the faker backend; assert schema + non-empty content only.
			if text := openAICompletionContent(t, body); strings.TrimSpace(text) == "" {
				t.Fatalf("faker backend returned empty content: %s", body)
			}
		})

		t.Run("streaming", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
			}
			assertSSEHeaders(t, resp.Header)
			// Faker backend shares the openai handler's Generate(30) path with lorem.
			assertOpenAIStreamShape(t, body, 30)
		})
	})

	t.Run("fixture", func(t *testing.T) {
		fixturesDir := t.TempDir()
		copyTestdataFixtures(t, repoRoot, fixturesDir)
		writeOpenAICELFixture(t, fixturesDir)
		admin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(admin.Close)

		profile := map[string]any{
			"backend": "fixture",
		}
		listenerBaseURL := createRuntimeListener(t, admin, "openai", profile)

		// The fixture is templated and interpolates the runtime profile name.
		// createRuntimeListener constructs the profile name as "<provider>-<backend>-demo".
		profileName := "openai-fixture-demo"
		want := "Templated fixture for profile " + profileName + "."

		t.Run("non-streaming", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			if got := openAICompletionContent(t, body); got != want {
				t.Fatalf("rendered fixture content: got %q, want %q", got, want)
			}
		})

		t.Run("sse", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
			}
			assertSSEHeaders(t, resp.Header)
			got := joinOpenAIStreamContent(t, body)
			if got != want {
				t.Fatalf("rendered streamed content: got %q, want %q", got, want)
			}
			// The fixture handler tokenizes the rendered content with
			// strings.Fields; the rendered string is 5 words
			// ("Templated fixture for profile openai-fixture-demo."), so the
			// chunker must emit exactly 5 token deltas.
			assertOpenAIStreamShape(t, body, 5)
		})

		t.Run("cel", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"cel please"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			if got := openAICompletionContent(t, body); got != "CEL fixture matched." {
				t.Fatalf("CEL fixture content: got %q, want %q", got, "CEL fixture matched.")
			}
		})
	})

	t.Run("fixtures-yaml", func(t *testing.T) {
		fixturesDir := t.TempDir()
		writeFixturesYAMLNamespace(t, fixturesDir)
		admin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "fixture",
		})

		t.Run("routes-mini-model", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			if got := openAICompletionContent(t, body); got != "yaml-mini matched." {
				t.Fatalf("yaml-mini content: got %q, want yaml-mini matched.", got)
			}
		})

		t.Run("routes-full-model", func(t *testing.T) {
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			if got := openAICompletionContent(t, body); got != "yaml-full matched." {
				t.Fatalf("yaml-full content: got %q, want yaml-full matched.", got)
			}
		})
	})

	t.Run("ollama", func(t *testing.T) {
		var (
			lastRequest atomicChatRequest
		)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
				http.Error(w, "unexpected request", http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			var req struct {
				Model    string `json:"model"`
				Stream   bool   `json:"stream"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			lastRequest.Store(req.Model, req.Stream, len(req.Messages))

			if !req.Stream {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{
						{"message": map[string]any{"role": "assistant", "content": "ollama-upstream-reply"}},
					},
				})
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, token := range []string{"hello ", "from ", "ollama"} {
				chunk := map[string]any{
					"choices": []map[string]any{
						{"delta": map[string]string{"content": token}},
					},
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				if flusher != nil {
					flusher.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}))
		t.Cleanup(upstream.Close)

		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":         "ollama",
			"backend_model":   "test-model",
			"ollama_upstream": upstream.URL,
		})

		t.Run("non-streaming", func(t *testing.T) {
			lastRequest.Reset()
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			assertOpenAIChatCompletion(t, resp, body)
			if got := openAICompletionContent(t, body); got != "ollama-upstream-reply" {
				t.Fatalf("ollama content: got %q, want %q", got, "ollama-upstream-reply")
			}
			model, stream, msgCount := lastRequest.Load()
			if model != "test-model" {
				t.Fatalf("upstream model: got %q, want test-model", model)
			}
			if stream {
				t.Fatalf("upstream stream: got true, want false")
			}
			if msgCount != 1 {
				t.Fatalf("upstream message count: got %d, want 1", msgCount)
			}
		})

		t.Run("streaming", func(t *testing.T) {
			lastRequest.Reset()
			resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
			}
			assertSSEHeaders(t, resp.Header)
			got := joinOpenAIStreamContent(t, body)
			if got != "hello from ollama" {
				t.Fatalf("streamed content: got %q, want %q", got, "hello from ollama")
			}
			_, stream, _ := lastRequest.Load()
			if !stream {
				t.Fatalf("upstream stream: got false, want true")
			}
			// Upstream stub emits exactly 3 content deltas; the ollama
			// translator is required to forward 1:1 (no batching, no
			// dropping) to the client.
			assertOpenAIStreamShape(t, body, 3)
		})
	})
}

// atomicChatRequest captures the most recent chat-completion request observed
// by the in-process ollama upstream stub. It is intentionally goroutine-safe
// because httptest handlers may run on a different goroutine than the test.
type atomicChatRequest struct {
	mu       sync.Mutex
	model    string
	stream   bool
	messages int
	set      bool
}

func (a *atomicChatRequest) Store(model string, stream bool, messages int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	a.stream = stream
	a.messages = messages
	a.set = true
}

func (a *atomicChatRequest) Load() (string, bool, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.set {
		return "", false, 0
	}
	return a.model, a.stream, a.messages
}

func (a *atomicChatRequest) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = ""
	a.stream = false
	a.messages = 0
	a.set = false
}

func assertOpenAIChatCompletion(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type: got %q, want application/json", got)
	}
	var payload struct {
		Object  string `json:"object"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	mustJSONUnmarshal(t, body, &payload)
	if payload.Object != "chat.completion" {
		t.Fatalf("object: got %q, want chat.completion", payload.Object)
	}
	if len(payload.Choices) == 0 {
		t.Fatalf("choices missing: %s", body)
	}
	choice := payload.Choices[0]
	if choice.Message.Role != "assistant" {
		t.Fatalf("choices[0].message.role: got %q, want assistant", choice.Message.Role)
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		t.Fatalf("choices[0].message.content empty: %s", body)
	}
	if choice.FinishReason != "stop" {
		t.Fatalf("choices[0].finish_reason: got %q, want stop", choice.FinishReason)
	}
	if payload.Usage.TotalTokens == 0 || payload.Usage.TotalTokens != payload.Usage.PromptTokens+payload.Usage.CompletionTokens {
		t.Fatalf("usage tokens inconsistent: %+v", payload.Usage)
	}
}

func openAICompletionContent(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	mustJSONUnmarshal(t, body, &payload)
	if len(payload.Choices) == 0 {
		t.Fatalf("choices missing: %s", body)
	}
	return payload.Choices[0].Message.Content
}

func joinOpenAIStreamContent(t *testing.T, body []byte) string {
	t.Helper()
	var sb strings.Builder
	for _, record := range sseRecords(t, body) {
		payload := sseDataPayload(t, record)
		if string(payload) == "[DONE]" {
			continue
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			sb.WriteString(choice.Delta.Content)
		}
	}
	return sb.String()
}

func copyTestdataFixtures(t *testing.T, repoRoot, dst string) {
	t.Helper()
	src := filepath.Join(repoRoot, "cmd", "zolem", "testdata", "e2e_fixtures")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read testdata fixtures dir: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fixtureSrc := filepath.Join(src, entry.Name())
		fixtureDst := filepath.Join(dst, entry.Name())
		mustMkdir(t, fixtureDst)

		fixtureFiles, err := os.ReadDir(fixtureSrc)
		if err != nil {
			t.Fatalf("read fixture %q: %v", entry.Name(), err)
		}
		for _, f := range fixtureFiles {
			if f.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(fixtureSrc, f.Name()))
			if err != nil {
				t.Fatalf("read fixture file %q: %v", f.Name(), err)
			}
			if err := os.WriteFile(filepath.Join(fixtureDst, f.Name()), data, 0o644); err != nil {
				t.Fatalf("write fixture file %q: %v", f.Name(), err)
			}
		}
		// These legacy fixtures use match.wasm; the always-match module from
		// integration_helpers_test.go matches every request.
		if err := os.WriteFile(filepath.Join(fixtureDst, "match.wasm"), alwaysMatchWASM, 0o644); err != nil {
			t.Fatalf("write match.wasm: %v", err)
		}
	}
}

func writeOpenAICELFixture(t *testing.T, root string) {
	t.Helper()

	dir := filepath.Join(root, "openai-cel")
	mustMkdir(t, dir)
	meta := `id: openai-cel
provider: openai
version: v1
status: 200
match:
  cel: 'body["model"] == "gpt-4o-mini" && body["messages"][0]["content"] == "cel please"'
  score: 20
`
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write CEL meta.yaml: %v", err)
	}
	response := `{
  "id": "chatcmpl-cel",
  "object": "chat.completion",
  "created": 1,
  "model": "fixture-model",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "CEL fixture matched."},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 1, "completion_tokens": 3, "total_tokens": 4}
}`
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(response), 0o644); err != nil {
		t.Fatalf("write CEL response.json: %v", err)
	}
}

func writeFixturesYAMLNamespace(t *testing.T, root string) {
	t.Helper()
	writeYAMLNamespaceFixture(t, root, "yaml-mini", "yaml-mini matched.")
	writeYAMLNamespaceFixture(t, root, "yaml-full", "yaml-full matched.")
	yaml := `provider: openai
version: v1
fixtures:
  - expression: 'body["model"] == "gpt-4o-mini"'
    fixture: yaml-mini
  - expression: 'body["model"] == "gpt-4o"'
    fixture: yaml-full
`
	if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write fixtures.yaml: %v", err)
	}
}

func writeYAMLNamespaceFixture(t *testing.T, root, id, content string) {
	t.Helper()
	dir := filepath.Join(root, id)
	mustMkdir(t, dir)
	meta := "id: " + id + "\nprovider: openai\nversion: v1\nstatus: 200\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta.yaml for %q: %v", id, err)
	}
	response := fmt.Sprintf(`{
  "id": "chatcmpl-%s",
  "object": "chat.completion",
  "created": 1,
  "model": "fixture-model",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": %q},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 1, "completion_tokens": 3, "total_tokens": 4}
}`, id, content)
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(response), 0o644); err != nil {
		t.Fatalf("write response.json for %q: %v", id, err)
	}
}

// startLocalAdminServiceWithFixtures starts the cross-process local admin server
// with -local-fixtures-dir set so fixture-backend profiles can be exercised.
// It deliberately mirrors startLocalAdminService rather than extending it, to
// keep the existing helper signature stable for parallel test work on this file.
func startLocalAdminServiceWithFixtures(t *testing.T, repoRoot, fixturesDir string) *localAdminService {
	t.Helper()

	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-local-admin-addr", adminAddr, "-local-fixtures-dir", fixturesDir)
	cmd.Dir = filepath.Join(repoRoot, "cmd", "zolem")
	cmd.Env = os.Environ()
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start local admin: %v", err)
	}

	svc := &localAdminService{
		baseURL: "http://" + adminAddr,
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}

	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-svc.done:
			err := <-svc.errCh
			t.Fatalf("local admin exited before readiness: %v\nlogs:\n%s", err, logs.String())
		default:
		}
		resp, _, err := doRequestRaw(client, svc.baseURL, http.MethodGet, "/_zolem/health", "")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return svc
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for local admin readiness\nlogs:\n%s", logs.String())
	return nil
}

// TestLocalRuntimeWASMBoundaryExports_E2E covers the wasmgen
// boundary-export allowance (zolem-qnt) through the cross-process admin
// API. The accept-path is otherwise only exercised by package-internal
// tests in internal/wasmgen. The reject-path here additionally confirms
// the validator's error message reaches the admin client.
//
// Determinism: the WASM bytes are constructed in pure Go from a known
// hand-encoded base module — no rustc invocation, no checked-in
// .wasm fixture.
func TestLocalRuntimeWASMBoundaryExports_E2E(t *testing.T) {
	repoRoot := repoRoot(t)

	t.Run("accepts_boundary_globals", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		moduleBytes := wasmWithBoundaryGlobals(t)
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":                  "wasm",
			"wasm_module_base64":       base64.StdEncoding.EncodeToString(moduleBytes),
			"wasm_generate_timeout_ms": 100,
			"stream_delay": map[string]any{
				"mode": "fixed",
				"ms":   0,
			},
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		defer resp.Body.Close()

		assertOpenAIChatCompletion(t, resp, body)
		if got := openAICompletionContent(t, body); got != "Hello from WASM." {
			t.Fatalf("content: got %q, want %q", got, "Hello from WASM.")
		}
	})

	t.Run("rejects_unsupported_extra_export", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		moduleBytes := wasmWithExtraFuncExport(t, "extra")
		profileBody, err := json.Marshal(map[string]any{
			"backend":                  "wasm",
			"wasm_module_base64":       base64.StdEncoding.EncodeToString(moduleBytes),
			"wasm_generate_timeout_ms": 100,
		})
		if err != nil {
			t.Fatalf("marshal profile: %v", err)
		}

		resp, body := doRequest(t, admin.baseURL, http.MethodPut,
			"/_zolem/profiles/openai-wasm-extra-export",
			string(profileBody),
			"Content-Type: application/json")
		defer resp.Body.Close()

		// writeAdminError uses StatusBadRequest for non-NotFound/Conflict
		// errors, and the wasmgen path returns a plain validation error.
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400 (body: %s)", resp.StatusCode, body)
		}

		var payload struct {
			Error string `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		// The validator must name the offending export. Pin to "extra"
		// (hard requirement per the acceptance criteria).
		if !strings.Contains(payload.Error, `"extra"`) {
			t.Fatalf("error message must name the offending export %q, got %q",
				"extra", payload.Error)
		}
	})
}

// localGeneratorWASMBaseE2E mirrors localGeneratorWASM in wasm_backend_test.go.
// It is replicated here because that file lives in package main while this
// file lives in package main_test, and both modules need to assemble validator
// fixtures from the same hand-encoded base bytes.
var localGeneratorWASMBaseE2E = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x15, 0x04, 0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x00, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f, 0x60, 0x01, 0x7f, 0x00,
	0x03, 0x07, 0x06, 0x00, 0x01, 0x02, 0x00, 0x00, 0x03, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07,
	0x4f, 0x07, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x61, 0x6c, 0x6c,
	0x6f, 0x63, 0x00, 0x00, 0x07, 0x64, 0x65, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00, 0x01, 0x08,
	0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x00, 0x02, 0x0a, 0x72, 0x65, 0x73, 0x75,
	0x6c, 0x74, 0x5f, 0x70, 0x74, 0x72, 0x00, 0x03, 0x0a, 0x72, 0x65, 0x73, 0x75, 0x6c, 0x74,
	0x5f, 0x6c, 0x65, 0x6e, 0x00, 0x04, 0x0b, 0x72, 0x65, 0x73, 0x75, 0x6c, 0x74, 0x5f, 0x66,
	0x72, 0x65, 0x65, 0x00, 0x05, 0x0a, 0x1d, 0x06, 0x05, 0x00, 0x41, 0x80, 0x08, 0x0b, 0x02,
	0x00, 0x0b, 0x04, 0x00, 0x41, 0x01, 0x0b, 0x05, 0x00, 0x41, 0x80, 0x10, 0x0b, 0x04, 0x00,
	0x41, 0x17, 0x0b, 0x02, 0x00, 0x0b, 0x0b, 0x1e, 0x01, 0x00, 0x41, 0x80, 0x10, 0x0b, 0x17,
	0x5b, 0x22, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x20, 0x22, 0x2c, 0x22, 0x66, 0x72, 0x6f, 0x6d,
	0x20, 0x57, 0x41, 0x53, 0x4d, 0x2e, 0x22, 0x5d,
}

// wasmModuleSection holds a parsed top-level WASM section.
type wasmModuleSection struct {
	id      byte
	payload []byte
}

// parseWASMModule splits a binary WASM module into its 8-byte magic+version
// header and an ordered list of top-level sections. It is intentionally
// minimal: it does not understand section bodies, only the section framing.
func parseWASMModule(t *testing.T, b []byte) ([]byte, []wasmModuleSection) {
	t.Helper()
	if len(b) < 8 {
		t.Fatalf("WASM module too short: %d bytes", len(b))
	}
	header := append([]byte(nil), b[:8]...)
	off := 8
	var sections []wasmModuleSection
	for off < len(b) {
		id := b[off]
		off++
		size, n, err := decodeULEB128(b[off:])
		if err != nil {
			t.Fatalf("decode section size at offset %d: %v", off, err)
		}
		off += n
		end := off + int(size)
		if end < off || end > len(b) {
			t.Fatalf("section %d extends past module end", id)
		}
		sections = append(sections, wasmModuleSection{
			id:      id,
			payload: append([]byte(nil), b[off:end]...),
		})
		off = end
	}
	return header, sections
}

// emitWASMModule re-emits a header plus an ordered list of sections into a
// single WASM byte slice, encoding each section's size as ULEB128.
func emitWASMModule(header []byte, sections []wasmModuleSection) []byte {
	out := make([]byte, 0, len(header))
	out = append(out, header...)
	for _, s := range sections {
		out = append(out, s.id)
		out = append(out, encodeULEB128(uint32(len(s.payload)))...)
		out = append(out, s.payload...)
	}
	return out
}

// wasmWithBoundaryGlobals returns the base generator module patched to declare
// and export two i32-const boundary globals named "__data_end" and
// "__heap_base" — exactly the pair the validator's allowedBoundaryExports
// permits. The module remains runnable because the new globals do not affect
// the existing function/code/data sections.
func wasmWithBoundaryGlobals(t *testing.T) []byte {
	t.Helper()
	header, sections := parseWASMModule(t, localGeneratorWASMBaseE2E)

	// Build a Global section (id=6) declaring two i32 const globals
	// initialized to 0: count=2, then for each global the value type
	// (0x7f=i32), mutability (0x00=const), and init expression
	// `i32.const 0` `end` (0x41 0x00 0x0b).
	globalsPayload := []byte{
		0x02,
		0x7f, 0x00, 0x41, 0x00, 0x0b,
		0x7f, 0x00, 0x41, 0x00, 0x0b,
	}

	// Locate the export section and append "__data_end" -> global 0,
	// "__heap_base" -> global 1.
	exportIdx := indexOfSection(t, sections, 0x07)
	exportPayload := sections[exportIdx].payload

	count, n, err := decodeULEB128(exportPayload)
	if err != nil {
		t.Fatalf("decode export count: %v", err)
	}
	rest := append([]byte(nil), exportPayload[n:]...)
	rest = appendExportEntry(rest, "__data_end", 0x03 /* global */, 0)
	rest = appendExportEntry(rest, "__heap_base", 0x03 /* global */, 1)
	newPayload := append(encodeULEB128(count+2), rest...)

	// Replace the export section, then insert the global section just
	// before it (sections must appear in canonical id order: memory(5),
	// global(6), export(7)).
	sections[exportIdx].payload = newPayload
	sections = append(sections[:exportIdx], append(
		[]wasmModuleSection{{id: 0x06, payload: globalsPayload}},
		sections[exportIdx:]...,
	)...)

	return emitWASMModule(header, sections)
}

// wasmWithExtraFuncExport returns the base generator module patched to add an
// extra function export whose name is not in the wasmgen required-export set.
// The new entry points to function index 0 (alloc) so no new function or code
// body is needed; the validator rejects on the unrecognized name before any
// signature check.
func wasmWithExtraFuncExport(t *testing.T, name string) []byte {
	t.Helper()
	header, sections := parseWASMModule(t, localGeneratorWASMBaseE2E)

	exportIdx := indexOfSection(t, sections, 0x07)
	exportPayload := sections[exportIdx].payload

	count, n, err := decodeULEB128(exportPayload)
	if err != nil {
		t.Fatalf("decode export count: %v", err)
	}
	rest := append([]byte(nil), exportPayload[n:]...)
	rest = appendExportEntry(rest, name, 0x00 /* function */, 0)
	sections[exportIdx].payload = append(encodeULEB128(count+1), rest...)

	return emitWASMModule(header, sections)
}

func indexOfSection(t *testing.T, sections []wasmModuleSection, id byte) int {
	t.Helper()
	for i, s := range sections {
		if s.id == id {
			return i
		}
	}
	t.Fatalf("section id %d not found in module", id)
	return -1
}

func appendExportEntry(buf []byte, name string, kind byte, index uint32) []byte {
	buf = append(buf, encodeULEB128(uint32(len(name)))...)
	buf = append(buf, []byte(name)...)
	buf = append(buf, kind)
	buf = append(buf, encodeULEB128(index)...)
	return buf
}

func encodeULEB128(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		out = append(out, b)
		return out
	}
}

func decodeULEB128(b []byte) (uint32, int, error) {
	var result uint32
	for i := 0; i < 5; i++ {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("ULEB128: unexpected end of input")
		}
		c := b[i]
		result |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return result, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("ULEB128: value exceeds uint32")
}

func TestLocalRuntimeCallHistory_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	t.Run("basic_recording", func(t *testing.T) {
		listenerName := "openai-lorem-listener"
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})
		clearCalls(t, admin.baseURL, listenerName)

		resp, _ := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(calls))
		}
		c := calls[0]
		if c.CallID != 1 {
			t.Fatalf("call_id: got %d, want 1", c.CallID)
		}
		if c.Request.Method != "POST" {
			t.Fatalf("method: got %q, want POST", c.Request.Method)
		}
		if c.Request.Path != "/v1/chat/completions" {
			t.Fatalf("path: got %q, want /v1/chat/completions", c.Request.Path)
		}
		if c.Response.Status != 200 {
			t.Fatalf("status: got %d, want 200", c.Response.Status)
		}
		if c.Request.Body == "" {
			t.Fatal("request.body is empty")
		}
		if c.Response.Body == "" {
			t.Fatal("response.body is empty")
		}
	})

	t.Run("body_cap", func(t *testing.T) {
		profileName := "openai-capped-profile"
		listenerName := "openai-capped-listener"
		profileBody := `{"backend":"lorem"}`
		profileResp, body := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName, profileBody, "Content-Type: application/json")
		profileResp.Body.Close()
		if profileResp.StatusCode != http.StatusOK {
			t.Fatalf("profile: %d: %s", profileResp.StatusCode, body)
		}
		t.Cleanup(func() {
			r, _, _ := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/profiles/"+profileName, "")
			if r != nil {
				r.Body.Close()
			}
		})

		cap := 10
		listenerPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"openai","profile":"%s","record_request_body_cap_bytes":%d}`, profileName, cap)
		listenerResp, listenerBody := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
		listenerResp.Body.Close()
		if listenerResp.StatusCode != http.StatusOK {
			t.Fatalf("listener: %d: %s", listenerResp.StatusCode, listenerBody)
		}
		t.Cleanup(func() {
			r, _, _ := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName, "")
			if r != nil {
				r.Body.Close()
			}
		})
		var lv struct {
			BaseURL string `json:"base_url"`
		}
		mustJSONUnmarshal(t, listenerBody, &lv)

		longBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"this is a much longer body than 10 bytes"}]}`
		resp, _ := doRequest(t, lv.BaseURL, http.MethodPost, "/v1/chat/completions", longBody,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(calls))
		}
		if calls[0].Request.BodyTruncatedBytes <= 0 {
			t.Fatalf("body_truncated_bytes: got %d, want > 0", calls[0].Request.BodyTruncatedBytes)
		}
		if len(calls[0].Request.Body) != cap {
			t.Fatalf("request body len: got %d, want %d", len(calls[0].Request.Body), cap)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		listenerName := "openai-lorem-listener"
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})
		clearCalls(t, admin.baseURL, listenerName)

		resp, _ := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(calls))
		}
		c := calls[0]
		if c.Response.Stream == nil {
			t.Fatal("response.stream is nil for streaming request")
		}
		if len(c.Response.Stream.Events) == 0 {
			t.Fatal("response.stream.events is empty")
		}
		if c.Response.Body != "" {
			t.Fatalf("response.body should be empty for streaming, got %d bytes", len(c.Response.Body))
		}
	})

	t.Run("reset", func(t *testing.T) {
		listenerName := "openai-lorem-listener"
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})
		clearCalls(t, admin.baseURL, listenerName)

		for i := 0; i < 2; i++ {
			resp, _ := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			resp.Body.Close()
		}

		cleared := clearCalls(t, admin.baseURL, listenerName)
		if cleared != 2 {
			t.Fatalf("cleared: got %d, want 2", cleared)
		}

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 0 {
			t.Fatalf("expected 0 calls after clear, got %d", len(calls))
		}

		resp, _ := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		calls = getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 1 || calls[0].CallID != 1 {
			t.Fatalf("after clear: expected call_id=1, got %v", calls)
		}
	})

	t.Run("idempotent_clear", func(t *testing.T) {
		listenerName := "openai-lorem-listener"
		createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})
		clearCalls(t, admin.baseURL, listenerName)

		cleared := clearCalls(t, admin.baseURL, listenerName)
		if cleared != 0 {
			t.Fatalf("cleared on empty: got %d, want 0", cleared)
		}
	})

	t.Run("listener_deletion_drops_history", func(t *testing.T) {
		profileName := "openai-del-profile"
		listenerName := "openai-del-listener"
		profileBody := `{"backend":"lorem"}`
		profileResp, _ := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName, profileBody, "Content-Type: application/json")
		profileResp.Body.Close()
		t.Cleanup(func() {
			r, _, _ := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/profiles/"+profileName, "")
			if r != nil {
				r.Body.Close()
			}
		})

		listenerPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"openai","profile":"%s"}`, profileName)
		listenerResp, listenerBody := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
		listenerResp.Body.Close()
		var lv struct {
			BaseURL string `json:"base_url"`
		}
		mustJSONUnmarshal(t, listenerBody, &lv)

		resp, _ := doRequest(t, lv.BaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		delResp, _ := doRequest(t, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName, "")
		delResp.Body.Close()

		listenerResp2, listenerBody2 := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
		listenerResp2.Body.Close()
		mustJSONUnmarshal(t, listenerBody2, &lv)
		t.Cleanup(func() {
			r, _, _ := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName, "")
			if r != nil {
				r.Body.Close()
			}
		})

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 0 {
			t.Fatalf("expected 0 calls after delete+recreate, got %d", len(calls))
		}
	})

	t.Run("upsert_resets_history", func(t *testing.T) {
		profileName1 := "openai-upsert-lorem"
		profileName2 := "openai-upsert-faker"
		listenerName := "openai-upsert-listener"
		doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName1, `{"backend":"lorem"}`, "Content-Type: application/json")
		doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName2, `{"backend":"faker"}`, "Content-Type: application/json")
		t.Cleanup(func() {
			c := &http.Client{Timeout: 5 * time.Second}
			r, _, _ := doRequestRaw(c, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName, "")
			if r != nil {
				r.Body.Close()
			}
			r, _, _ = doRequestRaw(c, admin.baseURL, http.MethodDelete, "/_zolem/profiles/"+profileName1, "")
			if r != nil {
				r.Body.Close()
			}
			r, _, _ = doRequestRaw(c, admin.baseURL, http.MethodDelete, "/_zolem/profiles/"+profileName2, "")
			if r != nil {
				r.Body.Close()
			}
		})

		listenerPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"openai","profile":"%s"}`, profileName1)
		listenerResp, listenerBody := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
		listenerResp.Body.Close()
		var lv struct {
			BaseURL string `json:"base_url"`
		}
		mustJSONUnmarshal(t, listenerBody, &lv)

		resp, _ := doRequest(t, lv.BaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 1 {
			t.Fatalf("before upsert: expected 1 call, got %d", len(calls))
		}

		upsertPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"openai","profile":"%s"}`, profileName2)
		upsertResp, _ := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, upsertPayload, "Content-Type: application/json")
		upsertResp.Body.Close()

		calls = getCalls(t, admin.baseURL, listenerName)
		if len(calls) != 0 {
			t.Fatalf("after upsert: expected 0 calls (history reset on upsert), got %d", len(calls))
		}
	})

	t.Run("concurrent_requests", func(t *testing.T) {
		listenerName := "openai-lorem-listener"
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})
		clearCalls(t, admin.baseURL, listenerName)

		const n = 10
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				resp, _ := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
					`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
					"Content-Type: application/json", "Authorization: Bearer sk-test")
				resp.Body.Close()
			}()
		}
		wg.Wait()

		calls := getCalls(t, admin.baseURL, listenerName)
		if len(calls) != n {
			t.Fatalf("expected %d calls, got %d", n, len(calls))
		}
		seen := make(map[int64]bool)
		for _, c := range calls {
			if seen[c.CallID] {
				t.Fatalf("duplicate call_id: %d", c.CallID)
			}
			seen[c.CallID] = true
		}
	})

	t.Run("404_on_nonexistent_listener", func(t *testing.T) {
		client := &http.Client{Timeout: 5 * time.Second}
		getResp, _, err := doRequestRaw(client, admin.baseURL, http.MethodGet, "/_zolem/listeners/no-such-listener/calls", "")
		if err != nil {
			t.Fatalf("GET calls: %v", err)
		}
		getResp.Body.Close()
		if getResp.StatusCode != 404 {
			t.Fatalf("GET status: got %d, want 404", getResp.StatusCode)
		}

		delResp, _, err := doRequestRaw(client, admin.baseURL, http.MethodDelete, "/_zolem/listeners/no-such-listener/calls", "")
		if err != nil {
			t.Fatalf("DELETE calls: %v", err)
		}
		delResp.Body.Close()
		if delResp.StatusCode != 404 {
			t.Fatalf("DELETE status: got %d, want 404", delResp.StatusCode)
		}
	})
}
