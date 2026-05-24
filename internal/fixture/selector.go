package fixture

import (
	"context"
	"encoding/json"
	"fmt"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

// Selector picks at most one fixture from candidates filtered by provider/version.
type Selector interface {
	Select(ctx context.Context, req MatchRequest, candidates []Fixture) (*Fixture, error)
}

// LegacySelector reproduces the historical scoring loop: per-fixture CEL or
// WASM modules are evaluated and the highest non-negative score wins, with
// declaration order breaking ties.
type LegacySelector struct {
	runner *Runner
	cel    map[string]*CompiledCELMatcher
}

func NewLegacySelector(runner *Runner) *LegacySelector {
	return &LegacySelector{runner: runner, cel: map[string]*CompiledCELMatcher{}}
}

func (s *LegacySelector) Select(ctx context.Context, req MatchRequest, candidates []Fixture) (*Fixture, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var best *Fixture
	var bestScore float32 = -1
	for i := range candidates {
		f := &candidates[i]
		score := s.score(ctx, f, req, input)
		if score < 0 {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = f
		}
	}
	return best, nil
}

func (s *LegacySelector) score(ctx context.Context, f *Fixture, req MatchRequest, input []byte) float32 {
	if cel, ok := s.cel[f.ID]; ok {
		score, err := cel.Score(ctx, req)
		if err != nil {
			return -1
		}
		return score
	}
	if f.Module != nil && s.runner != nil {
		score, err := s.runner.Score(ctx, *f.Module, input)
		if err != nil {
			return -1
		}
		return score
	}
	return -1
}

// HasMatcher reports whether the legacy selector will ever pick this fixture.
// Meaningful only on the legacy (no fixtures.yaml) path.
func (s *LegacySelector) HasMatcher(f Fixture) bool {
	if _, ok := s.cel[f.ID]; ok {
		return true
	}
	return f.Module != nil || f.WASMPath != ""
}

type fixturesYAMLEntry struct {
	matcher   *CompiledCELMatcher
	fixtureID string         // populated for single-fixture entries
	sequence  *sequenceEntry // populated for sequence entries; mutually exclusive with fixtureID
}

type sequenceEntry struct {
	id        string
	onExhaust ExhaustAction
	steps     []string // fixture IDs, in order
}

// fixturesYAMLSelector evaluates entries from fixtures.yaml in declared order
// and returns the first fixture whose expression matches the request.
// Sequence entries advance a per-(profile, namespace, id) counter on each
// match.
type fixturesYAMLSelector struct {
	entries   []fixturesYAMLEntry
	counters  *SequenceCounters
	namespace string
}

func (s *fixturesYAMLSelector) Select(ctx context.Context, req MatchRequest, candidates []Fixture) (*Fixture, error) {
	byID := make(map[string]*Fixture, len(candidates))
	for i := range candidates {
		byID[candidates[i].ID] = &candidates[i]
	}
	profile := profileFromContext(ctx)
	for _, e := range s.entries {
		score, err := e.matcher.Score(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("evaluate selector entry for fixture %q: %w", e.label(), err)
		}
		if score < 0 {
			continue
		}
		if e.sequence == nil {
			if f, ok := byID[e.fixtureID]; ok {
				return f, nil
			}
			continue
		}
		idx, exhausted := s.counters.Step(profile, s.namespace, e.sequence.id, len(e.sequence.steps), e.sequence.onExhaust)
		if exhausted {
			switch e.sequence.onExhaust {
			case ExhaustErrorAction:
				return nil, &ExhaustError{SequenceID: e.sequence.id, Namespace: s.namespace}
			case ExhaustFallthrough:
				continue
			}
		}
		stepID := e.sequence.steps[idx]
		if f, ok := byID[stepID]; ok {
			return f, nil
		}
	}
	return nil, nil
}

func (e fixturesYAMLEntry) label() string {
	if e.sequence != nil {
		return "sequence:" + e.sequence.id
	}
	return e.fixtureID
}

func profileFromContext(ctx context.Context) string {
	if rt, ok := runtimecfg.ListenerRuntimeFromContext(ctx); ok {
		return rt.Profile.Name
	}
	return ""
}
