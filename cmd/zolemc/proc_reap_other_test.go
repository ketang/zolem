//go:build !linux

package main

import "os/exec"

// configureProcReaping is a no-op on platforms without Pdeathsig. Spawned
// servers remain in the test's process group, so terminal interrupts still
// reach them, and t.Cleanup kills the process on the normal path.
func configureProcReaping(cmd *exec.Cmd) {}
