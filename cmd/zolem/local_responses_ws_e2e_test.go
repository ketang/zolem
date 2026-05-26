package main_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOpenAIResponsesWebSocket_E2E(t *testing.T) {
	repoRoot := repoRoot(t)

	t.Run("auth_and_lorem", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "lorem",
		})

		_, resp, err := websocket.DefaultDialer.Dial(responsesWSURL(t, listenerBaseURL), http.Header{
			"Authorization": []string{"Bearer wrong"},
		})
		if err == nil {
			t.Fatal("Dial with wrong bearer succeeded")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("wrong bearer status: got %#v, want 403", resp)
		}

		conn := dialResponsesWS(t, listenerBaseURL)
		defer conn.Close()
		writeResponseCreate(t, conn, false)
		prewarm := readUntilResponseCompleted(t, conn)
		if got := completedResponseID(prewarm); got == "" {
			t.Fatalf("prewarm completed response id missing: %#v", prewarm)
		}

		writeResponseCreate(t, conn, nil)
		events := readUntilResponseCompleted(t, conn)
		if got := lastEventType(events); got != "response.completed" {
			t.Fatalf("last event type: got %q, want response.completed", got)
		}
		if text := joinedOutputText(events); text == "" {
			t.Fatalf("lorem websocket response text was empty: %#v", events)
		}
	})

	t.Run("fixture_sequence_and_version_isolation", func(t *testing.T) {
		fixturesDir := t.TempDir()
		writeResponsesWSFixtures(t, fixturesDir)
		admin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "fixture",
		})

		chatResp, chatBody := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		defer chatResp.Body.Close()
		assertOpenAIChatCompletion(t, chatResp, chatBody)
		if got := openAICompletionContent(t, chatBody); got == "turn-tool" || got == "turn-end" {
			t.Fatalf("chat/completions matched v1-responses fixture content %q", got)
		}

		conn := dialResponsesWS(t, listenerBaseURL)
		defer conn.Close()

		writeResponseCreate(t, conn, false)
		if got := completedResponseID(readUntilResponseCompleted(t, conn)); got != "resp_prewarm" {
			t.Fatalf("prewarm completed id: got %q, want resp_prewarm", got)
		}

		writeResponseCreate(t, conn, nil)
		if got := completedResponseID(readUntilResponseCompleted(t, conn)); got != "resp_tool" {
			t.Fatalf("first generated turn id: got %q, want resp_tool", got)
		}

		writeResponseCreate(t, conn, nil)
		if got := completedResponseID(readUntilResponseCompleted(t, conn)); got != "resp_end" {
			t.Fatalf("second generated turn id: got %q, want resp_end", got)
		}

		writeResponseCreate(t, conn, nil)
		if got := completedResponseID(readUntilResponseCompleted(t, conn)); got != "resp_end" {
			t.Fatalf("exhausted generated turn id: got %q, want resp_end", got)
		}
	})

	t.Run("chat_fixture_not_used_by_websocket", func(t *testing.T) {
		fixturesDir := t.TempDir()
		writeChatOnlyFixtures(t, fixturesDir)
		admin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(admin.Close)

		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend": "fixture",
		})

		conn := dialResponsesWS(t, listenerBaseURL)
		defer conn.Close()
		writeResponseCreate(t, conn, nil)
		events := readUntilResponseCompleted(t, conn)
		if text := joinedOutputText(events); text == "chat-only" {
			t.Fatalf("websocket matched chat/completions fixture content %q", text)
		}
		if lastEventType(events) != "response.completed" {
			t.Fatalf("websocket fallback did not complete: %#v", events)
		}
	})
}

func TestOpenAIResponsesWebSocketCallsFile_E2E(t *testing.T) {
	bin := buildZolemBinary(t)
	callsFile := filepath.Join(t.TempDir(), "calls.jsonl")
	svc := startZolemWithCallsFile(t, bin, callsFile, 0)

	conn := dialResponsesWS(t, svc.baseURL)
	writeResponseCreate(t, conn, nil)
	_ = readUntilResponseCompleted(t, conn)
	conn.Close()

	records := waitRawJSONLFile(t, callsFile, 1)
	if len(records) != 1 {
		t.Fatalf("expected 1 JSONL record, got %d: %#v", len(records), records)
	}
	rec := records[0]
	if rec["method"] != "GET" {
		t.Fatalf("method: got %#v, want GET", rec["method"])
	}
	if rec["path"] != "/v1/responses" {
		t.Fatalf("path: got %#v, want /v1/responses", rec["path"])
	}
	if rec["status"] != float64(http.StatusSwitchingProtocols) {
		t.Fatalf("status: got %#v, want 101", rec["status"])
	}
	if rec["frames_received"] == nil || rec["frames_sent"] == nil {
		t.Fatalf("missing frame counts: %#v", rec)
	}
}

func responsesWSURL(t *testing.T, baseURL string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		t.Fatalf("unsupported base URL scheme %q", u.Scheme)
	}
	u.Path = "/v1/responses"
	u.RawQuery = ""
	return u.String()
}

func dialResponsesWS(t *testing.T, baseURL string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(responsesWSURL(t, baseURL), http.Header{
		"Authorization": []string{"Bearer sk-test"},
	})
	if err != nil {
		if resp != nil {
			t.Fatalf("dial websocket: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func writeResponseCreate(t *testing.T, conn *websocket.Conn, generate any) {
	t.Helper()
	frame := map[string]any{
		"type":  "response.create",
		"model": "gpt-5-codex",
		"input": []any{},
	}
	if generate != nil {
		frame["generate"] = generate
	}
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatalf("write response.create: %v", err)
	}
}

func readUntilResponseCompleted(t *testing.T, conn *websocket.Conn) []map[string]any {
	t.Helper()
	var events []map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("unmarshal websocket event: %v\n%s", err, payload)
		}
		events = append(events, event)
		if event["type"] == "response.completed" {
			return events
		}
	}
}

func lastEventType(events []map[string]any) string {
	if len(events) == 0 {
		return ""
	}
	typ, _ := events[len(events)-1]["type"].(string)
	return typ
}

func completedResponseID(events []map[string]any) string {
	if len(events) == 0 {
		return ""
	}
	resp, _ := events[len(events)-1]["response"].(map[string]any)
	id, _ := resp["id"].(string)
	return id
}

func joinedOutputText(events []map[string]any) string {
	for _, event := range events {
		if text, ok := event["text"].(string); ok && text != "" {
			return text
		}
		if delta, ok := event["delta"].(string); ok && delta != "" {
			return delta
		}
	}
	return ""
}

func writeResponsesWSFixtures(t *testing.T, root string) {
	t.Helper()
	writeResponsesWSFixture(t, root, "turn-tool", "resp_tool", "turn-tool")
	writeResponsesWSFixture(t, root, "turn-end", "resp_end", "turn-end")
	yaml := `provider: openai
version: v1-responses
fixtures:
  - expression: 'true'
    sequence:
      id: conversation
      on_exhaust: last
      steps: [turn-tool, turn-end]
`
	if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write fixtures.yaml: %v", err)
	}
}

func writeResponsesWSFixture(t *testing.T, root, id, responseID, text string) {
	t.Helper()
	dir := filepath.Join(root, id)
	mustMkdir(t, dir)
	meta := "id: " + id + "\nprovider: openai\nversion: v1-responses\nstatus: 200\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta.yaml for %q: %v", id, err)
	}
	response := []map[string]any{
		{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": responseID, "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0, "item": map[string]any{"type": "message", "id": id + "_msg", "role": "assistant", "content": []any{}, "status": "in_progress"}},
		{"type": "response.output_text.delta", "sequence_number": 2, "item_id": id + "_msg", "output_index": 0, "content_index": 0, "delta": text},
		{"type": "response.output_text.done", "sequence_number": 3, "item_id": id + "_msg", "output_index": 0, "content_index": 0, "text": text},
		{"type": "response.output_item.done", "sequence_number": 4, "output_index": 0, "item": map[string]any{"type": "message", "id": id + "_msg", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}, "status": "completed"}},
		{"type": "response.completed", "sequence_number": 5, "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}}},
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), data, 0o644); err != nil {
		t.Fatalf("write response.json for %q: %v", id, err)
	}
}

func writeChatOnlyFixtures(t *testing.T, root string) {
	t.Helper()
	writeYAMLNamespaceFixture(t, root, "chat-only", "chat-only")
	yaml := `provider: openai
version: v1
fixtures:
  - expression: 'true'
    fixture: chat-only
`
	if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write fixtures.yaml: %v", err)
	}
}

func readRawJSONLFile(t *testing.T, path string) []map[string]any {
	t.Helper()
	records, err := readRawJSONLFileResult(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func waitRawJSONLFile(t *testing.T, path string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var records []map[string]any
	for time.Now().Before(deadline) {
		var err error
		records, err = readRawJSONLFileResult(path)
		if err == nil && len(records) >= want {
			return records
		}
		time.Sleep(50 * time.Millisecond)
	}
	return readRawJSONLFile(t, path)
}

func readRawJSONLFileResult(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var records []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
