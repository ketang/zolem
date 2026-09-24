package typesafe

import "encoding/json"

// Question type discriminators, confirmed from docs.typesafe.ai (api.md,
// primitives/{choice,noul,score}.md, fetched 2026-09-21).
const (
	QuestionNoul   = "noul"
	QuestionChoice = "choice"
	QuestionScore  = "score"
)

// Request is the POST /v1/systemone request body.
//
// State and every Question's Instructions may be a string, object, or array
// per the confirmed contract, so both are left as json.RawMessage rather than
// typed as string.
type Request struct {
	Model     string              `json:"model"`
	State     json.RawMessage     `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Question is one entry in the request's questions map.
//
// Criteria's shape depends on Type:
//   - choice: required, a JSON object mapping option name to description.
//   - score: required, a JSON array of 2-10 level descriptions (each a
//     string, or an object with "what" and "examples").
//   - noul: optional, a JSON object with optional "true"/"false" keys.
type Question struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// Answer is one entry in the response's answers map. Fields are omitempty so
// a single struct can represent all three answer shapes without emitting
// zero values that don't belong to the answer's Type.
type Answer struct {
	Type string `json:"type"`

	// noul answers.
	Noul *float64 `json:"noul,omitempty"`

	// choice answers.
	Choice string `json:"choice,omitempty"`

	// score answers.
	Score *float64 `json:"score,omitempty"`
	// Legend maps a level index (as a string) to its description, for score
	// answers only.
	Legend map[string]string `json:"legend,omitempty"`

	// Shared by choice and score answers: choice keys option names, score
	// keys level indices (as strings).
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

// Usage reports token counts. Zolem never runs a real tokenizer; these are
// word-count-based estimates, matching the other providers' convention.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is the POST /v1/systemone response body.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

func floatPtr(v float64) *float64 { return &v }
