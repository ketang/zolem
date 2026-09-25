package typesafe_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

type stubToken struct {
	Token   string
	Logprob float64
}

// stubUpstream is a fake Ollama /api/generate. reply maps the received prompt
// to the first-token alternatives it should return; a nil result omits the
// logprobs field entirely, as older Ollama releases do.
type stubUpstream struct {
	*httptest.Server
	mu      sync.Mutex
	prompts []string
	models  []string
}

func newStubUpstream(t *testing.T, reply func(prompt string) []stubToken) *stubUpstream {
	t.Helper()
	s := &stubUpstream{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.prompts = append(s.prompts, req.Prompt)
		s.models = append(s.models, req.Model)
		s.mu.Unlock()

		tokens := reply(req.Prompt)
		if tokens == nil {
			_, _ = w.Write([]byte(`{"response":"1","done":true}`))
			return
		}
		top := make([]map[string]any, 0, len(tokens))
		for _, tk := range tokens {
			top = append(top, map[string]any{"token": tk.Token, "logprob": tk.Logprob})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": tokens[0].Token, "done": true,
			"logprobs": []map[string]any{{"token": tokens[0].Token, "logprob": tokens[0].Logprob, "top_logprobs": top}},
		})
	}))
	t.Cleanup(s.Close)
	return s
}

func logprobProfile(upstream string, temperature *float64) runtimecfg.RuntimeProfile {
	return runtimecfg.RuntimeProfile{
		Name:                   "lp",
		Backend:                runtimecfg.BackendOllamaLogprob,
		BackendModel:           "llama3.2",
		OllamaUpstream:         upstream,
		CalibrationTemperature: temperature,
	}
}

const twoOptionChoice = `{"model":"jev-latest","state":{"ticket":"refund please"},"questions":{"dept":{"type":"choice","instructions":"Which department?","criteria":{"billing":"payments and refunds","sales":"new purchases"}}}}`

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) systemOneResponse {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp systemOneResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func TestOllamaLogprob_ChoiceSoftmaxOverLogprobs(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken {
		return []stubToken{{"1", -0.1}, {"2", -2.5}}
	})
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), twoOptionChoice))

	a := resp.Answers["dept"]
	if a.Choice != "billing" {
		t.Errorf("choice: got %q, want billing", a.Choice)
	}
	if !near(a.Probabilities["billing"], 0.917) || !near(a.Probabilities["sales"], 0.083) {
		t.Errorf("probabilities: got %+v, want about 0.917/0.083", a.Probabilities)
	}
	if a.Confidence == nil || !near(*a.Confidence, 0.917) {
		t.Errorf("confidence: got %v, want the winning probability", a.Confidence)
	}
	if len(up.models) != 1 || up.models[0] != "llama3.2" {
		t.Errorf("upstream model: got %v, want backend_model", up.models)
	}
}

func TestOllamaLogprob_CalibrationTemperatureFlattens(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return []stubToken{{"1", -0.1}, {"2", -2.5}} })
	two := 2.0
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, &two), twoOptionChoice))

	p := resp.Answers["dept"].Probabilities["billing"]
	if p >= 0.917 || p <= 0.5 {
		t.Errorf("temperature 2.0 should flatten toward 0.5 (got %v), staying above it", p)
	}
	// softmax([-0.05, -1.25]) = 0.7685
	if !near(p, 0.7685) {
		t.Errorf("billing probability: got %v, want about 0.7685", p)
	}
}

func TestOllamaLogprob_PromptCarriesInstructionsStateAndOptions(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return []stubToken{{"1", -0.1}, {"2", -2.5}} })
	decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), twoOptionChoice))

	prompt := up.prompts[0]
	for _, want := range []string{"Which department?", "refund please", "1. billing", "payments and refunds", "2. sales", "new purchases"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestOllamaLogprob_MissingLogprobsIs502(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return nil })
	rr := postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), twoOptionChoice)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502. body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "did not return logprobs") {
		t.Errorf("expected a message saying the upstream did not return logprobs, got %s", rr.Body.String())
	}
}

func TestOllamaLogprob_UnreachableUpstreamIs502(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return nil })
	up.Close()
	rr := postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), twoOptionChoice)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502. body: %s", rr.Code, rr.Body.String())
	}
}

func TestOllamaLogprob_NoParseableLabelIsUniform(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return []stubToken{{"Sure", -0.1}, {"The", -1.0}} })
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), twoOptionChoice))

	a := resp.Answers["dept"]
	if !near(a.Probabilities["billing"], 0.5) || !near(a.Probabilities["sales"], 0.5) {
		t.Errorf("expected a uniform distribution, got %+v", a.Probabilities)
	}
	if a.Choice != "billing" {
		t.Errorf("uniform ties should pick the first option, got %q", a.Choice)
	}
}

func TestOllamaLogprob_TokenNormalization(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken {
		return []stubToken{{" 1", -1.0}, {"1", -1.0}, {"2.", -1.0}, {"\n2", -3.0}}
	})
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), twoOptionChoice))

	// option 1: two tokens at -1.0 (weight 2e^-1); option 2: e^-1 + e^-3.
	want := 2 * math.Exp(-1) / (2*math.Exp(-1) + math.Exp(-1) + math.Exp(-3))
	if got := resp.Answers["dept"].Probabilities["billing"]; !near(got, want) {
		t.Errorf("billing: got %v, want %v (whitespace/period trimmed, duplicates summed)", got, want)
	}
}

func TestOllamaLogprob_NoulRenormalizesYes(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return []stubToken{{"1", -0.2}, {"2", -1.8}} })
	body := `{"model":"jev-latest","state":"a glass vase","questions":{"fragile":{"type":"noul","instructions":"Is it fragile?"}}}`
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), body))

	want := math.Exp(-0.2) / (math.Exp(-0.2) + math.Exp(-1.8))
	if got := resp.Answers["fragile"].Noul; got == nil || !near(*got, want) {
		t.Errorf("noul: got %v, want %v", got, want)
	}
	if !strings.Contains(up.prompts[0], "1. yes") || !strings.Contains(up.prompts[0], "2. no") {
		t.Errorf("noul prompt should offer yes/no as labels 1/2:\n%s", up.prompts[0])
	}
}

func TestOllamaLogprob_ScoreIsProbabilityWeightedPosition(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return []stubToken{{"1", -0.7}, {"2", -0.7}, {"3", -50}} })
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","instructions":"Rate","criteria":["poor","average","excellent"]}}}`
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), body))

	a := resp.Answers["q"]
	if a.Score == nil || !near(*a.Score, 0.5) {
		t.Errorf("score: got %v, want 0.5 (even split over levels 0 and 1)", a.Score)
	}
	if len(a.Probabilities) != 3 || a.Legend["2"] != "excellent" {
		t.Errorf("expected all 3 level keys and a legend, got %+v / %+v", a.Probabilities, a.Legend)
	}
}

// Ollama caps top_logprobs at 20, so options beyond the top 20 labels get
// zero mass. The answer must still cover all 25 option keys and validate.
func TestOllamaLogprob_MoreOptionsThanTopLogprobs(t *testing.T) {
	var criteria []string
	for i := 0; i < 25; i++ {
		criteria = append(criteria, fmt.Sprintf(`"opt%02d":"option %d"`, i, i))
	}
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"Pick","criteria":{` + strings.Join(criteria, ",") + `}}}}`

	// 25 options exceed the 9 single-digit labels, so letters A-Y are used.
	up := newStubUpstream(t, func(string) []stubToken {
		var out []stubToken
		for i := 0; i < 20; i++ {
			out = append(out, stubToken{string(rune('A' + i)), -1.0 - float64(i)*0.1})
		}
		return out
	})
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), body))

	a := resp.Answers["q"]
	if len(a.Probabilities) != 25 {
		t.Fatalf("expected 25 option keys, got %d", len(a.Probabilities))
	}
	var sum float64
	zeros := 0
	for _, p := range a.Probabilities {
		sum += p
		if p == 0 {
			zeros++
		}
	}
	if !near(sum, 1) || zeros != 5 {
		t.Errorf("sum=%v zeros=%d, want sum 1 and 5 zero-mass options", sum, zeros)
	}
	if a.Choice != "opt00" {
		t.Errorf("choice: got %q, want opt00 (label A)", a.Choice)
	}
	if !strings.Contains(up.prompts[0], "A. opt00") {
		t.Errorf("more than 9 options should use letter labels:\n%s", up.prompts[0])
	}
}

func TestOllamaLogprob_MultipleQuestionsAnsweredIndependently(t *testing.T) {
	up := newStubUpstream(t, func(string) []stubToken { return []stubToken{{"1", -0.5}, {"2", -1.5}, {"3", -2.5}} })
	resp := decodeResponse(t, postSystemOne(t, newHandler(t), logprobProfile(up.URL, nil), threeQuestionBody))

	if len(resp.Answers) != 3 || len(up.prompts) != 3 {
		t.Fatalf("expected 3 answers from 3 upstream calls, got %d answers / %d calls", len(resp.Answers), len(up.prompts))
	}
}
