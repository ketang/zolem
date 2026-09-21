package typesafe

import (
	"encoding/json"
	"testing"
)

func TestChoiceOptions_PreservesOrder(t *testing.T) {
	got, err := choiceOptions(json.RawMessage(`{"zeta":"Z","alpha":"A","mid":"M"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"zeta", "alpha", "mid"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v, want %v", i, got, want)
		}
	}
}

func TestChoiceOptions_TooFew(t *testing.T) {
	if _, err := choiceOptions(json.RawMessage(`{"a":"A"}`)); err == nil {
		t.Fatal("expected an error for a single-option choice")
	}
}

func TestScoreLevels_PreservesOrderAndCount(t *testing.T) {
	got, err := scoreLevels(json.RawMessage(`["low","mid","high"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d levels, want 3", len(got))
	}
}

func TestScoreLevels_OutOfRange(t *testing.T) {
	if _, err := scoreLevels(json.RawMessage(`["only"]`)); err == nil {
		t.Fatal("expected an error for a single-level score")
	}
	eleven := `["1","2","3","4","5","6","7","8","9","10","11"]`
	if _, err := scoreLevels(json.RawMessage(eleven)); err == nil {
		t.Fatal("expected an error for an 11-level score")
	}
}

func TestLevelLabel_StringAndObject(t *testing.T) {
	if got := levelLabel(json.RawMessage(`"plain"`)); got != "plain" {
		t.Errorf("got %q, want %q", got, "plain")
	}
	if got := levelLabel(json.RawMessage(`{"what":"structured","examples":"e.g."}`)); got != "structured" {
		t.Errorf("got %q, want %q", got, "structured")
	}
}
