package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
)

type fixedTokenGenerator []string

func (g fixedTokenGenerator) Generate(int) []string { return []string(g) }

type firstFixtureSelector struct{}

func (firstFixtureSelector) Select(_ context.Context, _ fixture.MatchRequest, candidates []fixture.Fixture) (*fixture.Fixture, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	return &candidates[0], nil
}

func TestResponsesAuthorization(t *testing.T) {
	tests := []struct {
		auth string
		want bool
	}{
		{auth: "Bearer sk-test", want: true},
		{auth: " Bearer sk-test ", want: true},
		{auth: "", want: false},
		{auth: "Bearer sk-other", want: false},
	}
	for _, tt := range tests {
		if got := validResponsesAuthorization(tt.auth); got != tt.want {
			t.Fatalf("validResponsesAuthorization(%q) = %v, want %v", tt.auth, got, tt.want)
		}
	}
}

func TestResponsesWSRejectsMissingAuthorization(t *testing.T) {
	h := NewHandler(specs.NewValidator(), nil, response.NewLoremGenerator(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if !strings.Contains(rr.Body.String(), "not allowed") {
		t.Fatalf("body = %s, want permission guidance", rr.Body.String())
	}
}

func TestResponsesWSPrewarmAndGeneratedEvents(t *testing.T) {
	h := NewHandler(specs.NewValidator(), nil, fixedTokenGenerator{"hello", " ", "world"}, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rt := runtimecfg.ListenerRuntime{Profile: runtimecfg.RuntimeProfile{Backend: runtimecfg.BackendLorem}}
		h.ServeHTTP(w, req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt)))
	}))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	header := http.Header{"Authorization": []string{"Bearer sk-test"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial responses websocket: %v", err)
	}
	defer conn.Close()

	generate := false
	if err := conn.WriteJSON(responsesWSFrame{Type: "response.create", Generate: &generate}); err != nil {
		t.Fatalf("write prewarm frame: %v", err)
	}
	prewarmTypes := readResponseEventTypes(t, conn, 2)
	if strings.Join(prewarmTypes, ",") != "response.created,response.completed" {
		t.Fatalf("prewarm event types = %v", prewarmTypes)
	}

	if err := conn.WriteJSON(responsesWSFrame{Type: "ignored"}); err != nil {
		t.Fatalf("write ignored frame: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("ignored")); err != nil {
		t.Fatalf("write ignored binary frame: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("{")); err != nil {
		t.Fatalf("write invalid json frame: %v", err)
	}

	if err := conn.WriteJSON(responsesWSFrame{Type: "response.create"}); err != nil {
		t.Fatalf("write generated frame: %v", err)
	}
	generatedTypes := readResponseEventTypes(t, conn, 6)
	wantGenerated := "response.created,response.output_item.added,response.output_text.delta,response.output_text.done,response.output_item.done,response.completed"
	if strings.Join(generatedTypes, ",") != wantGenerated {
		t.Fatalf("generated event types = %v, want %s", generatedTypes, wantGenerated)
	}
}

func TestResponseCreateEventsFixtures(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	fixtureBody := []byte(`[{"type":"response.completed","sequence_number":0}]`)
	h := NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, []fixture.Fixture{{
		ID:           "responses-fixture",
		Provider:     "openai",
		Version:      "v1-responses",
		ResponseBody: fixtureBody,
	}}, firstFixtureSelector{}), response.NewLoremGenerator(), nil, nil)

	ctx := runtimecfg.WithListenerRuntime(context.Background(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Backend: runtimecfg.BackendFixture},
	})
	events, err := h.responseCreateEvents(ctx, []byte(`{"type":"response.create"}`))
	if err != nil {
		t.Fatalf("responseCreateEvents fixture: %v", err)
	}
	if len(events) != 1 || !strings.Contains(string(events[0]), "response.completed") {
		t.Fatalf("fixture events = %s", events)
	}

	h = NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, []fixture.Fixture{{
		ID:           "bad-responses-fixture",
		Provider:     "openai",
		Version:      "v1-responses",
		ResponseBody: []byte(`{"not":"an array"}`),
	}}, firstFixtureSelector{}), response.NewLoremGenerator(), nil, nil)
	_, err = h.responseCreateEvents(ctx, []byte(`{"type":"response.create"}`))
	if err == nil || !strings.Contains(err.Error(), "must be a JSON array") {
		t.Fatalf("bad fixture error = %v", err)
	}
}

func TestResponseEventBuilders(t *testing.T) {
	failed := responseFailedEvents("boom")
	if len(failed) != 1 {
		t.Fatalf("failed events len = %d, want 1", len(failed))
	}
	var failedEvent map[string]any
	if err := json.Unmarshal(failed[0], &failedEvent); err != nil {
		t.Fatalf("decode failed event: %v", err)
	}
	if failedEvent["type"] != "response.failed" {
		t.Fatalf("failed event type = %v", failedEvent["type"])
	}

	text := textResponseEvents("hello")
	if len(text) != 6 {
		t.Fatalf("text events len = %d, want 6", len(text))
	}
	var delta map[string]any
	if err := json.Unmarshal(text[2], &delta); err != nil {
		t.Fatalf("decode delta event: %v", err)
	}
	if delta["delta"] != "hello" {
		t.Fatalf("delta = %v, want hello", delta["delta"])
	}
}

func readResponseEventTypes(t *testing.T, conn *websocket.Conn, n int) []string {
	t.Helper()
	types := make([]string, 0, n)
	for i := 0; i < n; i++ {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read event %d: %v", i, err)
		}
		if messageType != websocket.TextMessage {
			t.Fatalf("event %d message type = %d, want text", i, messageType)
		}
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode event %d: %v\n%s", i, err, payload)
		}
		types = append(types, event.Type)
	}
	return types
}
