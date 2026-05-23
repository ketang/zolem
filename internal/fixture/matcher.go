package fixture

import (
	"context"
	"encoding/json"
	"sync"
)

// MatchRequest carries the fields used by selectors to pick a fixture.
type MatchRequest struct {
	Provider string            `json:"provider"`
	Version  string            `json:"version"`
	Labels   map[string]string `json:"labels"`
	Body     json.RawMessage   `json:"body"`
}

// Matcher selects a Fixture for a given MatchRequest by delegating to a
// Selector. It is safe for concurrent use; Swap may replace the fixture set
// while Match calls are in flight.
type Matcher struct {
	mu       sync.RWMutex
	runner   *Runner
	fixtures []Fixture
	selector Selector
}

// NewMatcher constructs a Matcher. If selector is nil, a Module-only legacy
// selector is used (the runner's Score is called for each candidate that
// carries a compiled module).
func NewMatcher(runner *Runner, fixtures []Fixture, selector Selector) *Matcher {
	if selector == nil {
		selector = &LegacySelector{runner: runner}
	}
	if legacy, ok := selector.(*LegacySelector); ok && legacy.runner == nil {
		legacy.runner = runner
	}
	return &Matcher{runner: runner, fixtures: fixtures, selector: selector}
}

// Swap atomically replaces the fixture set used by future Match calls.
func (m *Matcher) Swap(fixtures []Fixture) {
	m.mu.Lock()
	m.fixtures = fixtures
	m.mu.Unlock()
}

// SwapWithSelector atomically replaces both the fixture set and the selector.
func (m *Matcher) SwapWithSelector(fixtures []Fixture, selector Selector) {
	if selector == nil {
		selector = &LegacySelector{runner: m.runner}
	}
	if legacy, ok := selector.(*LegacySelector); ok && legacy.runner == nil {
		legacy.runner = m.runner
	}
	m.mu.Lock()
	m.fixtures = fixtures
	m.selector = selector
	m.mu.Unlock()
}

// Match filters fixtures by provider/version, then delegates to the selector.
func (m *Matcher) Match(ctx context.Context, req MatchRequest) (*Fixture, error) {
	m.mu.RLock()
	fixtures := m.fixtures
	selector := m.selector
	m.mu.RUnlock()

	candidates := make([]Fixture, 0, len(fixtures))
	for i := range fixtures {
		if fixtures[i].Provider != req.Provider || fixtures[i].Version != req.Version {
			continue
		}
		candidates = append(candidates, fixtures[i])
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	return selector.Select(ctx, req, candidates)
}
