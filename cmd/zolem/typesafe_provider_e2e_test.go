package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// systemOneEnvelope mirrors the subset of a POST /v1/systemone response the
// E2E assertions inspect.
type systemOneEnvelope struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Type          string             `json:"type"`
		Noul          *float64           `json:"noul"`
		Choice        string             `json:"choice"`
		Score         *float64           `json:"score"`
		Legend        map[string]string  `json:"legend"`
		Probabilities map[string]float64 `json:"probabilities"`
	} `json:"answers"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

const threeQuestionSystemOneBody = `{
  "model": "jev-latest",
  "state": {"item": "vintage lamp", "condition": "used"},
  "questions": {
    "is_fragile": {"type": "noul", "instructions": "Is this item fragile?"},
    "category": {"type": "choice", "instructions": "Pick a category", "criteria": {"electronics": "electronic devices", "furniture": "furniture and decor"}},
    "quality": {"type": "score", "instructions": "Rate the condition", "criteria": ["poor", "fair", "good", "excellent"]}
  }
}`

// TestLocalRuntimeTypesafeProvider_E2E covers the acceptance checks from the
// zolem-jwr issue over real HTTP: a fixed listener answering POST
// /v1/systemone and GET /v1/models for the lorem and faker backends, request
// schema validation, the Authorization-header presence check, and a fixture
// whose templated body renders and passes answer-contract validation.
func TestLocalRuntimeTypesafeProvider_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	listenerBaseURL := createRuntimeListener(t, admin, "typesafe", map[string]any{
		"backend": "lorem",
	})

	t.Run("missing-authorization-is-401", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/systemone",
			threeQuestionSystemOneBody, "Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status: got %d, want 401: %s", resp.StatusCode, body)
		}
	})

	t.Run("lorem-backend-answers-all-three-question-types", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/systemone",
			threeQuestionSystemOneBody,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("content-type: got %q, want application/json", ct)
		}

		var env systemOneEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if len(env.Answers) != 3 {
			t.Fatalf("answers: got %d, want 3: %s", len(env.Answers), body)
		}

		noul := env.Answers["is_fragile"]
		if noul.Type != "noul" || noul.Noul == nil {
			t.Errorf("noul answer missing: %+v", noul)
		}
		choice := env.Answers["category"]
		if choice.Type != "choice" || (choice.Choice != "electronics" && choice.Choice != "furniture") {
			t.Errorf("choice answer invalid: %+v", choice)
		}
		score := env.Answers["quality"]
		if score.Type != "score" || score.Score == nil {
			t.Errorf("score answer missing: %+v", score)
		}
		if env.Usage.InputTokens <= 0 || env.Usage.OutputTokens <= 0 {
			t.Errorf("usage: got %+v, want positive counts", env.Usage)
		}
	})

	t.Run("models-lists-jev-latest", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodGet, "/v1/models",
			"", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "jev-latest") {
			t.Errorf("expected jev-latest in models list: %s", body)
		}
		var models struct {
			Models []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				ReleaseDate string `json:"release_date"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &models); err != nil || len(models.Models) == 0 || models.Models[0].Name != "jev-latest" || models.Models[0].Description == "" || models.Models[0].ReleaseDate == "" {
			t.Fatalf("models do not match TypeSafe model-card shape: %s (decode error: %v)", body, err)
		}
	})

	t.Run("schema-rejects-unknown-question-type", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/systemone",
			`{"model":"jev-latest","state":"x","questions":{"q":{"type":"maybe","instructions":"?"}}}`,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400: %s", resp.StatusCode, body)
		}
	})

	t.Run("schema-rejects-empty-questions", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/systemone",
			`{"model":"jev-latest","state":"x","questions":{}}`,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400: %s", resp.StatusCode, body)
		}
	})

	t.Run("schema-rejects-score-with-one-level", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/systemone",
			`{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"?","criteria":["only"]}}}`,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400: %s", resp.StatusCode, body)
		}
	})

	// Same request twice must yield identical answers, and a different state
	// must yield a different answer, per the issue's faker acceptance check.
	t.Run("faker-backend-is-seeded-by-request", func(t *testing.T) {
		fakerURL := createRuntimeListener(t, admin, "typesafe", map[string]any{
			"backend": "faker",
		})

		resp1, body1 := doRequest(t, fakerURL, http.MethodPost, "/v1/systemone",
			threeQuestionSystemOneBody, "Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		resp1.Body.Close()
		resp2, body2 := doRequest(t, fakerURL, http.MethodPost, "/v1/systemone",
			threeQuestionSystemOneBody, "Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		resp2.Body.Close()
		if string(body1) != string(body2) {
			t.Fatalf("identical requests produced different faker answers:\n%s\nvs\n%s", body1, body2)
		}

		differentState := `{"model":"jev-latest","state":"a totally different state","questions":{"is_fragile":{"type":"noul","instructions":"?"}}}`
		resp3, body3 := doRequest(t, fakerURL, http.MethodPost, "/v1/systemone",
			differentState, "Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		resp3.Body.Close()
		sameQuestionDifferentState := `{"model":"jev-latest","state":"a completely other state","questions":{"is_fragile":{"type":"noul","instructions":"?"}}}`
		resp4, body4 := doRequest(t, fakerURL, http.MethodPost, "/v1/systemone",
			sameQuestionDifferentState, "Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		resp4.Body.Close()
		if string(body3) == string(body4) {
			t.Fatalf("different states produced identical faker answers: %s", body3)
		}
	})

	// Fixture bodies must go through template rendering, exactly as they do
	// for the other four providers.
	t.Run("templated-fixture-is-rendered", func(t *testing.T) {
		fixturesDir := t.TempDir()
		copyTestdataFixtures(t, repoRoot, fixturesDir)
		fixtureAdmin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(fixtureAdmin.Close)

		fixtureURL := createRuntimeListener(t, fixtureAdmin, "typesafe", map[string]any{
			"backend": "fixture",
		})
		want := "Templated fixture for profile typesafe-fixture-demo and state x."

		resp, body := doRequest(t, fixtureURL, http.MethodPost, "/v1/systemone",
			`{"model":"jev-latest","state":"x","questions":{"urgency":{"type":"score","instructions":"?","criteria":["low","high"]}}}`,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		var env systemOneEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if env.Answers["urgency"].Legend["1"] != want {
			t.Fatalf("rendered legend: got %q, want %q (body: %s)", env.Answers["urgency"].Legend["1"], want, body)
		}
	})

	t.Run("invalid-fixture-choice-is-500", func(t *testing.T) {
		fixturesDir := t.TempDir()
		mustMkdir(t, filepath.Join(fixturesDir, "invalid-choice"))
		for name, body := range map[string]string{
			"fixtures.yaml": `provider: typesafe
version: v1
fixtures:
  - expression: 'true'
    fixture: invalid-choice
`,
			"invalid-choice/meta.yaml": `id: invalid-choice
provider: typesafe
version: v1
status: 200
`,
			"invalid-choice/response.json": `{"model":"jev-latest","answers":{"category":{"type":"choice","choice":"absent","probabilities":{"electronics":0.6,"furniture":0.4},"confidence":0.6}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		} {
			if err := os.WriteFile(filepath.Join(fixturesDir, name), []byte(body), 0o644); err != nil {
				t.Fatalf("write fixture %s: %v", name, err)
			}
		}
		fixtureAdmin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(fixtureAdmin.Close)
		fixtureURL := createRuntimeListener(t, fixtureAdmin, "typesafe", map[string]any{"backend": "fixture"})
		requestBody := `{"model":"jev-latest","state":"x","questions":{"category":{"type":"choice","instructions":"pick","criteria":{"electronics":"E","furniture":"F"}}}}`
		resp, body := doRequest(t, fixtureURL, http.MethodPost, "/v1/systemone", requestBody,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "category") || !strings.Contains(string(body), "absent") {
			t.Fatalf("invalid choice response: status=%d body=%s", resp.StatusCode, body)
		}
	})
}
