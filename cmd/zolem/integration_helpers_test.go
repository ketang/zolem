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

func startFixedService(t *testing.T, provider string) *serviceProcess {
	t.Helper()

	repoRoot := repoRoot(t)
	workDir := t.TempDir()
	fixturesDir := filepath.Join(workDir, "fixtures")
	mustMkdir(t, fixturesDir)
	writeFixtures(t, fixturesDir)

	port := pickPort(t)
	var readinessPath string
	var readinessBody string
	var readinessHeaders []string

	switch provider {
	case "anthropic":
		readinessPath = "/v1/messages"
		readinessBody = `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`
		readinessHeaders = []string{"x-api-key: sk-test"}
	case "openai":
		readinessPath = "/v1/chat/completions"
		readinessBody = `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
		readinessHeaders = []string{"Authorization: Bearer sk-test"}
	case "gemini":
		readinessPath = "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse"
		readinessBody = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
		readinessHeaders = []string{"x-goog-api-key: test-key"}
	default:
		t.Fatalf("unsupported provider %q", provider)
	}

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-local-addr", fmt.Sprintf("127.0.0.1:%d", port), "-local-provider", provider, "-local-profile", "demo", "-local-backend", "fixture", "-local-fixtures-dir", fixturesDir)
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
	if err := waitForReady(client, svc, func(c *http.Client, baseURL string) error {
		resp, body, err := doRequestRaw(c, baseURL, http.MethodPost, readinessPath, readinessBody, readinessHeaders...)
		if err != nil {
			return errProbeNotReady
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("probe status %d: %s", resp.StatusCode, body)
		}
		return nil
	}); err != nil {
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

func waitForReady(client *http.Client, svc *serviceProcess, probe func(*http.Client, string) error) error {
	deadline := time.Now().Add(45 * time.Second)
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

func doRequest(t *testing.T, baseURL, method, path, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	resp, bodyBytes, err := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, baseURL, method, path, body, headers...)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp, bodyBytes
}

func doRequestRaw(client *http.Client, baseURL, method, path, body string, headers ...string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for _, header := range headers {
		parts := strings.SplitN(header, ": ", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("invalid header %q", header)
		}
		req.Header.Set(parts[0], parts[1])
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

func pickPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listeners are not permitted in this environment: %v", err)
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

// parseSSEDataPayloads splits an SSE response body into ordered data-event
// payloads. Each entry is the raw bytes between `data: ` and the trailing
// blank line, in source order, including the `[DONE]` terminator.
func parseSSEDataPayloads(t *testing.T, body []byte) [][]byte {
	t.Helper()
	records := sseRecords(t, body)
	out := make([][]byte, 0, len(records))
	for _, rec := range records {
		out = append(out, sseDataPayload(t, rec))
	}
	return out
}

// assertOpenAIStreamShape pins the exact SSE event-frame layout that zolem's
// OpenAI streaming chunker emits: 1 role-opener + wantTokens token deltas + 1
// finish-reason chunk + 1 usage chunk + 1 [DONE] terminator. Total event
// count must equal wantTokens + 4, [DONE] must be the final event, and no
// events may follow [DONE]. Pass exact wantTokens for deterministic backends
// (lorem/faker fixed at 30, fixture = word count of the rendered string,
// ollama = number of upstream content deltas, wasm = number of generator
// tokens). A regression that collapses chunks, drops [DONE], emits trailing
// events after [DONE], reorders the frame structure, or changes per-chunk
// content shape (role on opener, finish_reason on finalChunk, usage on
// usageChunk) fails this assertion.
func assertOpenAIStreamShape(t *testing.T, body []byte, wantTokens int) {
	t.Helper()
	payloads := parseSSEDataPayloads(t, body)
	wantTotal := wantTokens + 4
	if len(payloads) != wantTotal {
		t.Fatalf("event count: got %d, want %d (1 role-opener + %d token deltas + 1 finalChunk + 1 usageChunk + 1 [DONE]); body:\n%s",
			len(payloads), wantTotal, wantTokens, body)
	}

	for i, p := range payloads {
		if string(p) == "[DONE]" && i != len(payloads)-1 {
			t.Fatalf("[DONE] at index %d; must be the final event (no trailing events allowed); got %d trailing", i, len(payloads)-1-i)
		}
	}
	if last := payloads[len(payloads)-1]; string(last) != "[DONE]" {
		t.Fatalf("last event must be [DONE]; got %q", last)
	}

	var first openAIStreamChunk
	if err := json.Unmarshal(payloads[0], &first); err != nil {
		t.Fatalf("role-opener unmarshal: %v; payload=%s", err, payloads[0])
	}
	if len(first.Choices) != 1 {
		t.Fatalf("role-opener: choices len = %d, want 1; payload=%s", len(first.Choices), payloads[0])
	}
	if first.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("role-opener delta.role = %q, want \"assistant\"", first.Choices[0].Delta.Role)
	}
	if first.Choices[0].Delta.Content != "" {
		t.Fatalf("role-opener delta.content = %q, want empty", first.Choices[0].Delta.Content)
	}
	if first.Choices[0].FinishReason != nil {
		t.Fatalf("role-opener finish_reason = %q, want null", *first.Choices[0].FinishReason)
	}

	for i := 1; i <= wantTokens; i++ {
		var c openAIStreamChunk
		if err := json.Unmarshal(payloads[i], &c); err != nil {
			t.Fatalf("token chunk %d unmarshal: %v; payload=%s", i, err, payloads[i])
		}
		if len(c.Choices) != 1 {
			t.Fatalf("token chunk %d: choices len = %d, want 1; payload=%s", i, len(c.Choices), payloads[i])
		}
		if c.Choices[0].FinishReason != nil {
			t.Fatalf("token chunk %d: finish_reason = %q, want null", i, *c.Choices[0].FinishReason)
		}
	}

	finalIdx := wantTokens + 1
	var fin openAIStreamChunk
	if err := json.Unmarshal(payloads[finalIdx], &fin); err != nil {
		t.Fatalf("finalChunk unmarshal: %v; payload=%s", err, payloads[finalIdx])
	}
	if len(fin.Choices) != 1 {
		t.Fatalf("finalChunk: choices len = %d, want 1; payload=%s", len(fin.Choices), payloads[finalIdx])
	}
	if fin.Choices[0].FinishReason == nil || *fin.Choices[0].FinishReason != "stop" {
		var got string
		if fin.Choices[0].FinishReason != nil {
			got = *fin.Choices[0].FinishReason
		}
		t.Fatalf("finalChunk finish_reason: got %q, want \"stop\"", got)
	}
	if fin.Choices[0].Delta.Content != "" || fin.Choices[0].Delta.Role != "" {
		t.Fatalf("finalChunk delta should be empty; got role=%q content=%q",
			fin.Choices[0].Delta.Role, fin.Choices[0].Delta.Content)
	}

	usageIdx := wantTokens + 2
	var usage openAIStreamChunk
	if err := json.Unmarshal(payloads[usageIdx], &usage); err != nil {
		t.Fatalf("usageChunk unmarshal: %v; payload=%s", err, payloads[usageIdx])
	}
	if usage.Usage == nil {
		t.Fatalf("usageChunk: usage missing; payload=%s", payloads[usageIdx])
	}
	if len(usage.Choices) != 0 {
		t.Fatalf("usageChunk: choices len = %d, want 0; payload=%s", len(usage.Choices), payloads[usageIdx])
	}
}

type recordedCall struct {
	CallID      int64          `json:"call_id"`
	Listener    string         `json:"listener"`
	ReceivedAt  string         `json:"received_at"`
	CompletedAt string         `json:"completed_at"`
	LatencyMS   int64          `json:"latency_ms"`
	Request     recordedReq    `json:"request"`
	Response    recordedResp   `json:"response"`
}

type recordedReq struct {
	Method             string              `json:"method"`
	Path               string              `json:"path"`
	Query              string              `json:"query"`
	Headers            map[string][]string  `json:"headers"`
	RemoteAddr         string              `json:"remote_addr"`
	Body               string              `json:"body,omitempty"`
	BodyBase64         string              `json:"body_base64,omitempty"`
	BodyTruncatedBytes int                 `json:"body_truncated_bytes"`
}

type recordedResp struct {
	Status             int                 `json:"status"`
	Headers            map[string][]string `json:"headers"`
	Body               string              `json:"body,omitempty"`
	BodyBase64         string              `json:"body_base64,omitempty"`
	BodyTruncatedBytes int                 `json:"body_truncated_bytes"`
	Stream             *streamRecord       `json:"stream"`
}

type streamRecord struct {
	EventCount      int           `json:"event_count"`
	Events          []streamEvent `json:"events"`
	EventsTruncated int           `json:"events_truncated"`
}

type streamEvent struct {
	ReceivedAt string `json:"received_at"`
	Event      string `json:"event"`
	Data       string `json:"data"`
}

func getCalls(t *testing.T, adminBaseURL, listenerName string) []recordedCall {
	t.Helper()
	resp, body := doRequest(t, adminBaseURL, http.MethodGet, "/_zolem/listeners/"+listenerName+"/calls", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getCalls %s: status %d: %s", listenerName, resp.StatusCode, body)
	}
	var envelope struct {
		Calls []recordedCall `json:"calls"`
	}
	mustJSONUnmarshal(t, body, &envelope)
	return envelope.Calls
}

func clearCalls(t *testing.T, adminBaseURL, listenerName string) int {
	t.Helper()
	resp, body := doRequest(t, adminBaseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName+"/calls", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clearCalls %s: status %d: %s", listenerName, resp.StatusCode, body)
	}
	var result struct {
		Cleared int `json:"cleared"`
	}
	mustJSONUnmarshal(t, body, &result)
	return result.Cleared
}
