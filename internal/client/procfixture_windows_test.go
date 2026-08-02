package client

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// liveProc waits on a signal name nothing raises, which is how a windows process stays
// alive without a sleep binary, a console or an open file. The name is unique per call:
// waitfor exits when ANY sender raises its signal, so a shared name would let one test
// end another test's follower. /t caps the wait well past any test's lifetime so a
// skipped cleanup leaves nothing running for long.
//
// waitfor.exe, timeout.exe, PING.EXE and cmd.exe were all verified present on the target
// machine. Git-for-Windows ships sleep.exe and true.exe and is deliberately NOT used:
// they sit off PATH at a fixed install path, so depending on them would make the suite
// silently require Git for Windows on every future machine (que.7).
func liveProc(t *testing.T) int {
	t.Helper()
	sig := fmt.Sprintf("cbusfix%dx%d", os.Getpid(), fixtureSeq.Add(1))
	return startTracked(t, exec.Command("waitfor.exe", sig, "/t", "300"))
}

// deadProc exits immediately, leaving a pid whose process object is gone. Reaped here
// means Wait returned and the last handle closed, so OpenProcess fails and the pid reads
// dead through either primitive — distinct from the retained-object case in
// TestPidAliveExitCode259, where a handle is deliberately held open.
func deadProc(t *testing.T) int {
	t.Helper()
	return runReaped(t, exec.Command("cmd.exe", "/c", "exit", "0"))
}
