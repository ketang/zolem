package fixture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
)

func writeYAMLFixtureDir(t *testing.T, root, id, responseID string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "id: " + id + "\nprovider: anthropic\nversion: v1\nstatus: 200\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	body := `{"id":"` + responseID + `"}`
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func writeFixturesYAML(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixtures.yaml: %v", err)
	}
}

func TestFixturesYAML_RoutesByExpression(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "messages", "msg")
	writeYAMLFixtureDir(t, root, "models", "models")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'labels["path"] == "/v1/messages"'
    fixture: messages
  - expression: 'labels["path"] == "/v1/models"'
    fixture: models
`)

	fixtures, selector, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if selector == nil {
		t.Fatal("expected non-nil selector")
	}

	r := fixture.NewRunner()
	defer r.Close()
	m := fixture.NewMatcher(r, fixtures, selector)

	got, err := m.Match(context.Background(), fixture.MatchRequest{
		Provider: "anthropic", Version: "v1",
		Labels: map[string]string{"path": "/v1/messages"},
		Body:   []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil || got.ID != "messages" {
		t.Fatalf("want messages, got %+v", got)
	}

	got, err = m.Match(context.Background(), fixture.MatchRequest{
		Provider: "anthropic", Version: "v1",
		Labels: map[string]string{"path": "/v1/models"},
		Body:   []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil || got.ID != "models" {
		t.Fatalf("want models, got %+v", got)
	}
}

func TestFixturesYAML_FirstMatchWins(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "first", "first")
	writeYAMLFixtureDir(t, root, "second", "second")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    fixture: first
  - expression: 'true'
    fixture: second
`)
	fixtures, selector, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := fixture.NewRunner()
	defer r.Close()
	m := fixture.NewMatcher(r, fixtures, selector)

	got, err := m.Match(context.Background(), fixture.MatchRequest{
		Provider: "anthropic", Version: "v1",
		Labels: map[string]string{}, Body: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil || got.ID != "first" {
		t.Fatalf("want first, got %+v", got)
	}
}

func TestFixturesYAML_RejectsFixtureWithMatchCEL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "msg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "id: msg\nprovider: anthropic\nversion: v1\nmatch:\n  cel: 'true'\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    fixture: msg
`)

	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "fixtures.yaml") || !strings.Contains(err.Error(), "match.cel") {
		t.Fatalf("error does not mention fixtures.yaml/match.cel: %v", err)
	}
}

func TestFixturesYAML_RejectsFixtureWithMatchWASM(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "msg", "msg")
	if err := os.WriteFile(filepath.Join(root, "msg", "match.wasm"), matcherAlwaysMatchWASM, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    fixture: msg
`)
	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "match.wasm") {
		t.Fatalf("error does not mention match.wasm: %v", err)
	}
}

func TestFixturesYAML_RejectsUnknownFixtureRef(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "msg", "msg")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    fixture: nonexistent
`)
	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("error does not mention unknown fixture id: %v", err)
	}
}
