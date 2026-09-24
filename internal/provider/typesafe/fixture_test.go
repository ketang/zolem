package typesafe_test

import (
	"bytes"
	"context"
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

// firstMatchSelector picks the first candidate. The default LegacySelector
// only ever selects fixtures carrying a CEL or WASM matcher, which is not
// what these tests are exercising.
type firstMatchSelector struct{}

func (firstMatchSelector) Select(_ context.Context, _ fixture.MatchRequest, candidates []fixture.Fixture) (*fixture.Fixture, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	return &candidates[0], nil
}

func systemOneWithFixture(t *testing.T, f fixture.Fixture, body string) *httptest.ResponseRecorder {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, []fixture.Fixture{f}, firstMatchSelector{})
	h := typesafe.NewHandler(specs.NewValidator(), matcher, response.NewLoremGenerator())

	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rt := runtimecfg.ListenerRuntime{Profile: runtimecfg.RuntimeProfile{Name: "fx", Backend: runtimecfg.BackendFixture}}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

const oneNoulQuestionBody = `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`

func TestFixture_ValidAnswerIsServed(t *testing.T) {
	f := fixture.Fixture{
		ID:           "valid",
		Provider:     "typesafe",
		Version:      "v1",
		ResponseBody: []byte(`{"model":"jev-latest","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":1,"output_tokens":1}}`),
		Status:       http.StatusOK,
	}
	rr := systemOneWithFixture(t, f, oneNoulQuestionBody)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestFixture_MalformedResponseIs500(t *testing.T) {
	f := fixture.Fixture{ID: "malformed", Provider: "typesafe", Version: "v1", ResponseBody: []byte(`not json`), Status: http.StatusOK}
	rr := systemOneWithFixture(t, f, oneNoulQuestionBody)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500. body: %s", rr.Code, rr.Body.String())
	}
}

func TestFixture_TemplateCanReadParsedRequest(t *testing.T) {
	f := fixture.Fixture{ID: "request-aware", Provider: "typesafe", Version: "v1", Status: http.StatusOK}
	if err := f.SetResponseTemplate([]byte(`{"model":"jev-latest","answers":{"q":{"type":"noul","noul":{{ if eq .Request.state "fragile" }}0.9{{ else }}0.1{{ end }}}},"usage":{"input_tokens":1,"output_tokens":1}}`)); err != nil {
		t.Fatal(err)
	}
	rr := systemOneWithFixture(t, f, `{"model":"jev-latest","state":"fragile","questions":{"q":{"type":"noul","instructions":"?"}}}`)
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte(`"noul":0.9`)) {
		t.Fatalf("template did not use request state: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// A fixture whose answer violates the response contract (here: a choice
// naming an option absent from the request) is a 500 naming the question key,
// not a silently wrong answer.
func TestFixture_InvalidAnswerIs500WithQuestionKey(t *testing.T) {
	f := fixture.Fixture{
		ID:           "invalid-choice",
		Provider:     "typesafe",
		Version:      "v1",
		ResponseBody: []byte(`{"model":"jev-latest","answers":{"category":{"type":"choice","choice":"nonexistent","probabilities":{"electronics":0.5,"tools":0.5}}},"usage":{"input_tokens":1,"output_tokens":1}}`),
		Status:       http.StatusOK,
	}
	body := `{"model":"jev-latest","state":"x","questions":{"category":{"type":"choice","instructions":"?","criteria":{"electronics":"E","tools":"T"}}}}`
	rr := systemOneWithFixture(t, f, body)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500. body: %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("category")) {
		t.Errorf("expected error body to name the question key %q: %s", "category", rr.Body.String())
	}
}

func TestFixture_ResponseModelIsOverridden(t *testing.T) {
	f := fixture.Fixture{
		ID:           "model-override",
		Provider:     "typesafe",
		Version:      "v1",
		ResponseBody: []byte(`{"model":"whatever-the-fixture-says","answers":{"q":{"type":"noul","noul":0.1}},"usage":{"input_tokens":1,"output_tokens":1}}`),
		Status:       http.StatusOK,
	}
	rr := systemOneWithFixture(t, f, oneNoulQuestionBody)
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Model != "jev-latest" {
		t.Errorf("model: got %q, want echoed request model %q", payload.Model, "jev-latest")
	}
}
