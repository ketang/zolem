package main_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubOllamaGenerate is a fake Ollama /api/generate. The first-token
// alternatives are "1" (-0.1) and "2" (-2.5), or none when withLogprobs is
// false, as older Ollama releases behave.
func stubOllamaGenerate(t *testing.T, withLogprobs bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		if !withLogprobs {
			_, _ = w.Write([]byte(`{"response":"1","done":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"response":"1","done":true,"logprobs":[{"token":"1","logprob":-0.1,"top_logprobs":[{"token":"1","logprob":-0.1},{"token":"2","logprob":-2.5}]}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func putAdmin(t *testing.T, admin *localAdminService, path, body string) (int, string) {
	t.Helper()
	resp, out := doRequest(t, admin.baseURL, http.MethodPut, path, body, "Content-Type: application/json")
	resp.Body.Close()
	return resp.StatusCode, string(out)
}

// newTypesafeListener creates a profile and a typesafe listener and returns
// the listener's base URL.
func newTypesafeListener(t *testing.T, admin *localAdminService, name string, profileJSON string) string {
	t.Helper()
	if status, body := putAdmin(t, admin, "/_zolem/profiles/"+name, profileJSON); status != http.StatusOK {
		t.Fatalf("profile %s: status=%d body=%s", name, status, body)
	}
	resp, body := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+name,
		fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"typesafe","profile":"%s"}`, name), "Content-Type: application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listener %s: status=%d body=%s", name, resp.StatusCode, body)
	}
	t.Cleanup(func() {
		r, _ := doRequest(t, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+name, "")
		r.Body.Close()
	})
	var view struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(body, &view); err != nil || view.BaseURL == "" {
		t.Fatalf("listener %s: no base_url in %s (%v)", name, body, err)
	}
	return view.BaseURL
}

func postTwoOptionChoice(t *testing.T, baseURL string) (int, []byte) {
	t.Helper()
	resp, body := doRequest(t, baseURL, http.MethodPost, "/v1/systemone",
		`{"model":"jev-latest","state":"refund please","questions":{"dept":{"type":"choice","instructions":"Which department?","criteria":{"billing":"payments","sales":"purchases"}}}}`,
		"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
	defer resp.Body.Close()
	return resp.StatusCode, body
}

// TestLocalRuntimeTypesafeOllamaLogprob_E2E drives the ollama-logprob backend
// through the admin API and a real typesafe listener against a stub Ollama
// upstream (no live Ollama), covering answers, calibration, profile and
// listener validation, and upstream failure.
func TestLocalRuntimeTypesafeOllamaLogprob_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	upstream := stubOllamaGenerate(t, true)
	profile := func(extra string) string {
		return fmt.Sprintf(`{"backend":"ollama-logprob","backend_model":"llama3.2","ollama_upstream":%q%s}`, upstream.URL, extra)
	}

	t.Run("answers-from-logprobs", func(t *testing.T) {
		baseURL := newTypesafeListener(t, admin, "lp-default", profile(""))
		status, body := postTwoOptionChoice(t, baseURL)
		if status != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", status, body)
		}
		var env systemOneEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		a := env.Answers["dept"]
		if a.Choice != "billing" || math.Abs(a.Probabilities["billing"]-0.917) > 1e-3 || math.Abs(a.Probabilities["sales"]-0.083) > 1e-3 {
			t.Fatalf("expected billing at about 0.917/0.083, got %+v", a)
		}
	})

	t.Run("all-three-question-types", func(t *testing.T) {
		baseURL := newTypesafeListener(t, admin, "lp-three", profile(""))
		resp, body := doRequest(t, baseURL, http.MethodPost, "/v1/systemone", threeQuestionSystemOneBody,
			"Content-Type: application/json", "Authorization: Bearer sk-unchecked-key")
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
	})

	t.Run("calibration-temperature-flattens", func(t *testing.T) {
		baseURL := newTypesafeListener(t, admin, "lp-temp", profile(`,"calibration_temperature":2.0`))
		status, body := postTwoOptionChoice(t, baseURL)
		if status != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", status, body)
		}
		var env systemOneEnvelope
		_ = json.Unmarshal(body, &env)
		if p := env.Answers["dept"].Probabilities["billing"]; math.Abs(p-0.7685) > 1e-3 {
			t.Fatalf("billing probability at temperature 2.0: got %v, want about 0.7685", p)
		}
	})

	t.Run("missing-logprobs-is-502", func(t *testing.T) {
		old := stubOllamaGenerate(t, false)
		baseURL := newTypesafeListener(t, admin, "lp-old",
			fmt.Sprintf(`{"backend":"ollama-logprob","backend_model":"llama3.2","ollama_upstream":%q}`, old.URL))
		status, body := postTwoOptionChoice(t, baseURL)
		if status != http.StatusBadGateway || !strings.Contains(string(body), "did not return logprobs") {
			t.Fatalf("status=%d body=%s, want 502 saying the upstream did not return logprobs", status, body)
		}
	})

	t.Run("profile-validation", func(t *testing.T) {
		for name, tc := range map[string]struct{ body, want string }{
			"missing-model":        {`{"backend":"ollama-logprob"}`, "backend_model"},
			"zero-temperature":     {profile(`,"calibration_temperature":0`), "calibration_temperature"},
			"negative-temperature": {profile(`,"calibration_temperature":-1`), "calibration_temperature"},
			"external-upstream":    {`{"backend":"ollama-logprob","backend_model":"m","ollama_upstream":"http://evil.example:11434"}`, "ollama_upstream"},
		} {
			status, body := putAdmin(t, admin, "/_zolem/profiles/lp-bad-"+name, tc.body)
			if status != http.StatusBadRequest || !strings.Contains(body, tc.want) {
				t.Errorf("%s: status=%d body=%s, want 400 naming %s", name, status, body, tc.want)
			}
		}
	})

	t.Run("rejected-for-other-providers", func(t *testing.T) {
		if status, body := putAdmin(t, admin, "/_zolem/profiles/lp-other", profile("")); status != http.StatusOK {
			t.Fatalf("profile: status=%d body=%s", status, body)
		}
		status, body := putAdmin(t, admin, "/_zolem/listeners/lp-openai",
			`{"addr":"127.0.0.1:0","provider":"openai","profile":"lp-other"}`)
		if status != http.StatusBadRequest || !strings.Contains(body, "only supported for the typesafe provider") {
			t.Fatalf("status=%d body=%s, want 400 saying ollama-logprob is typesafe-only", status, body)
		}
	})
}
