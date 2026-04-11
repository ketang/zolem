package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/provider/gemini"
)

var alwaysMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

var errProbeNotReady = errors.New("service not ready")

type serviceProcess struct {
	baseURL string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	errCh   chan error
	logs    *bytes.Buffer
	once    sync.Once
}

func TestMain_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	svc := startService(t, repoRoot)
	t.Cleanup(svc.Close)

	t.Run("anthropic", func(t *testing.T) {
		resp, body := doRequest(t, svc.baseURL, http.MethodPost, "acme.api.anthropic.zolem.dev", "/v1/messages", `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, map[string]string{
			"Content-Type": "application/json",
			"x-api-key":    "sk-test",
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}
		var got anthropic.MessagesResponse
		mustJSONUnmarshal(t, body, &got)
		if got.Type != "message" {
			t.Fatalf("type: got %q, want message", got.Type)
		}
		if got.Role != "assistant" {
			t.Fatalf("role: got %q, want assistant", got.Role)
		}
		if len(got.Content) != 1 || got.Content[0].Text != "Fixture says hello from anthropic." {
			t.Fatalf("content: got %#v", got.Content)
		}
		if got.Model != "claude-3-5-sonnet-20241022" {
			t.Fatalf("model: got %q", got.Model)
		}
		if got.StopReason != "end_turn" {
			t.Fatalf("stop_reason: got %q, want end_turn", got.StopReason)
		}
		if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 5 {
			t.Fatalf("usage: got %#v", got.Usage)
		}
	})

	t.Run("openai-stream", func(t *testing.T) {
		resp, body := doRequest(t, svc.baseURL, http.MethodPost, "acme.api.openai.zolem.dev", "/v1/chat/completions", `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`, map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer sk-test",
			"Accept":        "text/event-stream",
		})
		defer resp.Body.Close()

		assertSSEHeaders(t, resp.Header)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		records := sseRecords(t, body)
		if len(records) < 8 {
			t.Fatalf("expected at least 8 SSE records, got %d\n%s", len(records), body)
		}
		if strings.TrimSpace(records[len(records)-1]) != "data: [DONE]" {
			t.Fatalf("terminal marker missing, got %q", records[len(records)-1])
		}
		dataRecords := records[:len(records)-1]
		var combined strings.Builder
		for i, record := range dataRecords {
			var chunk openAIStreamChunk
			mustJSONUnmarshal(t, sseDataPayload(t, record), &chunk)
			if chunk.Object != "chat.completion.chunk" {
				t.Fatalf("chunk %d object: got %q, want chat.completion.chunk", i, chunk.Object)
			}
			// OpenAI streams end with a finish chunk, then a usage-only chunk with
			// empty choices, and only then the [DONE] sentinel.
			switch i {
			case 0:
				if len(chunk.Choices) != 1 {
					t.Fatalf("first chunk choices: got %d, want 1", len(chunk.Choices))
				}
				if chunk.Choices[0].Delta.Role != "assistant" || chunk.Choices[0].Delta.Content != "" {
					t.Fatalf("first chunk delta: %#v", chunk.Choices[0].Delta)
				}
			case len(dataRecords) - 2:
				if len(chunk.Choices) != 1 {
					t.Fatalf("final completion chunk choices: got %d, want 1", len(chunk.Choices))
				}
				if chunk.Choices[0].FinishReason == nil || *chunk.Choices[0].FinishReason != "stop" {
					t.Fatalf("final completion chunk finish reason: %#v", chunk.Choices[0].FinishReason)
				}
			case len(dataRecords) - 1:
				if len(chunk.Choices) != 0 {
					t.Fatalf("usage chunk choices: got %d, want 0", len(chunk.Choices))
				}
				if chunk.Usage == nil || chunk.Usage.PromptTokens != 7 || chunk.Usage.CompletionTokens != 5 || chunk.Usage.TotalTokens != 12 {
					t.Fatalf("usage chunk: %#v", chunk.Usage)
				}
			default:
				if len(chunk.Choices) != 1 {
					t.Fatalf("content chunk %d choices: got %d, want 1", i, len(chunk.Choices))
				}
				if chunk.Choices[0].FinishReason != nil {
					t.Fatalf("content chunk %d unexpectedly had finish reason: %#v", i, chunk.Choices[0].FinishReason)
				}
				combined.WriteString(chunk.Choices[0].Delta.Content)
			}
		}
		if combined.String() != "Fixture says hello from openai." {
			t.Fatalf("combined stream content: got %q", combined.String())
		}
	})

	t.Run("gemini-stream", func(t *testing.T) {
		resp, body := doRequest(t, svc.baseURL, http.MethodPost, "acme.api.gemini.zolem.dev", "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, map[string]string{
			"Content-Type":   "application/json",
			"x-goog-api-key": "test-key",
		})
		defer resp.Body.Close()

		assertSSEHeaders(t, resp.Header)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		records := sseRecords(t, body)
		if len(records) < 4 {
			t.Fatalf("expected at least 4 SSE records, got %d\n%s", len(records), body)
		}
		var combined strings.Builder
		for i, record := range records {
			var chunk gemini.GenerateContentResponse
			mustJSONUnmarshal(t, sseDataPayload(t, record), &chunk)
			if len(chunk.Candidates) != 1 {
				t.Fatalf("chunk %d candidates: got %d, want 1", i, len(chunk.Candidates))
			}
			if chunk.ModelVersion != "gemini-2.0-flash" {
				t.Fatalf("chunk %d model version: got %q, want gemini-2.0-flash", i, chunk.ModelVersion)
			}
			if chunk.Candidates[0].Index != 0 {
				t.Fatalf("chunk %d index: got %d, want 0", i, chunk.Candidates[0].Index)
			}
			if i < len(records)-1 && chunk.Candidates[0].FinishReason != "NONE" {
				t.Fatalf("chunk %d finish reason: got %q, want NONE", i, chunk.Candidates[0].FinishReason)
			}
			if i == len(records)-1 && chunk.Candidates[0].FinishReason != "STOP" {
				t.Fatalf("last chunk finish reason: got %q, want STOP", chunk.Candidates[0].FinishReason)
			}
			if len(chunk.Candidates[0].Content.Parts) != 1 {
				t.Fatalf("chunk %d parts: got %d, want 1", i, len(chunk.Candidates[0].Content.Parts))
			}
			combined.WriteString(chunk.Candidates[0].Content.Parts[0].Text)
		}
		if combined.String() != "Fixture says hello from gemini." {
			t.Fatalf("combined stream content: got %q", combined.String())
		}
	})

	t.Run("unmatched-host", func(t *testing.T) {
		resp, body := doRequest(t, svc.baseURL, http.MethodPost, "unknown.host.dev", "/anything", `{}`, map[string]string{
			"Content-Type": "application/json",
		})
		defer resp.Body.Close()

		if got := resp.Header.Get("X-Zolem-Error"); got != "true" {
			t.Fatalf("X-Zolem-Error: got %q, want true", got)
		}
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status: got %d, want 502", resp.StatusCode)
		}
		var payload map[string]string
		mustJSONUnmarshal(t, body, &payload)
		if payload["zolem_error"] != "no route matched host: unknown.host.dev" {
			t.Fatalf("payload: got %#v", payload)
		}
	})
}

func startService(t *testing.T, repoRoot string) *serviceProcess {
	t.Helper()

	workDir := t.TempDir()
	cacheDir := filepath.Join(workDir, "spec-cache")
	fixturesDir := filepath.Join(workDir, "fixtures")
	mustMkdir(t, cacheDir)
	mustMkdir(t, fixturesDir)

	writeSpecCache(t, cacheDir)
	writeFixtures(t, fixturesDir)

	port := pickPort(t)
	configPath := filepath.Join(workDir, "zolem.yaml")
	writeConfig(t, configPath, port, cacheDir, fixturesDir)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-config", configPath)
	cmd.Dir = filepath.Join(repoRoot, "cmd", "zolem")
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(workDir, "gocache"))
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start zolem: %v", err)
	}

	svc := &serviceProcess{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}
	t.Cleanup(svc.Close)

	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	if err := waitForReady(client, svc, anthropicReadyProbe); err != nil {
		t.Fatalf("zolem did not become ready: %v\nlogs:\n%s", err, logs.String())
	}
	return svc
}

func (s *serviceProcess) Close() {
	s.once.Do(func() {
		s.cancel()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
	})
}

func anthropicReadyProbe(client *http.Client, baseURL string) error {
	resp, body, err := doRequestRaw(client, baseURL, http.MethodPost, "acme.api.anthropic.zolem.dev", "/v1/messages", `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, map[string]string{
		"Content-Type": "application/json",
		"x-api-key":    "sk-test",
	})
	if err != nil {
		return errProbeNotReady
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe status %d: %s", resp.StatusCode, body)
	}

	var got anthropic.MessagesResponse
	if err := json.Unmarshal(body, &got); err != nil {
		return fmt.Errorf("probe decode: %w", err)
	}
	if got.Type != "message" || got.Role != "assistant" || len(got.Content) != 1 || got.Content[0].Text != "Fixture says hello from anthropic." {
		return fmt.Errorf("probe response mismatch: %#v", got)
	}
	return nil
}

func waitForReady(client *http.Client, svc *serviceProcess, probe func(*http.Client, string) error) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-svc.done:
			err := <-svc.errCh
			if err == nil {
				return errors.New("service exited before readiness")
			}
			return fmt.Errorf("service exited before readiness: %w", err)
		default:
		}

		err := probe(client, svc.baseURL)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errProbeNotReady):
			time.Sleep(100 * time.Millisecond)
			continue
		default:
			return err
		}
	}
	return fmt.Errorf("timed out waiting for readiness")
}

func doRequest(t *testing.T, baseURL, method, host, path, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	resp, bodyBytes, err := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, baseURL, method, host, path, body, headers)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp, bodyBytes
}

func doRequestRaw(client *http.Client, baseURL, method, host, path, body string, headers map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, bodyBytes, nil
}

func mustJSONUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode JSON: %v\npayload:\n%s", err, data)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeConfig(t *testing.T, path string, port int, cacheDir, fixturesDir string) {
	t.Helper()
	cfg := fmt.Sprintf(`server:
  addr: 127.0.0.1:%d
mode: fixture
specs:
  cache_dir: %s
  refresh_interval: 1h
fixtures:
  dir: %s
  watch: false
ollama:
  enabled: false
routes:
  - host: "*.api.anthropic.zolem.dev"
    provider: anthropic
    labels:
      tenant: "{1}"
  - host: "*.api.openai.zolem.dev"
    provider: openai
    labels:
      tenant: "{1}"
  - host: "*.api.gemini.zolem.dev"
    provider: gemini
    labels:
      tenant: "{1}"
`, port, cacheDir, fixturesDir)
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeSpecCache(t *testing.T, cacheDir string) {
	t.Helper()
	writeJSONFile(t, filepath.Join(cacheDir, "anthropic-v1.json"), []byte(minimalAnthropicSchema))
	writeJSONFile(t, filepath.Join(cacheDir, "openai-v1.json"), []byte(minimalOpenAISchema))
	writeJSONFile(t, filepath.Join(cacheDir, "gemini-v1.json"), []byte(minimalGeminiSchema))
	writeJSONFile(t, filepath.Join(cacheDir, "gemini-v1beta.json"), []byte(minimalGeminiSchema))
}

func writeFixtures(t *testing.T, fixturesDir string) {
	t.Helper()
	writeFixture(t, filepath.Join(fixturesDir, "anthropic-messages"), `id: anthropic-messages
provider: anthropic
version: v1
stream: false
status: 200
`, `{
  "id": "msg_fixture",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Fixture says hello from anthropic."}],
  "model": "claude-3-5-sonnet-20241022",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 5}
}`)
	writeFixture(t, filepath.Join(fixturesDir, "openai-chat"), `id: openai-chat
provider: openai
version: v1
stream: true
status: 200
`, `{
  "id": "chatcmpl-fixture",
  "object": "chat.completion",
  "created": 1,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "Fixture says hello from openai."},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12}
}`)
	writeFixture(t, filepath.Join(fixturesDir, "gemini-content"), `id: gemini-content
provider: gemini
version: v1beta
stream: true
status: 200
`, `{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [{"text": "Fixture says hello from gemini."}]
      },
      "finishReason": "STOP",
      "index": 0
    }
  ],
  "usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 5, "totalTokenCount": 14},
  "modelVersion": "gemini-2.0-flash"
}`)
}

func writeFixture(t *testing.T, dir, metaYAML, responseJSON string) {
	t.Helper()
	mustMkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(metaYAML), 0o644); err != nil {
		t.Fatalf("write meta.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(responseJSON), 0o644); err != nil {
		t.Fatalf("write response.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "match.wasm"), alwaysMatchWASM, 0o644); err != nil {
		t.Fatalf("write match.wasm: %v", err)
	}
}

func writeJSONFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func pickPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func assertSSEHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if got := header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type: got %q, want text/event-stream", got)
	}
	if got := header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control: got %q, want no-cache", got)
	}
	if got := header.Get("Connection"); got != "keep-alive" {
		t.Fatalf("Connection: got %q, want keep-alive", got)
	}
	if got := header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering: got %q, want no", got)
	}
}

func sseRecords(t *testing.T, body []byte) []string {
	t.Helper()
	chunks := strings.Split(strings.TrimSpace(string(body)), "\n\n")
	records := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if trimmed := strings.TrimSpace(chunk); trimmed != "" {
			records = append(records, trimmed)
		}
	}
	if len(records) == 0 {
		t.Fatalf("expected SSE records, got empty body:\n%s", body)
	}
	return records
}

func sseDataPayload(t *testing.T, record string) []byte {
	t.Helper()
	lines := strings.Split(record, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			return []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	t.Fatalf("record does not contain data payload: %q", record)
	return nil
}

func assertContainsInOrder(t *testing.T, body []byte, needles []string) {
	t.Helper()
	haystack := string(body)
	offset := 0
	for _, needle := range needles {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			t.Fatalf("missing %q in body:\n%s", needle, body)
		}
		offset += idx + len(needle)
	}
}

type openAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIStreamUsage   `json:"usage,omitempty"`
}

type openAIStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

type openAIStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIStreamUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

const minimalAnthropicSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "max_tokens", "messages"],
  "properties": {
    "model": {"type": "string"},
    "max_tokens": {"type": "integer"},
    "messages": {"type": "array"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}`

const minimalOpenAISchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "messages"],
  "properties": {
    "model": {"type": "string"},
    "messages": {"type": "array"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}`

const minimalGeminiSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["contents"],
  "properties": {
    "contents": {"type": "array"},
    "generationConfig": {"type": "object"}
  },
  "additionalProperties": true
}`
