package fixture_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
)

// Binary WASM constants for matcher tests.
// Structure mirrors alwaysMatchWASM / noMatchWASM in wasm_test.go;
// only the f32.const bytes differ.

// highMatchWASM returns f32 10.0  (bytes: 0x43, 0x00, 0x00, 0x20, 0x41)
var highMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x20, 0x41, 0x0b,
}

// lowMatchWASM returns f32 1.0  (same as alwaysMatchWASM)
var lowMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

// matcherNoMatchWASM returns f32 -1.0
var matcherNoMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0xbf, 0x0b,
}

// matcherAlwaysMatchWASM returns f32 1.0 (identical to lowMatchWASM; kept separate for readability)
var matcherAlwaysMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

func TestMatcher_MatchesHighestScore(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	highMod, err := r.CompileWASM(context.Background(), highMatchWASM)
	if err != nil {
		t.Fatalf("compile high: %v", err)
	}
	lowMod, err := r.CompileWASM(context.Background(), lowMatchWASM)
	if err != nil {
		t.Fatalf("compile low: %v", err)
	}

	fixtures := []fixture.Fixture{
		{ID: "low", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"low"}`), Module: &lowMod},
		{ID: "high", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"high"}`), Module: &highMod},
	}

	m := fixture.NewMatcher(r, fixtures, nil)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}

	result, err := m.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a match")
	}
	if result.ID != "high" {
		t.Errorf("expected high-scoring fixture, got %q", result.ID)
	}
}

func TestMatcher_NilOnNoMatch(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	noMod, err := r.CompileWASM(context.Background(), matcherNoMatchWASM)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	fixtures := []fixture.Fixture{
		{ID: "none", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{}`), Module: &noMod},
	}

	m := fixture.NewMatcher(r, fixtures, nil)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}

	result, err := m.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got fixture %q", result.ID)
	}
}

func TestMatcher_SkipsWrongProviderOrVersion(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	alwaysMod, err := r.CompileWASM(context.Background(), matcherAlwaysMatchWASM)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	fixtures := []fixture.Fixture{
		{ID: "openai-fixture", Provider: "openai", Version: "v1", Status: 200, ResponseBody: []byte(`{}`), Module: &alwaysMod},
	}

	m := fixture.NewMatcher(r, fixtures, nil)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}

	result, _ := m.Match(context.Background(), req)
	if result != nil {
		t.Error("fixture for wrong provider should not match")
	}
}

func TestMatcher_CELMatchesRequestBodyAndLabels(t *testing.T) {
	root := t.TempDir()
	writeCELMatcherFixture(t, root, "low", "labels[\"tenant\"] == \"acme\"", 1)
	writeCELMatcherFixture(t, root, "high", `body["model"] == "gpt-4o-mini" && body["messages"][0]["content"] == "refund"`, 20)

	fixtures, selector, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}

	r := fixture.NewRunner()
	defer r.Close()
	m := fixture.NewMatcher(r, fixtures, selector)

	got, err := m.Match(context.Background(), fixture.MatchRequest{
		Provider: "openai",
		Version:  "v1",
		Labels:   map[string]string{"tenant": "acme"},
		Body:     []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"refund"}]}`),
	})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil {
		t.Fatal("expected CEL fixture match")
	}
	if got.ID != "high" {
		t.Fatalf("matched fixture: got %q, want high", got.ID)
	}
}

func TestMatcher_CELFalseDoesNotMatch(t *testing.T) {
	root := t.TempDir()
	writeCELMatcherFixture(t, root, "false", "false", 1)

	fixtures, selector, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}

	r := fixture.NewRunner()
	defer r.Close()
	m := fixture.NewMatcher(r, fixtures, selector)

	got, err := m.Match(context.Background(), fixture.MatchRequest{
		Provider: "openai",
		Version:  "v1",
		Labels:   map[string]string{},
		Body:     []byte(`{"model":"gpt-4o-mini"}`),
	})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no match, got %q", got.ID)
	}
}

func writeCELMatcherFixture(t *testing.T, root, id, expr string, score int) {
	t.Helper()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture %q: %v", id, err)
	}
	meta := "id: " + id + "\n" +
		"provider: openai\n" +
		"version: v1\n" +
		"status: 200\n" +
		"match:\n" +
		"  cel: " + strconv.Quote(expr) + "\n" +
		"  score: " + strconv.Itoa(score) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta for %q: %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(`{"id":"`+id+`"}`), 0o644); err != nil {
		t.Fatalf("write response for %q: %v", id, err)
	}
}
