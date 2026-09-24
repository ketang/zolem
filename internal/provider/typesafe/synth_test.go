package typesafe

import (
	"encoding/json"
	"testing"
)

func TestAnswerLorem_Noul(t *testing.T) {
	a, err := answerLorem(Question{Type: QuestionNoul})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Noul == nil || *a.Noul != 0.5 {
		t.Fatalf("got %+v, want noul=0.5", a)
	}
}

func TestAnswerLorem_ChoiceIsValidAgainstValidator(t *testing.T) {
	q := Question{Type: QuestionChoice, Criteria: json.RawMessage(`{"a":"A","b":"B","c":"C"}`)}
	a, err := answerLorem(q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := Request{Questions: map[string]Question{"q": q}}
	if err := ValidateAnswers(req, map[string]Answer{"q": a}); err != nil {
		t.Fatalf("lorem choice answer failed validation: %v", err)
	}
	if a.Choice != "a" {
		t.Errorf("lorem choice: got %q, want first option %q", a.Choice, "a")
	}
}

func TestAnswerLorem_ScoreIsValidAgainstValidator(t *testing.T) {
	q := Question{Type: QuestionScore, Criteria: json.RawMessage(`["low","mid","high"]`)}
	a, err := answerLorem(q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := Request{Questions: map[string]Question{"q": q}}
	if err := ValidateAnswers(req, map[string]Answer{"q": a}); err != nil {
		t.Fatalf("lorem score answer failed validation: %v", err)
	}
	if a.Score == nil {
		t.Fatal("lorem score: score field is nil")
	}
}

func TestAnswerFaker_ChoiceIsValidAgainstValidator(t *testing.T) {
	q := Question{Type: QuestionChoice, Criteria: json.RawMessage(`{"a":"A","b":"B","c":"C"}`)}
	a, err := answerFaker(q, "q", 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := Request{Questions: map[string]Question{"q": q}}
	if err := ValidateAnswers(req, map[string]Answer{"q": a}); err != nil {
		t.Fatalf("faker choice answer failed validation: %v", err)
	}
}

func TestAnswerFaker_ScoreIsValidAgainstValidator(t *testing.T) {
	q := Question{Type: QuestionScore, Criteria: json.RawMessage(`["low","mid","high","top"]`)}
	a, err := answerFaker(q, "q", 987654321)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := Request{Questions: map[string]Question{"q": q}}
	if err := ValidateAnswers(req, map[string]Answer{"q": a}); err != nil {
		t.Fatalf("faker score answer failed validation: %v", err)
	}
}

func TestAnswerFaker_SameSeedSameAnswer(t *testing.T) {
	q := Question{Type: QuestionChoice, Criteria: json.RawMessage(`{"a":"A","b":"B","c":"C"}`)}
	a1, _ := answerFaker(q, "q", 42)
	a2, _ := answerFaker(q, "q", 42)
	if a1.Choice != a2.Choice || a1.Probabilities["a"] != a2.Probabilities["a"] {
		t.Fatalf("same seed produced different answers: %+v vs %+v", a1, a2)
	}
}

func TestAnswerFaker_DifferentSeedDifferentAnswer(t *testing.T) {
	q := Question{Type: QuestionChoice, Criteria: json.RawMessage(`{"a":"A","b":"B","c":"C"}`)}
	a1, _ := answerFaker(q, "q", 1)
	a2, _ := answerFaker(q, "q", 2)
	if a1.Probabilities["a"] == a2.Probabilities["a"] {
		t.Fatalf("different seeds produced identical probabilities: %v", a1.Probabilities["a"])
	}
}

func TestRequestSeed_Deterministic(t *testing.T) {
	body := []byte(`{"model":"jev-latest","state":"x"}`)
	if requestSeed(body) != requestSeed(body) {
		t.Fatal("requestSeed is not deterministic for identical input")
	}
	if requestSeed(body) == requestSeed([]byte(`{"model":"jev-latest","state":"y"}`)) {
		t.Fatal("requestSeed collided for different input")
	}
}
