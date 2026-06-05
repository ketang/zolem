package fixture_test

import (
	"context"
	"testing"

	"zolem.dev/zolem/internal/fixture"
)

func staticTestCandidates() []fixture.Fixture {
	return []fixture.Fixture{
		{ID: "first", Provider: "anthropic", Version: "v1"},
		{ID: "second", Provider: "anthropic", Version: "v1"},
		{ID: "third", Provider: "anthropic", Version: "v1"},
	}
}

func TestStaticSelector_PicksIndex(t *testing.T) {
	sel := fixture.NewStaticSelector(1)
	got, err := sel.Select(context.Background(), fixture.MatchRequest{}, staticTestCandidates())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a fixture, got nil")
	}
	if got.ID != "second" {
		t.Errorf("expected fixture at index 1 (second), got %q", got.ID)
	}
}

func TestStaticSelector_OutOfRangeReturnsNil(t *testing.T) {
	cases := []struct {
		name  string
		index int
	}{
		{"negative", -1},
		{"past end", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel := fixture.NewStaticSelector(tc.index)
			got, err := sel.Select(context.Background(), fixture.MatchRequest{}, staticTestCandidates())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Errorf("expected nil, got %q", got.ID)
			}
		})
	}
}

func TestStaticSelector_EmptyCandidatesReturnsNil(t *testing.T) {
	sel := fixture.NewStaticSelector(0)
	got, err := sel.Select(context.Background(), fixture.MatchRequest{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty candidates, got %q", got.ID)
	}
}

func TestNoopSelector_AlwaysNil(t *testing.T) {
	sel := fixture.NewNoopSelector()
	got, err := sel.Select(context.Background(), fixture.MatchRequest{}, staticTestCandidates())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %q", got.ID)
	}
}

func TestNewEmptyMatcher_MatchesNothing(t *testing.T) {
	m := fixture.NewEmptyMatcher()
	if m == nil {
		t.Fatal("expected non-nil matcher")
	}
	got, err := m.Match(context.Background(), fixture.MatchRequest{Provider: "anthropic", Version: "v1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil match, got %q", got.ID)
	}
}

func TestNewMatcher_WithStaticSelector(t *testing.T) {
	fixtures := []fixture.Fixture{
		{ID: "only", Provider: "anthropic", Version: "v1"},
	}
	m := fixture.NewMatcher(nil, fixtures, fixture.NewStaticSelector(0))
	got, err := m.Match(context.Background(), fixture.MatchRequest{Provider: "anthropic", Version: "v1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a fixture, got nil")
	}
	if got.ID != "only" {
		t.Errorf("expected fixture %q, got %q", "only", got.ID)
	}
}
