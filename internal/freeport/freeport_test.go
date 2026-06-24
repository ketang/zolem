package freeport

import (
	"fmt"
	"net"
	"testing"
)

// TestPickReturnsBindablePort verifies that Pick reports a port that is
// actually free: re-binding it must succeed and the reported port must be in
// the valid TCP range.
func TestPickReturnsBindablePort(t *testing.T) {
	port := Pick(t)
	if port <= 0 || port > 65535 {
		t.Fatalf("Pick returned out-of-range port %d", port)
	}

	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("re-binding port %d returned by Pick failed: %v", port, err)
	}
	t.Cleanup(func() { _ = l.Close() })
}

// TestPickVariesAcrossCalls guards against Pick handing out the same port on
// back-to-back calls, which would defeat its use for distinct child processes.
func TestPickVariesAcrossCalls(t *testing.T) {
	seen := make(map[int]struct{})
	for range 8 {
		seen[Pick(t)] = struct{}{}
	}
	if len(seen) == 1 {
		t.Fatalf("Pick returned the same port across %d calls", 8)
	}
}
