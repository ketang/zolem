package fixture

import (
	"fmt"
	"sync"
)

// ExhaustAction controls what happens after the last step of a sequence has
// been served.
type ExhaustAction string

const (
	// ExhaustLast keeps returning the final step indefinitely.
	ExhaustLast ExhaustAction = "last"
	// ExhaustCycle wraps back to step 0 on overflow.
	ExhaustCycle ExhaustAction = "cycle"
	// ExhaustErrorAction causes the selector to return an *ExhaustError
	// sentinel once the sequence has been fully consumed. Translating the
	// sentinel into a provider-native error response is the caller's job;
	// the fixture package stays ignorant of provider wire formats.
	ExhaustErrorAction ExhaustAction = "error"
	// ExhaustFallthrough stops matching this entry once exhausted, allowing
	// later fixtures.yaml entries to evaluate.
	ExhaustFallthrough ExhaustAction = "fallthrough"
)

// DefaultExhaustAction is applied when on_exhaust is omitted.
const DefaultExhaustAction = ExhaustLast

// ParseExhaustAction validates and normalises a YAML on_exhaust value. An
// empty string yields DefaultExhaustAction.
func ParseExhaustAction(s string) (ExhaustAction, error) {
	switch s {
	case "":
		return DefaultExhaustAction, nil
	case string(ExhaustLast):
		return ExhaustLast, nil
	case string(ExhaustCycle):
		return ExhaustCycle, nil
	case string(ExhaustErrorAction):
		return ExhaustErrorAction, nil
	case string(ExhaustFallthrough):
		return ExhaustFallthrough, nil
	default:
		return "", fmt.Errorf("invalid on_exhaust %q (want last|cycle|error|fallthrough)", s)
	}
}

// ExhaustError signals that a sequence configured with on_exhaust: error has
// run past its final step. The dispatch layer translates this into a
// provider-native error response (e.g. via BackendError); the fixture package
// never constructs provider wire formats itself.
type ExhaustError struct {
	SequenceID string
	Namespace  string
}

func (e *ExhaustError) Error() string {
	return fmt.Sprintf("fixture sequence %q in namespace %q exhausted", e.SequenceID, e.Namespace)
}

// sequenceKey identifies a per-sequence counter. The namespace discriminator
// is required so two namespaces declaring the same sequence id (e.g. "poll")
// do not share a counter.
type sequenceKey struct {
	profile   string
	namespace string
	id        string
}

// SequenceCounters tracks per-sequence step indices keyed by
// (profile, namespace, sequence id). Counters are in-memory and reset on
// server restart. It mirrors the sync.Mutex pattern in ProfileCounters.
type SequenceCounters struct {
	mu       sync.Mutex
	counters map[sequenceKey]int
}

// NewSequenceCounters returns an empty counter store ready for use.
func NewSequenceCounters() *SequenceCounters {
	return &SequenceCounters{counters: map[sequenceKey]int{}}
}

// Step advances the counter for one matching request and returns the
// 0-based step index to serve. If exhausted is true the caller should apply
// the on_exhaust policy at the selector layer (return an *ExhaustError or
// skip the entry for fallthrough). For ExhaustLast and ExhaustCycle the
// function never sets exhausted=true — it returns a valid step index.
func (c *SequenceCounters) Step(profile, namespace, id string, total int, onExhaust ExhaustAction) (index int, exhausted bool) {
	if c == nil || total <= 0 {
		return 0, true
	}
	key := sequenceKey{profile: profile, namespace: namespace, id: id}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.counters[key]
	if n < total {
		c.counters[key] = n + 1
		return n, false
	}
	// We've already served every step at least once.
	switch onExhaust {
	case ExhaustLast:
		// Clamp the counter and keep returning the final step.
		return total - 1, false
	case ExhaustCycle:
		idx := n % total
		c.counters[key] = n + 1
		return idx, false
	case ExhaustErrorAction, ExhaustFallthrough:
		// Counter stays at len(steps); selector decides the outward shape.
		return 0, true
	default:
		return total - 1, false
	}
}

// Peek returns the current counter value without advancing it. Intended for
// tests and diagnostics; production code should use Step.
func (c *SequenceCounters) Peek(profile, namespace, id string) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counters[sequenceKey{profile: profile, namespace: namespace, id: id}]
}
