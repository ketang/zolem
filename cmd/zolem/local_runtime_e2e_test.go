package main_test

import (
	"bytes"
	"context"
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

	t.Run("faker", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "faker",
		})

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

	t.Run("fixture", func(t *testing.T) {
		fixturesDir := t.TempDir()
		copyTestdataFixtures(t, repoRoot, fixturesDir)
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
		// Each fixture needs a match.wasm; the always-match module from
		// integration_helpers_test.go matches every request.
		if err := os.WriteFile(filepath.Join(fixtureDst, "match.wasm"), alwaysMatchWASM, 0o644); err != nil {
			t.Fatalf("write match.wasm: %v", err)
		}
	}
}

// startLocalAdminServiceWithFixtures starts the cross-process local admin server
// with -local-fixtures-dir set so fixture-backend profiles can be exercised.
// It deliberately mirrors startLocalAdminService rather than extending it, to
// keep the existing helper signature stable for parallel test work on this file.
func startLocalAdminServiceWithFixtures(t *testing.T, repoRoot, fixturesDir string) *localAdminService {
	t.Helper()

	workDir := t.TempDir()
	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-local-admin-addr", adminAddr, "-local-fixtures-dir", fixturesDir)
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
