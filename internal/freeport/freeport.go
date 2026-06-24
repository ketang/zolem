// Package freeport finds a free TCP port for tests that must hand a concrete
// port to a child process they spawn (for example an admin server launched
// with -local-addr 127.0.0.1:PORT).
//
// It exists so the cmd/zolem and cmd/zolemc end-to-end tests share a single
// port picker instead of each maintaining its own copy. Only _test.go files
// import this package, so it is compiled into the test binaries and never into
// the shipped zolem/zolemc executables.
package freeport

import (
	"net"
	"testing"
)

// Pick returns a TCP port that was free on 127.0.0.1 at the moment of the call,
// skipping the test if loopback listeners are not permitted in the environment.
//
// It binds 127.0.0.1:0, reads the kernel-assigned port, then closes the
// listener so the port can be handed to a child process. This carries an
// inherent time-of-check/time-of-use gap: another process can claim the port
// between the close here and the child's bind. The window is small and the
// e2e tests start their servers sequentially, so collisions are vanishingly
// rare in practice. Eliminating the gap entirely would require handing the
// open listener fd to the child (a server-side change), so callers that intend
// to run in parallel should add a bind-retry loop rather than trusting Pick to
// be race-free.
func Pick(tb testing.TB) int {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Skipf("loopback listeners are not permitted in this environment: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
