//go:build linux

package main_test

import (
	"os/exec"
	"syscall"
)

// configureProcReaping arranges for a spawned server process to be cleaned up
// even when the test process dies abruptly.
//
// We deliberately do NOT call Setpgid: leaving the child in the test's process
// group means a terminal interrupt (Ctrl-C, which signals the whole foreground
// process group) reaches the server directly, and zolem shuts down gracefully
// on SIGINT/SIGTERM. Pdeathsig is a backstop for the case where the test binary
// is hard-killed (e.g. `go test -timeout` SIGKILL): when the parent dies the
// kernel sends SIGKILL to the child, so no orphaned zolem processes survive.
func configureProcReaping(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
