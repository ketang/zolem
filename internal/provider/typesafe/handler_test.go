package typesafe_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/typesafe"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

const threeQuestionBody = `{
  "model": "jev-latest",
  "state": {"item": "widget"},
  "questions": {
    "is_fragile": {"type": "noul", "instructions": "Is this item fragile?"},
    "category": {"type": "choice", "instructions": "Pick a category", "criteria": {"electronics": "electronic devices", "tools": "hand tools"}},
    "quality": {"type": "score", "instructions": "Rate the quality", "criteria": ["poor", "average", "excellent"]}
  }
}`

// systemOneResponse mirrors the wire envelope for decoding in tests.
type systemOneResponse struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Type          string             `json:"type"`
		Noul          *float64           `json:"noul"`
		Choice        string             `json:"choice"`
		Score         *float64           `json:"score"`
		Legend        map[string]string  `json:"legend"`
		Probabilities map[string]float64 `json:"probabilities"`
		Confidence    *float64           `json:"confidence"`
	} `json:"answers"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func newHandler(t *testing.T) *typesafe.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return typesafe.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator())
}

// newSchemaHandler is newHandler with the vendored typesafe:v1 request schema
// loaded, as production startup does; a bare specs.NewValidator() skips schema
// checks entirely.
func newSchemaHandler(t *testing.T) *typesafe.Handler {
	t.Helper()
	validator := specs.NewValidator()
	if err := specs.LoadProviderSchema(validator, "typesafe", "v1", specs.VendoredFallbacks()["typesafe:v1"]); err != nil {
		t.Fatalf("load typesafe schema: %v", err)
	}
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return typesafe.NewHandler(validator, fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator())
}

func postSystemOne(t *testing.T, h *typesafe.Handler, profile runtimecfg.RuntimeProfile, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test-unchecked")
	rt := runtimecfg.ListenerRuntime{Profile: profile}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSystemOne_RequiresAuthorizationHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", bytes.NewBufferString(threeQuestionBody))
	rr := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401. body: %s", rr.Code, rr.Body.String())
	}
}

// The key's contents are accepted and ignored, matching how zolem treats
// other providers' keys; only the header's presence is validated.
func TestSystemOne_AuthorizationKeyContentsIgnored(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem}, threeQuestionBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
}

func TestSystemOne_LoremBackend_ProducesValidAnswers(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem}, threeQuestionBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}

	var resp systemOneResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Answers) != 3 {
		t.Fatalf("answers: got %d, want 3", len(resp.Answers))
	}

	noul := resp.Answers["is_fragile"]
	if noul.Type != "noul" || noul.Noul == nil || *noul.Noul != 0.5 {
		t.Errorf("noul answer: got %+v, want noul=0.5", noul)
	}

	choice := resp.Answers["category"]
	if choice.Type != "choice" || choice.Choice != "electronics" {
		t.Errorf("choice answer: got %+v, want choice=electronics (first option)", choice)
	}
	if sum := choice.Probabilities["electronics"] + choice.Probabilities["tools"]; sum < 0.999999 || sum > 1.000001 {
		t.Errorf("choice probabilities sum: got %v, want ~1", sum)
	}

	score := resp.Answers["quality"]
	// lorem puts 0.9 on level 0 and spreads 0.05 over levels 1 and 2:
	// weighted position = 0*0.9 + 1*0.05 + 2*0.05 = 0.15.
	if score.Type != "score" || score.Score == nil || *score.Score < 0.149999 || *score.Score > 0.150001 {
		t.Errorf("score answer: got %+v, want score~=0.15", score)
	}
	if len(score.Legend) != 3 {
		t.Errorf("score legend: got %d entries, want 3", len(score.Legend))
	}

	if resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
		t.Errorf("usage: got %+v, want positive token counts", resp.Usage)
	}
}

func TestSystemOne_UnmatchedFixtureFallsBackToLorem(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "fx", Backend: runtimecfg.BackendFixture}, threeQuestionBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp systemOneResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Answers["is_fragile"].Noul == nil || *resp.Answers["is_fragile"].Noul != 0.5 {
		t.Fatalf("unmatched fixture did not fall back to lorem: %s", rr.Body.String())
	}
}

func TestSystemOne_FakerBackend_DeterministicPerRequest(t *testing.T) {
	h := newHandler(t)
	profile := runtimecfg.RuntimeProfile{Name: "f", Backend: runtimecfg.BackendFaker}

	first := postSystemOne(t, h, profile, threeQuestionBody)
	second := postSystemOne(t, h, profile, threeQuestionBody)

	if first.Body.String() != second.Body.String() {
		t.Fatalf("identical requests produced different answers:\n%s\nvs\n%s", first.Body.String(), second.Body.String())
	}
}

func TestSystemOne_FakerBackend_DifferentStateDifferentAnswer(t *testing.T) {
	h := newHandler(t)
	profile := runtimecfg.RuntimeProfile{Name: "f", Backend: runtimecfg.BackendFaker}

	bodyA := `{"model":"jev-latest","state":"a","questions":{"q":{"type":"noul","instructions":"?"}}}`
	bodyB := `{"model":"jev-latest","state":"b","questions":{"q":{"type":"noul","instructions":"?"}}}`

	rrA := postSystemOne(t, h, profile, bodyA)
	rrB := postSystemOne(t, h, profile, bodyB)

	if rrA.Body.String() == rrB.Body.String() {
		t.Fatalf("different states produced identical faker answers: %s", rrA.Body.String())
	}
}

func TestSystemOne_FakerBackend_ProducesValidAnswers(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "f", Backend: runtimecfg.BackendFaker}, threeQuestionBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp systemOneResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	choice := resp.Answers["category"]
	if choice.Choice != "electronics" && choice.Choice != "tools" {
		t.Errorf("choice: got %q, want one of the request's options", choice.Choice)
	}
}

func TestSystemOne_ErrorBackend_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		errorType string
		want      int
	}{
		{runtimecfg.ErrorTypeAuthentication, http.StatusUnauthorized},
		{runtimecfg.ErrorTypePermission, http.StatusForbidden},
		{runtimecfg.ErrorTypeInvalidRequest, http.StatusBadRequest},
		{runtimecfg.ErrorTypeRateLimit, http.StatusTooManyRequests},
		{runtimecfg.ErrorTypeServerError, http.StatusInternalServerError},
	} {
		t.Run(tc.errorType, func(t *testing.T) {
			rr := postSystemOne(t, newHandler(t),
				runtimecfg.RuntimeProfile{Name: "e", Backend: runtimecfg.BackendError, ErrorType: tc.errorType},
				threeQuestionBody)
			if rr.Code != tc.want {
				t.Fatalf("status: got %d, want %d. body: %s", rr.Code, tc.want, rr.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if _, ok := payload["error"]; !ok {
				t.Errorf("expected a nested error envelope, got %s", rr.Body.String())
			}
		})
	}
}

// backend=ollama and backend=wasm are accepted by profile validation
// generically (internal/runtime), but this provider has no typesafe-specific
// behavior for either yet (ollama-logprob is zolem-w0i; a typesafe wasm
// backend is out of scope). Listener setup rejects both; a handler invoked
// directly still surfaces a clear 500 rather than silently falling back.
func TestSystemOne_UnsupportedBackends_Return500NotSilentFallback(t *testing.T) {
	for _, backend := range []string{runtimecfg.BackendOllama, runtimecfg.BackendWASM} {
		t.Run(backend, func(t *testing.T) {
			rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "b", Backend: backend}, threeQuestionBody)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status: got %d, want 500. body: %s", rr.Code, rr.Body.String())
			}
			if !bytes.Contains(rr.Body.Bytes(), []byte("not supported for the typesafe provider yet")) {
				t.Errorf("expected an explicit not-supported message, got %s", rr.Body.String())
			}
		})
	}
}

// assertValidationDetail checks the FastAPI-style 422 body: a non-empty
// "detail" array whose entries each carry a "loc" array and a "msg" string.
func assertValidationDetail(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422. body: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Detail []struct {
			Loc []any  `json:"loc"`
			Msg string `json:"msg"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(payload.Detail) == 0 {
		t.Fatalf("expected a non-empty detail array, got %s", rr.Body.String())
	}
	for i, d := range payload.Detail {
		if len(d.Loc) == 0 || d.Msg == "" {
			t.Errorf("detail[%d] needs a loc array and msg string, got %+v", i, d)
		}
	}
}

func TestSystemOne_InvalidJSON(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem}, `not json`)
	assertValidationDetail(t, rr)
}

func TestSystemOne_EmptyBody(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem}, ``)
	assertValidationDetail(t, rr)
}

func TestSystemOne_MissingModel(t *testing.T) {
	rr := postSystemOne(t, newHandler(t), runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem},
		`{"state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`)
	assertValidationDetail(t, rr)
}

func TestSystemOne_SchemaViolation(t *testing.T) {
	rr := postSystemOne(t, newSchemaHandler(t), runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem},
		`{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"?","criteria":{"only":"one"}}}}`)
	assertValidationDetail(t, rr)
}

func TestModels_ListsJevLatest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Models []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ReleaseDate string `json:"release_date"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if len(payload.Models) == 0 || payload.Models[0].Name != "jev-latest" || payload.Models[0].Description == "" || payload.Models[0].ReleaseDate == "" {
		t.Fatalf("models response does not match the TypeSafe SDK model card: %s", rr.Body.String())
	}
}

func TestModels_RequiresAuthorizationHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401. body: %s", rr.Code, rr.Body.String())
	}
}

func TestSystemOne_NotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	newHandler(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404. body: %s", rr.Code, rr.Body.String())
	}
}
