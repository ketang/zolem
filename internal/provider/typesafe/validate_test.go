package typesafe

import (
	"encoding/json"
	"strings"
	"testing"
)

func reqWithChoice(criteria string) Request {
	return Request{
		Questions: map[string]Question{
			"q": {Type: QuestionChoice, Instructions: json.RawMessage(`"pick"`), Criteria: json.RawMessage(criteria)},
		},
	}
}

func reqWithScore(criteria string) Request {
	return Request{
		Questions: map[string]Question{
			"q": {Type: QuestionScore, Instructions: json.RawMessage(`"rate"`), Criteria: json.RawMessage(criteria)},
		},
	}
}

func reqWithNoul() Request {
	return Request{
		Questions: map[string]Question{
			"q": {Type: QuestionNoul, Instructions: json.RawMessage(`"is it?"`)},
		},
	}
}

func TestValidateAnswers_ValidNoul(t *testing.T) {
	err := ValidateAnswers(reqWithNoul(), map[string]Answer{
		"q": {Type: QuestionNoul, Noul: floatPtr(0.7)},
	})
	if err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateAnswers_NoulOutOfRange(t *testing.T) {
	err := ValidateAnswers(reqWithNoul(), map[string]Answer{
		"q": {Type: QuestionNoul, Noul: floatPtr(1.5)},
	})
	assertValidationErrorNames(t, err, "q")
}

func TestValidateAnswers_MissingAnswer(t *testing.T) {
	err := ValidateAnswers(reqWithNoul(), map[string]Answer{})
	assertValidationErrorNames(t, err, "q")
}

func TestValidateAnswers_ExtraAnswer(t *testing.T) {
	req := reqWithNoul()
	err := ValidateAnswers(req, map[string]Answer{
		"q":     {Type: QuestionNoul, Noul: floatPtr(0.5)},
		"extra": {Type: QuestionNoul, Noul: floatPtr(0.5)},
	})
	assertValidationErrorNames(t, err, "extra")
}

func TestValidateAnswers_ChoiceValid(t *testing.T) {
	req := reqWithChoice(`{"a":"A","b":"B"}`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "a", Probabilities: map[string]float64{"a": 0.6, "b": 0.4}, Confidence: floatPtr(0.6)},
	})
	if err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateAnswers_ChoiceNotAnOption(t *testing.T) {
	req := reqWithChoice(`{"a":"A","b":"B"}`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "c", Probabilities: map[string]float64{"a": 0.6, "b": 0.4}},
	})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "not one of the request's options") {
		t.Errorf("expected 'not one of the request's options' rule, got %v", err)
	}
}

func TestValidateAnswers_ChoiceProbabilitiesDontSumToOne(t *testing.T) {
	req := reqWithChoice(`{"a":"A","b":"B"}`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "a", Probabilities: map[string]float64{"a": 0.6, "b": 0.6}},
	})
	assertValidationErrorNames(t, err, "q")
}

func TestValidateAnswers_RejectsOutOfRangeProbabilities(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		ans  Answer
	}{
		{"choice", reqWithChoice(`{"a":"A","b":"B"}`), Answer{Type: QuestionChoice, Choice: "a", Probabilities: map[string]float64{"a": 1.5, "b": -0.5}}},
		{"score", reqWithScore(`["low","high"]`), Answer{Type: QuestionScore, Score: floatPtr(-0.5), Probabilities: map[string]float64{"0": 1.5, "1": -0.5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAnswers(tc.req, map[string]Answer{"q": tc.ans})
			assertValidationErrorNames(t, err, "q")
			if !strings.Contains(err.Error(), "outside [0, 1]") {
				t.Fatalf("wrong validation rule: %v", err)
			}
		})
	}
}

func TestValidateAnswers_ChoiceRequiresConfidence(t *testing.T) {
	err := ValidateAnswers(reqWithChoice(`{"a":"A","b":"B"}`), map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "a", Probabilities: map[string]float64{"a": 0.6, "b": 0.4}},
	})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("wrong validation rule: %v", err)
	}
}

func TestValidateAnswers_ChoiceNotArgmax(t *testing.T) {
	req := reqWithChoice(`{"a":"A","b":"B"}`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "a", Probabilities: map[string]float64{"a": 0.2, "b": 0.8}},
	})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "argmax") {
		t.Errorf("expected argmax rule, got %v", err)
	}
}

func TestValidateAnswers_ChoiceMissingProbabilityKey(t *testing.T) {
	req := reqWithChoice(`{"a":"A","b":"B"}`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "a", Probabilities: map[string]float64{"a": 1}},
	})
	assertValidationErrorNames(t, err, "q")
}

func TestValidateAnswers_ScoreValid(t *testing.T) {
	req := reqWithScore(`["low","mid","high"]`)
	// weighted position = 0*0.25 + 1*0.25 + 2*0.5 = 1.25.
	err := ValidateAnswers(req, map[string]Answer{
		"q": {
			Type:          QuestionScore,
			Score:         floatPtr(1.25),
			Probabilities: map[string]float64{"0": 0.25, "1": 0.25, "2": 0.5},
			Legend:        map[string]string{"0": "low", "1": "mid", "2": "high"},
			Confidence:    floatPtr(0.5),
		},
	})
	if err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateAnswers_ScoreRequiresLegendAndConfidence(t *testing.T) {
	req := reqWithScore(`["low","high"]`)
	base := Answer{Type: QuestionScore, Score: floatPtr(0.4), Probabilities: map[string]float64{"0": 0.6, "1": 0.4}}
	err := ValidateAnswers(req, map[string]Answer{"q": base})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "legend") {
		t.Fatalf("wrong validation rule: %v", err)
	}
	base.Legend = map[string]string{"0": "low", "1": "high"}
	err = ValidateAnswers(req, map[string]Answer{"q": base})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("wrong validation rule: %v", err)
	}
}

func TestValidateAnswers_ScoreMismatchedWeightedPosition(t *testing.T) {
	req := reqWithScore(`["low","mid","high"]`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {
			Type:          QuestionScore,
			Score:         floatPtr(0),
			Probabilities: map[string]float64{"0": 0.25, "1": 0.25, "2": 0.5},
		},
	})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "probability-weighted position") {
		t.Errorf("expected probability-weighted-position rule, got %v", err)
	}
}

func TestValidateAnswers_ScoreProbabilitiesWrongKeys(t *testing.T) {
	req := reqWithScore(`["low","mid","high"]`)
	err := ValidateAnswers(req, map[string]Answer{
		"q": {
			Type:          QuestionScore,
			Score:         floatPtr(0.5),
			Probabilities: map[string]float64{"0": 0.5, "1": 0.5},
		},
	})
	assertValidationErrorNames(t, err, "q")
}

func TestValidateAnswers_TypeMismatch(t *testing.T) {
	err := ValidateAnswers(reqWithNoul(), map[string]Answer{
		"q": {Type: QuestionChoice, Choice: "a"},
	})
	assertValidationErrorNames(t, err, "q")
	if !strings.Contains(err.Error(), "does not match question type") {
		t.Errorf("expected type-mismatch rule, got %v", err)
	}
}

func assertValidationErrorNames(t *testing.T, err error, key string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	ve, ok := err.(*AnswerValidationError)
	if !ok {
		t.Fatalf("expected *AnswerValidationError, got %T: %v", err, err)
	}
	if ve.Key != key {
		t.Fatalf("error key: got %q, want %q (message: %v)", ve.Key, key, err)
	}
}
