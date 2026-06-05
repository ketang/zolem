package fixture

import "context"

// The selectors in this file are deterministic helpers intended for tests,
// scan tools, and extension integrations that need to construct a non-nil
// Selector or Matcher without standing up a full Runner or fixtures.yaml. They
// are not part of fixture matching policy; production fixture selection uses
// the legacy and fixtures.yaml selectors.

// staticSelector always returns the candidate at a fixed index when present.
type staticSelector struct {
	index int
}

// NewStaticSelector returns a Selector that always picks the candidate at the
// given index, ignoring the request. If the index is out of range for the
// candidate slice (including a negative index or empty candidates), it returns
// nil without error. Intended for tests and tooling, not matching policy.
func NewStaticSelector(index int) Selector {
	return staticSelector{index: index}
}

func (s staticSelector) Select(_ context.Context, _ MatchRequest, candidates []Fixture) (*Fixture, error) {
	if s.index < 0 || s.index >= len(candidates) {
		return nil, nil
	}
	return &candidates[s.index], nil
}

// noopSelector never selects a fixture.
type noopSelector struct{}

// NewNoopSelector returns a Selector that always returns nil without error,
// regardless of the request or candidates. Intended for tests and tooling that
// need a non-nil Selector with no matching behavior.
func NewNoopSelector() Selector {
	return noopSelector{}
}

func (noopSelector) Select(_ context.Context, _ MatchRequest, _ []Fixture) (*Fixture, error) {
	return nil, nil
}

// NewEmptyMatcher returns a Matcher with no fixtures and a noop selector. It is
// a convenience for tests and tooling that need a non-nil *Matcher without
// relying on nil special cases or a Runner; Match always returns nil, nil.
func NewEmptyMatcher() *Matcher {
	return &Matcher{selector: NewNoopSelector()}
}
