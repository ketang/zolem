package fixture

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWASMSelectorInput_IncludesTags pins the JSON shape that wasmSelector
// hands to the WASM module: each fixture entry carries id + tags, with tags
// rendered as an object (never null) even when no tags are declared.
func TestWASMSelectorInput_IncludesTags(t *testing.T) {
	in := wasmSelectorInput{
		Request: MatchRequest{
			Provider: "anthropic",
			Version:  "v1",
			Labels:   map[string]string{"method": "GET"},
			Body:     []byte(`{"k":"v"}`),
		},
		Fixtures: []wasmSelectorFixture{
			{ID: "tagged", Tags: map[string]string{"state": "in_progress"}},
			{ID: "untagged", Tags: map[string]string{}},
		},
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		`"provider":"anthropic"`,
		`"id":"tagged"`,
		`"tags":{"state":"in_progress"}`,
		`"id":"untagged"`,
		`"tags":{}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected output to contain %q, got %s", want, s)
		}
	}
	if strings.Contains(s, `"tags":null`) {
		t.Errorf("tags must never be null, got %s", s)
	}
}
