package client

import (
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// The suite spawns real processes to stand in for a live follower and for a reaped pid.
// The binaries that do that differ per platform, so the tests name the INTENT and this
// seam names the binary: liveProc and deadProc are implemented in
// procfixture_unix_test.go and procfixture_windows_test.go.
//
// Two constraints hold on both sides, and both are load-bearing rather than stylistic.
//
// No fixture holds the peer's inbox open. The predicate reads procZombie plus
// procStartTime against the recorded token and nothing else — the argv-grep clause that
// once needed a process whose command line carried the inbox path is deleted
// (liveness.go:108). On windows an open handle also blocks deletion, so a stand-in that
// held the inbox would break t.TempDir cleanup rather than model the follower more
// closely.
//
// No fixture resolves a shell by bare name. On windows `bash` reaches a stub, and WHICH
// stub is not pinned (system32 and the WindowsApps alias both answer), so a bare name
// fails in a way that reads as a port bug instead of a missing shell.

// procGap separates consecutive spawns so their start tokens cannot collide: linux
// starttime counts in 10ms ticks, so two children started back to back can share a
// witness and every wrong-process case silently stops discriminating (F2). It lives
// here rather than at the call sites so the next fixture inherits it.
const procGap = 50 * time.Millisecond

// fixtureSeq makes each live fixture's identity unique within a test binary.
var fixtureSeq atomic.Uint64

// startTracked starts cmd, registers kill-and-reap cleanup, and returns its pid. The
// process is never verified alive here: every caller asserts a live pid reads listening,
// so a fixture that died on start fails those assertions loudly rather than turning them
// vacuous.
func startTracked(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	time.Sleep(procGap)
	if err := cmd.Start(); err != nil {
		t.Fatalf("live fixture %v: %v", cmd.Args, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

// runReaped runs cmd to completion and returns the pid it no longer occupies.
func runReaped(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	if err := cmd.Run(); err != nil {
		t.Fatalf("dead fixture %v: %v", cmd.Args, err)
	}
	return cmd.Process.Pid
}
