package fixture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
)

var noMemoryMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x07, 0x09, 0x01,
	0x05, 0x6d, 0x61, 0x74, 0x63, 0x68, 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x43, 0x00, 0x00,
	0x80, 0x3f, 0x0b,
}

var noMatchExportWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x0a, 0x01, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

var importOnlyWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x02, 0x0f, 0x01, 0x03, 0x65, 0x6e, 0x76,
	0x07, 0x6d, 0x69, 0x73, 0x73, 0x69, 0x6e, 0x67,
	0x00, 0x00,
}

type fixtureSpec struct {
	name             string
	meta             string
	response         string
	templateResponse string
	wasm             []byte
}

func writeFixtureSpec(t *testing.T, root string, spec fixtureSpec) string {
	t.Helper()

	dir := filepath.Join(root, spec.name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture %q: %v", spec.name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(spec.meta), 0o644); err != nil {
		t.Fatalf("write meta for %q: %v", spec.name, err)
	}
	if spec.response != "" {
		if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(spec.response), 0o644); err != nil {
			t.Fatalf("write response for %q: %v", spec.name, err)
		}
	}
	if spec.templateResponse != "" {
		if err := os.WriteFile(filepath.Join(dir, "response.json.tmpl"), []byte(spec.templateResponse), 0o644); err != nil {
			t.Fatalf("write template response for %q: %v", spec.name, err)
		}
	}
	if len(spec.wasm) != 0 {
		if err := os.WriteFile(filepath.Join(dir, "match.wasm"), spec.wasm, 0o644); err != nil {
			t.Fatalf("write wasm for %q: %v", spec.name, err)
		}
	}
	return dir
}

func newRunner(t *testing.T) *fixture.Runner {
	t.Helper()
	r := fixture.NewRunner()
	t.Cleanup(r.Close)
	return r
}

func mustCompileWASM(t *testing.T, r *fixture.Runner, wasm []byte) *fixture.CompiledModule {
	t.Helper()

	mod, err := r.CompileWASM(context.Background(), wasm)
	if err != nil {
		t.Fatalf("compile wasm: %v", err)
	}
	return &mod
}

func TestLoader_MissingFixtureDirectory(t *testing.T) {
	l := fixture.NewLoader(filepath.Join(t.TempDir(), "missing-fixtures"))
	_, err := l.Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "read fixture dir") {
		t.Fatalf("error %q does not mention fixture dir read failure", err)
	}
}

func TestLoader_ReportsParseAndReadFailures(t *testing.T) {
	root := t.TempDir()
	writeFixtureSpec(t, root, fixtureSpec{
		name:     "invalid-meta",
		meta:     "id: [",
		response: `{"ok":true}`,
	})
	writeFixtureSpec(t, root, fixtureSpec{
		name: "missing-response",
		meta: `id: missing-response
provider: anthropic
version: v1
stream: false
`,
	})

	l := fixture.NewLoader(root)
	_, err := l.Load()
	if err == nil {
		t.Fatal("expected loader error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fixture \"invalid-meta\"") {
		t.Fatalf("error %q does not identify the bad fixture", msg)
	}
	if !strings.Contains(msg, "parse meta.yaml") {
		t.Fatalf("error %q does not mention meta parse failure", msg)
	}
}

func TestLoader_DefaultStatusAcrossMixedFixtures(t *testing.T) {
	root := t.TempDir()
	writeFixtureSpec(t, root, fixtureSpec{
		name: "default-status",
		meta: `id: default-status
provider: anthropic
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
stream: true
status: 503
`,
		response: `{"id":"explicit-status"}`,
	})

	fixtures, err := fixture.NewLoader(root).Load()
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
	if got["explicit-status"].Status != 503 {
		t.Errorf("explicit-status fixture: got %d, want 503", got["explicit-status"].Status)
	}
}

func TestLoader_MissingResponseIsReported(t *testing.T) {
	root := t.TempDir()
	writeFixtureSpec(t, root, fixtureSpec{
		name: "missing-response",
		meta: `id: missing-response
provider: anthropic
version: v1
stream: false
`,
	})

	_, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected loader error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fixture \"missing-response\"") {
		t.Fatalf("error %q does not identify the missing-response fixture", msg)
	}
	if !strings.Contains(msg, "response.json or response.json.tmpl") {
		t.Fatalf("error %q does not mention missing response body", msg)
	}
}

func TestLoader_LoadsTemplatedResponseAndSeed(t *testing.T) {
	root := t.TempDir()
	writeFixtureSpec(t, root, fixtureSpec{
		name: "templated",
		meta: `id: templated
provider: openai
version: v1
status: 202
template_seed: 123
`,
		templateResponse: `{"id": {{ json .Fixture.ID }}, "request_sequence": {{ json .Sequence.ProfileRequest }}}`,
	})

	fixtures, err := fixture.NewLoader(root).Load()
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("fixture count: got %d, want 1", len(fixtures))
	}
	got := fixtures[0]
	if !got.Templated {
		t.Fatal("expected templated fixture")
	}
	if got.TemplateSeed == nil || *got.TemplateSeed != 123 {
		t.Fatalf("template seed: got %#v, want 123", got.TemplateSeed)
	}
	rendered, err := fixture.RenderBody(got, fixture.RenderInput{
		Sequence: fixture.TemplateSequenceContext{ProfileRequest: 9, TemplateRender: 2},
	})
	if err != nil {
		t.Fatalf("render body: %v", err)
	}
	if string(rendered) != `{"id": "templated", "request_sequence": 9}` {
		t.Fatalf("rendered body: got %s", rendered)
	}
}

func TestLoader_RejectsStaticAndTemplatedResponseTogether(t *testing.T) {
	root := t.TempDir()
	writeFixtureSpec(t, root, fixtureSpec{
		name: "duplicate-response",
		meta: `id: duplicate-response
provider: openai
version: v1
`,
		response:         `{"id":"static"}`,
		templateResponse: `{"id": {{ json .Fixture.ID }}}`,
	})

	_, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected loader error")
	}
	if !strings.Contains(err.Error(), "only one of response.json or response.json.tmpl") {
		t.Fatalf("error %q does not mention response exclusivity", err)
	}
}

func TestRunner_CompileWASM_InvalidModule(t *testing.T) {
	r := newRunner(t)
	_, err := r.CompileWASM(context.Background(), []byte{0x00, 0x61, 0x73, 0x6d, 0x01})
	if err == nil {
		t.Fatal("expected compile error")
	}
	if !strings.Contains(err.Error(), "compile WASM") {
		t.Fatalf("error %q does not mention compile WASM", err)
	}
}

func TestRunner_Score_MissingExportedMemory(t *testing.T) {
	r := newRunner(t)
	mod := mustCompileWASM(t, r, noMemoryMatchWASM)

	_, err := r.Score(context.Background(), *mod, []byte(`{}`))
	if err == nil {
		t.Fatal("expected score error")
	}
	if !strings.Contains(err.Error(), "module has no exported memory") {
		t.Fatalf("error %q does not mention missing exported memory", err)
	}
}

func TestRunner_Score_MissingMatchFunction(t *testing.T) {
	r := newRunner(t)
	mod := mustCompileWASM(t, r, noMatchExportWASM)

	_, err := r.Score(context.Background(), *mod, []byte(`{}`))
	if err == nil {
		t.Fatal("expected score error")
	}
	if !strings.Contains(err.Error(), "module does not export 'match' function") {
		t.Fatalf("error %q does not mention missing match", err)
	}
}

func TestRunner_Score_InstantiationFailure(t *testing.T) {
	r := newRunner(t)
	mod := mustCompileWASM(t, r, importOnlyWASM)

	_, err := r.Score(context.Background(), *mod, []byte(`{}`))
	if err == nil {
		t.Fatal("expected score error")
	}
	if !strings.Contains(err.Error(), "instantiate:") {
		t.Fatalf("error %q does not mention instantiate failure", err)
	}
}

func TestRunner_Score_OversizedWrite(t *testing.T) {
	r := newRunner(t)
	mod := mustCompileWASM(t, r, alwaysMatchWASM)

	oversized := strings.Repeat("x", 70*1024)
	_, err := r.Score(context.Background(), *mod, []byte(oversized))
	if err == nil {
		t.Fatal("expected score error")
	}
	if !strings.Contains(err.Error(), "failed to write input to WASM memory") {
		t.Fatalf("error %q does not mention the write failure", err)
	}
}

func TestMatcher_SkipsMissingModules(t *testing.T) {
	r := newRunner(t)
	goodMod := mustCompileWASM(t, r, alwaysMatchWASM)

	m := fixture.NewMatcher(r, []fixture.Fixture{
		{ID: "missing-module", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"missing-module"}`)},
		{ID: "good", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"good"}`), Module: goodMod},
	})

	got, err := m.Match(context.Background(), fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil {
		t.Fatal("expected a match")
	}
	if got.ID != "good" {
		t.Fatalf("got %q, want good", got.ID)
	}
}

func TestMatcher_SkipsScoringErrors(t *testing.T) {
	r := newRunner(t)
	badMod := mustCompileWASM(t, r, noMatchExportWASM)
	goodMod := mustCompileWASM(t, r, alwaysMatchWASM)

	m := fixture.NewMatcher(r, []fixture.Fixture{
		{ID: "bad", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"bad"}`), Module: badMod},
		{ID: "good", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"good"}`), Module: goodMod},
	})

	got, err := m.Match(context.Background(), fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil {
		t.Fatal("expected a match")
	}
	if got.ID != "good" {
		t.Fatalf("got %q, want good", got.ID)
	}
}

func TestMatcher_TieBreaksByOrder(t *testing.T) {
	r := newRunner(t)
	firstMod := mustCompileWASM(t, r, alwaysMatchWASM)
	secondMod := mustCompileWASM(t, r, alwaysMatchWASM)

	m := fixture.NewMatcher(r, []fixture.Fixture{
		{ID: "first", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"first"}`), Module: firstMod},
		{ID: "second", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"second"}`), Module: secondMod},
	})

	got, err := m.Match(context.Background(), fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got == nil {
		t.Fatal("expected a match")
	}
	if got.ID != "first" {
		t.Fatalf("got %q, want first", got.ID)
	}
}
