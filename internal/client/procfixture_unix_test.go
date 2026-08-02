//go:build darwin || linux

package client

import (
	"os/exec"
	"testing"
)

// liveProc stays alive until the test ends. sleep is the smallest thing that does that
// without opening a file or a terminal.
func liveProc(t *testing.T) int {
	t.Helper()
	return startTracked(t, exec.Command("sleep", "300"))
}

// deadProc exits immediately, leaving a reaped pid. /bin/sh by absolute path: the
// never-a-bare-shell-name rule is windows-driven but the seam keeps one shape.
func deadProc(t *testing.T) int {
	t.Helper()
	return runReaped(t, exec.Command("/bin/sh", "-c", "true"))
}
