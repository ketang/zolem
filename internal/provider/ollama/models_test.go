package ollama_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/provider/ollama"
)

func get(t *testing.T, h *ollama.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func TestVersion(t *testing.T) {
	rr := get(t, newHandler(t), "/api/version")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version == "" {
		t.Error("expected a version string")
	}
}

type tagsResponse struct {
	Models []struct {
		Name         string   `json:"name"`
		Model        string   `json:"model"`
		ModifiedAt   string   `json:"modified_at"`
		Digest       string   `json:"digest"`
		Capabilities []string `json:"capabilities"`
		Details      struct {
			Family string `json:"family"`
		} `json:"details"`
	} `json:"models"`
}

func TestTags_ReturnsApprovedCatalogue(t *testing.T) {
	rr := get(t, newHandler(t), "/api/tags")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp tagsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []string{"llama3.2:latest", "mistral:latest", "gemma3:4b"}
	if len(resp.Models) != len(want) {
		t.Fatalf("model count: got %d, want %d", len(resp.Models), len(want))
	}
	for i, w := range want {
		if resp.Models[i].Name != w {
			t.Errorf("model %d name: got %q, want %q", i, resp.Models[i].Name, w)
		}
		// `name` is the legacy field; both carry the same value.
		if resp.Models[i].Model != resp.Models[i].Name {
			t.Errorf("model %d: name %q and model %q must match", i, resp.Models[i].Name, resp.Models[i].Model)
		}
		if len(resp.Models[i].Capabilities) == 0 {
			t.Errorf("model %d missing capabilities", i)
		}
	}
}

// AGENTS.md excludes Chinese-origin LLMs from this repository. The catalogue is
// synthetic and downloads nothing, but the names must stay consistent with it.
func TestTags_ExcludesChineseOriginModels(t *testing.T) {
	rr := get(t, newHandler(t), "/api/tags")
	body := strings.ToLower(rr.Body.String())
	for _, forbidden := range []string{"qwen", "deepseek", "yi:", "baichuan", "glm", "chatglm"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("catalogue must not contain %q: %s", forbidden, rr.Body.String())
		}
	}
}

// Listings must be stable across calls so clients and tests can rely on them.
func TestTags_IsDeterministic(t *testing.T) {
	h := newHandler(t)
	first := get(t, h, "/api/tags").Body.String()
	second := get(t, h, "/api/tags").Body.String()
	if first != second {
		t.Errorf("catalogue must be deterministic:\nfirst:  %s\nsecond: %s", first, second)
	}
}

// Zolem loads no models, so an empty list is the honest answer — and a state a
// real Ollama genuinely reports.
func TestPS_ReturnsEmptyList(t *testing.T) {
	rr := get(t, newHandler(t), "/api/ps")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Errorf("expected no loaded models, got %d", len(resp.Models))
	}
	if !strings.Contains(rr.Body.String(), "[]") {
		t.Errorf("models must serialize as an empty array, not null: %s", rr.Body.String())
	}
}

func postShow(t *testing.T, h *ollama.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/show", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestShow_ReportsCapabilities(t *testing.T) {
	rr := postShow(t, newHandler(t), `{"model":"llama3.2:latest"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Capabilities []string `json:"capabilities"`
		Details      struct {
			Family string `json:"family"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Capabilities) == 0 {
		t.Fatal("expected capabilities")
	}
	var hasCompletion bool
	for _, c := range resp.Capabilities {
		if c == "completion" {
			hasCompletion = true
		}
	}
	if !hasCompletion {
		t.Errorf("every catalogue model must declare 'completion', got %v", resp.Capabilities)
	}
}

// Ollama normalizes an untagged name to :latest, so both forms resolve.
func TestShow_NormalizesUntaggedModelName(t *testing.T) {
	rr := postShow(t, newHandler(t), `{"model":"llama3.2"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("untagged name must resolve via :latest; got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestShow_UnknownModelReturnsFlatNotFound(t *testing.T) {
	rr := postShow(t, newHandler(t), `{"model":"no-such-model"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404. body: %s", rr.Code, rr.Body.String())
	}
	assertFlatError(t, rr.Body.Bytes())
}

func TestShow_MissingModelIsInvalidRequest(t *testing.T) {
	rr := postShow(t, newHandler(t), `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400. body: %s", rr.Code, rr.Body.String())
	}
	assertFlatError(t, rr.Body.Bytes())
}

// Unlike /api/show, /api/chat accepts any model: zolem has no model store, and
// response_model_policy already owns the model-name question.
func TestChat_AcceptsOffCatalogueModel(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"totally-made-up:99b","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
}
