package openai

import (
	"strings"
	"sync"
	"testing"
)

// TestNewChatcmplIDConcurrentUnique verifies that concurrent callers never
// receive duplicate streaming completion IDs. The previous implementation
// derived the ID from time.Now().UnixNano(), so two goroutines hitting the
// same nanosecond produced identical IDs.
func TestNewChatcmplIDConcurrentUnique(t *testing.T) {
	const goroutines = 64
	const perGoroutine = 200
	const total = goroutines * perGoroutine

	ids := make([]string, total)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := range perGoroutine {
				ids[base+i] = newChatcmplID()
			}
		}(g * perGoroutine)
	}
	wg.Wait()

	seen := make(map[string]struct{}, total)
	for _, id := range ids {
		if !strings.HasPrefix(id, "chatcmpl-zolem") {
			t.Fatalf("unexpected ID format: %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate chatcmpl ID generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}
