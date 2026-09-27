package fixture_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "fixtures")
}

func TestLoader_LoadDirectory(t *testing.T) {
	l := fixture.NewLoader(testdataDir())
	fixtures, _, err := l.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("expected at least one fixture")
	}
}

func TestLoader_FixtureMetadata(t *testing.T) {
	l := fixture.NewLoader(testdataDir())
	fixtures, _, _ := l.Load()

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

func TestLoader_RejectsOutOfRangeStatus(t *testing.T) {
	for _, status := range []int{42, -1, 600, 1000, 100, 199} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			root := t.TempDir()
			writeFixtureSpec(t, root, fixtureSpec{
				name: "bad",
				meta: fmt.Sprintf(`id: bad
provider: openai
version: v1
stream: false
status: %d
`, status),
				response: `{"id":"bad"}`,
			})

			_, _, err := fixture.NewLoader(root).Load()
			if err == nil {
				t.Fatalf("status %d: expected loader error", status)
			}
			msg := err.Error()
			if !strings.Contains(msg, `"bad"`) {
				t.Fatalf("status %d: error %q does not identify the fixture ID", status, msg)
			}
			if !strings.Contains(msg, "meta.yaml") {
				t.Fatalf("status %d: error %q does not mention meta.yaml", status, msg)
			}
			if !strings.Contains(msg, "must be between 200 and 599") {
				t.Fatalf("status %d: error %q does not describe the valid range", status, msg)
			}
		})
	}
}

func TestLoader_StatusDefaultAndValidValueLoad(t *testing.T) {
	root := t.TempDir()
	writeFixtureSpec(t, root, fixtureSpec{
		name: "default-status",
		meta: `id: default-status
provider: openai
version: v1
stream: false
`,
		response: `{"id":"default-status"}`,
	})
	writeFixtureSpec(t, root, fixtureSpec{
		name: "explicit-status",
		meta: `id: explicit-status
provider: openai
version: v1
stream: false
status: 429
`,
		response: `{"id":"explicit-status"}`,
	})

	fixtures, _, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}

	got := map[string]fixture.Fixture{}
	for i := range fixtures {
		got[fixtures[i].ID] = fixtures[i]
	}
	if got["default-status"].Status != 200 {
		t.Errorf("default-status fixture: got %d, want 200", got["default-status"].Status)
	}
	if got["explicit-status"].Status != 429 {
		t.Errorf("explicit-status fixture: got %d, want 429", got["explicit-status"].Status)
	}
}
