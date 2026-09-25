package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirstTokenLogprobs_RequestShapeAndParse(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"response":"1","done":true,"logprobs":[{"token":"1","logprob":-0.1,"top_logprobs":[{"token":"1","logprob":-0.1},{"token":"2","logprob":-2.5}]}]}`))
	}))
	defer srv.Close()

	cands, err := FirstTokenLogprobs(context.Background(), srv.URL, "llama3.2", "pick one")
	if err != nil {
		t.Fatalf("FirstTokenLogprobs: %v", err)
	}
	if len(cands) != 2 || cands[0].Token != "1" || cands[0].Logprob != -0.1 || cands[1].Token != "2" {
		t.Fatalf("unexpected candidates: %+v", cands)
	}

	if got["model"] != "llama3.2" || got["prompt"] != "pick one" || got["stream"] != false || got["logprobs"] != true || got["top_logprobs"] != float64(20) {
		t.Errorf("unexpected request fields: %+v", got)
	}
	opts, _ := got["options"].(map[string]any)
	if opts["num_predict"] != float64(1) || opts["temperature"] != float64(0) {
		t.Errorf("expected num_predict=1 and temperature=0, got %+v", opts)
	}
}

func TestFirstTokenLogprobs_FallsBackToChosenToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"response":"A","done":true,"logprobs":[{"token":"A","logprob":-0.3}]}`))
	}))
	defer srv.Close()

	cands, err := FirstTokenLogprobs(context.Background(), srv.URL, "m", "p")
	if err != nil || len(cands) != 1 || cands[0].Token != "A" {
		t.Fatalf("expected the chosen token as the sole candidate, got %+v, %v", cands, err)
	}
}

func TestFirstTokenLogprobs_MissingLogprobs(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"response":"1","done":true}`,
		"empty":  `{"response":"1","done":true,"logprobs":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			_, err := FirstTokenLogprobs(context.Background(), srv.URL, "m", "p")
			if !errors.Is(err, ErrNoLogprobs) {
				t.Fatalf("expected ErrNoLogprobs, got %v", err)
			}
		})
	}
}

func TestFirstTokenLogprobs_UpstreamFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FirstTokenLogprobs(context.Background(), srv.URL, "m", "p")
	if err == nil || errors.Is(err, ErrNoLogprobs) {
		t.Fatalf("expected a non-ErrNoLogprobs upstream error, got %v", err)
	}

	srv.Close()
	if _, err := FirstTokenLogprobs(context.Background(), srv.URL, "m", "p"); err == nil {
		t.Fatal("expected an error for an unreachable upstream")
	}
}
