package fixture_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"zolem.dev/zolem/internal/fixture"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "fixtures")
}

func TestLoader_LoadDirectory(t *testing.T) {
	l := fixture.NewLoader(testdataDir())
	fixtures, err := l.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("expected at least one fixture")
	}
}

func TestLoader_FixtureMetadata(t *testing.T) {
	l := fixture.NewLoader(testdataDir())
	fixtures, _ := l.Load()

	var found *fixture.Fixture
	for i := range fixtures {
		if fixtures[i].ID == "sample-anthropic" {
			found = &fixtures[i]
			break
		}
	}
	if found == nil {
		t.Fatal("sample-anthropic fixture not found")
	}
	if found.Provider != "anthropic" {
		t.Errorf("provider: got %q, want anthropic", found.Provider)
	}
	if !found.Stream {
		t.Error("expected stream: true")
	}
	if len(found.ResponseBody) == 0 {
		t.Error("expected non-empty response body")
	}
}
