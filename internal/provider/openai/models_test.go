package openai_test

import (
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
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "list" {
		t.Fatalf("object: got %q, want list", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected non-empty model list")
	}
	for _, m := range resp.Data {
		if m.Object != "model" || m.ID == "" {
			t.Fatalf("malformed model entry: %#v", m)
		}
	}
}

func TestListModels_HonoursForceLiteralPolicy(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
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

func TestGetModel_ByID(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var m struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.ID != "gpt-4o" || m.Object != "model" {
		t.Fatalf("unexpected model: %#v", m)
	}
}

func TestGetModel_NotFound(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models/does-not-exist", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body: %s", rr.Code, rr.Body.String())
	}
}
