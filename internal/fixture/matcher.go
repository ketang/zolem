package fixture

import (
	"context"
	"encoding/json"
)

// MatchRequest carries the fields used by WASM scoring modules to select a fixture.
type MatchRequest struct {
	Provider string            `json:"provider"`
	Version  string            `json:"version"`
	Labels   map[string]string `json:"labels"`
	Body     json.RawMessage   `json:"body"`
}

// Matcher selects the best-matching Fixture for a given MatchRequest.
type Matcher struct {
	runner   *Runner
	fixtures []Fixture
}

// NewMatcher constructs a Matcher from a Runner and a slice of Fixtures.
func NewMatcher(runner *Runner, fixtures []Fixture) *Matcher {
	return &Matcher{runner: runner, fixtures: fixtures}
}

// Match evaluates all fixtures whose Provider and Version match req, calls
// their WASM scoring module, and returns the fixture with the highest
// non-negative score.  Returns nil (no error) when nothing scores >= 0.
func (m *Matcher) Match(ctx context.Context, req MatchRequest) (*Fixture, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var best *Fixture
	var bestScore float32 = -1

	for i := range m.fixtures {
		f := &m.fixtures[i]
		if f.Provider != req.Provider || f.Version != req.Version {
			continue
		}
		if f.Module == nil {
			continue
		}
		score, err := m.runner.Score(ctx, *f.Module, input)
		if err != nil || score < 0 {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = f
		}
	}
	return best, nil
}
