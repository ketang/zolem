package fixture_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
	runtimecfg "zolem.dev/zolem/internal/runtime"
)

func ctxWithProfile(name string) context.Context {
	return runtimecfg.WithListenerRuntime(context.Background(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: name},
	})
}

func loadSeqSelector(t *testing.T, root, yaml string, counters *fixture.SequenceCounters) (*fixture.Matcher, []fixture.Fixture) {
	t.Helper()
	writeFixturesYAML(t, root, yaml)
	fixtures, selector, err := fixture.NewLoader(root).WithSequenceCounters(counters).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := fixture.NewRunner()
	t.Cleanup(r.Close)
	return fixture.NewMatcher(r, fixtures, selector), fixtures
}

func mustMatch(t *testing.T, m *fixture.Matcher, ctx context.Context, path string) *fixture.Fixture {
	t.Helper()
	got, err := m.Match(ctx, fixture.MatchRequest{
		Provider: "anthropic", Version: "v1",
		Labels: map[string]string{"path": path},
		Body:   []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("match %q: %v", path, err)
	}
	return got
}

func TestSequence_StepsThroughInOrder(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "step-a", "a")
	writeYAMLFixtureDir(t, root, "step-b", "b")
	writeYAMLFixtureDir(t, root, "step-c", "c")
	counters := fixture.NewSequenceCounters()
	m, _ := loadSeqSelector(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'labels["path"] == "/p"'
    sequence:
      id: poll
      on_exhaust: last
      steps: [step-a, step-b, step-c]
`, counters)
	ctx := ctxWithProfile("default")
	want := []string{"step-a", "step-b", "step-c"}
	for i, id := range want {
		got := mustMatch(t, m, ctx, "/p")
		if got == nil || got.ID != id {
			t.Fatalf("request %d: want %q, got %+v", i, id, got)
		}
	}
}

func TestSequence_OnExhaustLast(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeYAMLFixtureDir(t, root, "b", "b")
	counters := fixture.NewSequenceCounters()
	m, _ := loadSeqSelector(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      on_exhaust: last
      steps: [a, b]
`, counters)
	ctx := ctxWithProfile("default")
	mustMatch(t, m, ctx, "/")
	mustMatch(t, m, ctx, "/")
	for i := 0; i < 5; i++ {
		got := mustMatch(t, m, ctx, "/")
		if got == nil || got.ID != "b" {
			t.Fatalf("extra request %d: want b, got %+v", i, got)
		}
	}
}

func TestSequence_OnExhaustCycle(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeYAMLFixtureDir(t, root, "b", "b")
	writeYAMLFixtureDir(t, root, "c", "c")
	counters := fixture.NewSequenceCounters()
	m, _ := loadSeqSelector(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      on_exhaust: cycle
      steps: [a, b, c]
`, counters)
	ctx := ctxWithProfile("default")
	order := []string{"a", "b", "c", "a", "b", "c", "a"}
	for i, want := range order {
		got := mustMatch(t, m, ctx, "/")
		if got == nil || got.ID != want {
			t.Fatalf("request %d: want %q, got %+v", i, want, got)
		}
	}
}

func TestSequence_OnExhaustError(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeYAMLFixtureDir(t, root, "b", "b")
	counters := fixture.NewSequenceCounters()
	m, _ := loadSeqSelector(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      on_exhaust: error
      steps: [a, b]
`, counters)
	ctx := ctxWithProfile("default")
	mustMatch(t, m, ctx, "/")
	mustMatch(t, m, ctx, "/")
	_, err := m.Match(ctx, fixture.MatchRequest{
		Provider: "anthropic", Version: "v1",
		Labels: map[string]string{}, Body: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("want ExhaustError, got nil")
	}
	var ee *fixture.ExhaustError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExhaustError, got %T: %v", err, err)
	}
	if ee.SequenceID != "s" {
		t.Fatalf("SequenceID: want s, got %q", ee.SequenceID)
	}
	if ee.Namespace != "anthropic:v1" {
		t.Fatalf("Namespace: want anthropic:v1, got %q", ee.Namespace)
	}
}

func TestSequence_OnExhaustFallthrough(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeYAMLFixtureDir(t, root, "b", "b")
	writeYAMLFixtureDir(t, root, "fallback", "fallback")
	counters := fixture.NewSequenceCounters()
	m, _ := loadSeqSelector(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      on_exhaust: fallthrough
      steps: [a, b]
  - expression: 'true'
    fixture: fallback
`, counters)
	ctx := ctxWithProfile("default")
	if got := mustMatch(t, m, ctx, "/"); got.ID != "a" {
		t.Fatalf("req 1: want a, got %s", got.ID)
	}
	if got := mustMatch(t, m, ctx, "/"); got.ID != "b" {
		t.Fatalf("req 2: want b, got %s", got.ID)
	}
	for i := 0; i < 3; i++ {
		got := mustMatch(t, m, ctx, "/")
		if got == nil || got.ID != "fallback" {
			t.Fatalf("after exhaustion req %d: want fallback, got %+v", i, got)
		}
	}
}

func TestSequence_TwoSequencesAdvanceIndependently(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a1", "a1")
	writeYAMLFixtureDir(t, root, "a2", "a2")
	writeYAMLFixtureDir(t, root, "b1", "b1")
	writeYAMLFixtureDir(t, root, "b2", "b2")
	counters := fixture.NewSequenceCounters()
	m, _ := loadSeqSelector(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'labels["path"] == "/a"'
    sequence:
      id: alpha
      on_exhaust: last
      steps: [a1, a2]
  - expression: 'labels["path"] == "/b"'
    sequence:
      id: beta
      on_exhaust: last
      steps: [b1, b2]
`, counters)
	ctx := ctxWithProfile("default")
	if got := mustMatch(t, m, ctx, "/a"); got.ID != "a1" {
		t.Fatalf("/a #1 want a1, got %s", got.ID)
	}
	// hitting /b should not advance /a's counter
	if got := mustMatch(t, m, ctx, "/b"); got.ID != "b1" {
		t.Fatalf("/b #1 want b1, got %s", got.ID)
	}
	if got := mustMatch(t, m, ctx, "/a"); got.ID != "a2" {
		t.Fatalf("/a #2 want a2, got %s", got.ID)
	}
	if got := mustMatch(t, m, ctx, "/b"); got.ID != "b2" {
		t.Fatalf("/b #2 want b2, got %s", got.ID)
	}
}

func TestSequence_NamespaceDiscriminator(t *testing.T) {
	// Two distinct namespaces declaring the same sequence id must not share
	// a counter.
	dirA := filepath.Join(t.TempDir(), "ns-a")
	dirB := filepath.Join(t.TempDir(), "ns-b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		writeYAMLFixtureDir(t, d, "x", "x")
		writeYAMLFixtureDir(t, d, "y", "y")
	}
	yaml := func(provider string) string {
		return `provider: ` + provider + `
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: poll
      on_exhaust: last
      steps: [x, y]
`
	}
	writeFixturesYAML(t, dirA, yaml("anthropic"))
	writeFixturesYAML(t, dirB, yaml("openai"))

	counters := fixture.NewSequenceCounters()
	fxA, selA, err := fixture.NewLoader(dirA).WithSequenceCounters(counters).Load()
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	fxB, selB, err := fixture.NewLoader(dirB).WithSequenceCounters(counters).Load()
	if err != nil {
		t.Fatalf("load B: %v", err)
	}
	// Re-tag namespace via WithNamespace would be equivalent, but we rely
	// on the default provider:version discriminator here. The fixtures.yaml
	// docs use different provider headers so namespaces are distinct.
	// Adjust fixtures to match their declared providers.
	for i := range fxA {
		fxA[i].Provider = "anthropic"
	}
	for i := range fxB {
		fxB[i].Provider = "openai"
	}
	r := fixture.NewRunner()
	defer r.Close()
	mA := fixture.NewMatcher(r, fxA, selA)
	mB := fixture.NewMatcher(r, fxB, selB)
	ctx := ctxWithProfile("default")
	// Advance namespace A's counter once.
	got, err := mA.Match(ctx, fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)})
	if err != nil || got == nil || got.ID != "x" {
		t.Fatalf("A #1: %+v err=%v", got, err)
	}
	// Namespace B must still be at step 0.
	got, err = mB.Match(ctx, fixture.MatchRequest{Provider: "openai", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)})
	if err != nil || got == nil || got.ID != "x" {
		t.Fatalf("B #1 (must be independent of A): %+v err=%v", got, err)
	}
}

func TestSequence_LoadError_FixtureAndSequenceTogether(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    fixture: a
    sequence:
      id: s
      steps: [a]
`)
	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error missing 'mutually exclusive': %v", err)
	}
}

func TestSequence_LoadError_UnknownStep(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      steps: [a, nope]
`)
	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error missing unknown id 'nope': %v", err)
	}
}

func TestSequence_LoadError_InvalidOnExhaust(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      on_exhaust: explode
      steps: [a]
`)
	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "on_exhaust") {
		t.Fatalf("error missing on_exhaust mention: %v", err)
	}
}

func TestSequence_LoadError_EmptySteps(t *testing.T) {
	root := t.TempDir()
	writeYAMLFixtureDir(t, root, "a", "a")
	writeFixturesYAML(t, root, `provider: anthropic
version: v1
fixtures:
  - expression: 'true'
    sequence:
      id: s
      steps: []
`)
	_, _, err := fixture.NewLoader(root).Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "steps") {
		t.Fatalf("error missing 'steps': %v", err)
	}
}
