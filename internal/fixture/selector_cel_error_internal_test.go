package fixture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeErrTestFixture(t *testing.T, root, id string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "id: " + id + "\nprovider: openai\nversion: v1\nstatus: 200\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(`{"id":"`+id+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

const celErrorYAML = `provider: openai
version: v1
fixtures:
  - expression: 'body["metadata"]["tenant"] == "acme"'
    fixture: ok
  - expression: 'true'
    fixture: ok
`

func loadCELErrorSelector(t *testing.T) (*fixturesYAMLSelector, []Fixture) {
	t.Helper()
	root := t.TempDir()
	writeErrTestFixture(t, root, "ok")
	if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(celErrorYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtures, sel, err := NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ys, ok := sel.(*fixturesYAMLSelector)
	if !ok {
		t.Fatalf("selector is %T, want *fixturesYAMLSelector", sel)
	}
	return ys, fixtures
}

func TestFixturesYAMLSelector_CELErrorSkipsEntry(t *testing.T) {
	ys, fixtures := loadCELErrorSelector(t)
	ys.logf = func(string, ...any) {}
	got, err := ys.Select(context.Background(), MatchRequest{
		Provider: "openai", Version: "v1", Labels: map[string]string{},
		Body: []byte(`{"messages":[]}`),
	}, fixtures)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got == nil || got.ID != "ok" {
		t.Fatalf("want fixture ok via catch-all, got %+v", got)
	}
}

func TestFixturesYAMLSelector_CELErrorLoggedOnce(t *testing.T) {
	ys, fixtures := loadCELErrorSelector(t)
	var mu sync.Mutex
	var lines []string
	ys.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	for i := 0; i < 3; i++ {
		if _, err := ys.Select(context.Background(), MatchRequest{
			Provider: "openai", Version: "v1", Labels: map[string]string{},
			Body: []byte(`{"messages":[]}`),
		}, fixtures); err != nil {
			t.Fatalf("select %d: %v", i, err)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 log line, got %d: %v", len(lines), lines)
	}
	for _, want := range []string{"openai:v1", "entry 0", "(ok)", "warn: fixtures.yaml"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line %q missing %q", lines[0], want)
		}
	}
}
