package fixture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
)

// Hand-crafted WASM blobs for selector tests. Each module exports `memory`
// and `select(i32 i32) -> (i32 i32)` and always returns the same (ptr, len)
// pair pointing at a pre-baked output JSON string in a data segment at
// memory offset 1024 (0x80 0x08 in LEB128).
//
// Section layout (shared across the three modules):
//   magic+ver | type | func | memory | export | code | data
//
// The only differences between modules are:
//   - the i32.const <len> in the code section (length of output JSON)
//   - the i32.const <len> in the data segment header
//   - the data bytes themselves
//
// Equivalent WAT (with output text varying):
//   (module
//     (memory (export "memory") 1)
//     (data (i32.const 1024) "{\"id\":\"alpha\"}")
//     (func (export "select") (param i32 i32) (result i32 i32)
//       i32.const 1024
//       i32.const 14))

// selectAlphaWASM: always returns `{"id":"alpha"}` (14 bytes).
var selectAlphaWASM = []byte{
	// magic + version
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// type section: 1 type, (i32,i32) -> (i32,i32)
	0x01, 0x08, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x02, 0x7f, 0x7f,
	// function section: 1 func, type 0
	0x03, 0x02, 0x01, 0x00,
	// memory section: 1 mem, min 1 page
	0x05, 0x03, 0x01, 0x00, 0x01,
	// export section: "memory" (mem 0), "select" (func 0)
	0x07, 0x13, 0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x06, 's', 'e', 'l', 'e', 'c', 't', 0x00, 0x00,
	// code section: 1 func, body = i32.const 1024, i32.const 14, end
	0x0a, 0x09, 0x01, 0x07, 0x00,
	0x41, 0x80, 0x08, // i32.const 1024
	0x41, 0x0e, // i32.const 14
	0x0b,
	// data section: 1 active segment at memory[1024] = `{"id":"alpha"}`
	0x0b, 0x15, 0x01, 0x00,
	0x41, 0x80, 0x08, 0x0b,
	0x0e,
	'{', '"', 'i', 'd', '"', ':', '"', 'a', 'l', 'p', 'h', 'a', '"', '}',
}

// selectNullWASM: always returns `{"id":null}` (11 bytes).
var selectNullWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x08, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x02, 0x7f, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x13, 0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x06, 's', 'e', 'l', 'e', 'c', 't', 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00,
	0x41, 0x80, 0x08,
	0x41, 0x0b, // i32.const 11
	0x0b,
	0x0b, 0x12, 0x01, 0x00,
	0x41, 0x80, 0x08, 0x0b,
	0x0b,
	'{', '"', 'i', 'd', '"', ':', 'n', 'u', 'l', 'l', '}',
}

// selectUnknownWASM: always returns `{"id":"missing"}` (16 bytes).
var selectUnknownWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x08, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x02, 0x7f, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x13, 0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x06, 's', 'e', 'l', 'e', 'c', 't', 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00,
	0x41, 0x80, 0x08,
	0x41, 0x10, // i32.const 16
	0x0b,
	0x0b, 0x17, 0x01, 0x00,
	0x41, 0x80, 0x08, 0x0b,
	0x10,
	'{', '"', 'i', 'd', '"', ':', '"', 'm', 'i', 's', 's', 'i', 'n', 'g', '"', '}',
}

func writeSelectorNamespace(t *testing.T, root string, wasm []byte, withFixturesYAML bool) {
	t.Helper()
	if wasm != nil {
		if err := os.WriteFile(filepath.Join(root, "selector.wasm"), wasm, 0o644); err != nil {
			t.Fatalf("write selector.wasm: %v", err)
		}
	}
	if withFixturesYAML {
		yaml := "provider: anthropic\nversion: v1\nfixtures: []\n"
		if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatalf("write fixtures.yaml: %v", err)
		}
	}
}

func writeSelectorFixture(t *testing.T, root, id string, tags map[string]string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", id, err)
	}
	meta := "id: " + id + "\nprovider: anthropic\nversion: v1\nstatus: 200\n"
	if len(tags) > 0 {
		meta += "tags:\n"
		for k, v := range tags {
			meta += "  " + k + ": " + v + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(`{"id":"`+id+`"}`), 0o644); err != nil {
		t.Fatalf("write response.json: %v", err)
	}
}

func loadWithSelector(t *testing.T, root string) ([]fixture.Fixture, fixture.Selector, *fixture.Runner) {
	t.Helper()
	r := fixture.NewRunner()
	t.Cleanup(r.Close)
	fixtures, selector, err := fixture.NewLoader(root).WithRunner(r).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return fixtures, selector, r
}

func TestWASMSelector_RoutesToReturnedID(t *testing.T) {
	root := t.TempDir()
	writeSelectorNamespace(t, root, selectAlphaWASM, false)
	writeSelectorFixture(t, root, "alpha", map[string]string{"state": "ready"})
	writeSelectorFixture(t, root, "beta", nil)

	fixtures, selector, _ := loadWithSelector(t, root)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}
	got, err := selector.Select(context.Background(), req, fixtures)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.ID != "alpha" {
		t.Fatalf("got fixture %q, want alpha", got.ID)
	}
}

func TestWASMSelector_NullFallsThrough(t *testing.T) {
	root := t.TempDir()
	writeSelectorNamespace(t, root, selectNullWASM, false)
	writeSelectorFixture(t, root, "alpha", nil)

	fixtures, selector, _ := loadWithSelector(t, root)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}
	got, err := selector.Select(context.Background(), req, fixtures)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil (fall-through), got fixture %q", got.ID)
	}
}

func TestWASMSelector_UnknownIDReturnsError(t *testing.T) {
	root := t.TempDir()
	writeSelectorNamespace(t, root, selectUnknownWASM, false)
	writeSelectorFixture(t, root, "alpha", nil)

	fixtures, selector, _ := loadWithSelector(t, root)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}
	_, err := selector.Select(context.Background(), req, fixtures)
	if err == nil {
		t.Fatal("expected error for unknown id, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected error to mention unknown id, got %v", err)
	}
}

func TestLoader_SelectorWASMAndFixturesYAMLAreMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	writeSelectorNamespace(t, root, selectAlphaWASM, true)
	writeSelectorFixture(t, root, "alpha", nil)

	r := fixture.NewRunner()
	defer r.Close()
	_, _, err := fixture.NewLoader(root).WithRunner(r).Load()
	if err == nil {
		t.Fatal("expected load error when both selector.wasm and fixtures.yaml are present, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestLoader_TagsPopulatedFromMetaYAML(t *testing.T) {
	root := t.TempDir()
	writeSelectorFixture(t, root, "tagged", map[string]string{"state": "in_progress", "endpoint": "batch-poll"})
	writeSelectorFixture(t, root, "untagged", nil)

	fixtures, _, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	byID := map[string]fixture.Fixture{}
	for _, f := range fixtures {
		byID[f.ID] = f
	}

	tagged, ok := byID["tagged"]
	if !ok {
		t.Fatal("missing tagged fixture")
	}
	if tagged.Tags["state"] != "in_progress" || tagged.Tags["endpoint"] != "batch-poll" {
		t.Fatalf("unexpected tags: %v", tagged.Tags)
	}

	untagged, ok := byID["untagged"]
	if !ok {
		t.Fatal("missing untagged fixture")
	}
	if untagged.Tags == nil {
		t.Fatal("untagged fixture has nil Tags; want empty map (not nil)")
	}
	if len(untagged.Tags) != 0 {
		t.Fatalf("untagged fixture has tags %v; want empty", untagged.Tags)
	}
}
