package anthropic_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

func TestListModels_MissingAuth(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rr.Code)
	}
}

func TestListModels_ReturnsCatalogue(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data []struct {
			Type        string `json:"type"`
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
		HasMore bool    `json:"has_more"`
		FirstID *string `json:"first_id"`
		LastID  *string `json:"last_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected non-empty model list")
	}
	if resp.HasMore {
		t.Fatal("has_more should be false")
	}
	if resp.FirstID == nil || resp.LastID == nil {
		t.Fatal("first_id and last_id should be set")
	}
	for _, m := range resp.Data {
		if m.Type != "model" || m.ID == "" || m.DisplayName == "" || m.CreatedAt == "" {
			t.Fatalf("malformed model entry: %#v", m)
		}
	}
}

func TestListModels_HonoursForceLiteralPolicy(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "sk-test")
	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			Name:                "test",
			ResponseModelPolicy: runtimecfg.ResponseModelForceLiteral,
			ResponseModel:       "my-pinned-model",
		},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) == 0 || resp.Data[0].ID != "my-pinned-model" {
		t.Fatalf("expected pinned model first, got %#v", resp.Data)
	}
}

func TestCountTokens_MissingAuth(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rr.Code)
	}
}

func TestCountTokens_ReturnsEstimate(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hello world from zolem"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(body))
	req.Header.Set("x-api-key", "sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.InputTokens <= 0 {
		t.Fatalf("input_tokens: got %d, want > 0", resp.InputTokens)
	}
}

func TestCountTokens_MissingModel(t *testing.T) {
	h := newHandler(t)
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(body))
	req.Header.Set("x-api-key", "sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}
