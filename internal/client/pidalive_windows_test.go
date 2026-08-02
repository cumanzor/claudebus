package client

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// heldExitedProc runs a child to the given exit code and keeps ONE handle open across
// its death, so the pid still resolves after the process is gone. The caller releases.
//
// Taking that handle does not race the child. exec.Cmd holds its own handle from Start
// until Wait, so the process object is guaranteed to exist even if a `cmd /c exit`
// finished before OpenProcess ran; Wait then drops go's handle and leaves the object
// standing on this one alone.
func heldExitedProc(t *testing.T, code int) (int, func()) {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", "exit", strconv.Itoa(code))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start exit-%d child: %v", code, err)
	}
	pid := cmd.Process.Pid
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|_PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("hold a handle on pid %d before it is reaped: %v", pid, err)
	}
	release := func() { _ = syscall.CloseHandle(h) }
	_ = cmd.Wait() // a non-zero exit is the point; the code is checked below
	if got := cmd.ProcessState.ExitCode(); got != code {
		release()
		t.Fatalf("child exited %d, want %d — the fixture is not the case this pins", got, code)
	}
	return pid, release
}

// TestPidAliveExitCode259 is the case D9 was decided on and, until this test, only
// argued. STILL_ACTIVE is 259, so a pidAlive built on GetExitCodeProcess cannot separate
// a running process from one that exited with code 259: the sentinel is drawn from the
// value space it has to be distinguished from, which makes it unfixable in that
// primitive rather than rare in practice. WaitForSingleObject reads the signalled state
// and has no such collision.
//
// The held handle is the honest state, not pressure staged on the implementation. A
// terminated process whose kernel object persists because someone still holds a handle
// is exactly what windows has in place of a zombie, and it is what the code was written
// to answer for. Drop every handle first and the object goes away, OpenProcess fails
// with ERROR_INVALID_PARAMETER, and BOTH implementations correctly read dead — the case
// would then pass either way and pin nothing.
func TestPidAliveExitCode259(t *testing.T) {
	pid, release := heldExitedProc(t, 259)
	defer release()
	if pidAlive(pid) {
		t.Error("a process that exited with code 259 must read dead; 259 is STILL_ACTIVE, so a GetExitCodeProcess implementation reads it alive forever")
	}
}

// TestPidAliveHeldHandleExitZero is the 259 case's pair: the same retained-object
// fixture with an ordinary exit code, so a reader can see the answer follows the
// process's state and not the number 259.
//
// Its discriminating power is PROBABILISTIC and that is a property of the case, not a
// flake to chase. Measured against the reap-ordering mutant: the 259 case died on 8 runs
// of 8, this one on 6 of 8. The difference is where each one's kill path runs. The 259
// case turns on a value comparison that holds whenever the fixture builds, while this one
// turns on whether the process object still exists at the moment of the probe, which is a
// timing window the mutant widens rather than closes. So a single green here is not
// evidence the case is dead weight, and a single red is not evidence a change broke it;
// only an N-run characterization says anything either way.
func TestPidAliveHeldHandleExitZero(t *testing.T) {
	pid, release := heldExitedProc(t, 0)
	defer release()
	if pidAlive(pid) {
		t.Error("a process that exited with code 0 must read dead even while its object is retained by an open handle")
	}
}

// TestPidAliveLiveProcess is what stops the two exited cases from being vacuous. Both
// assert FALSE, so both pass under an implementation that always answers false; this is
// the only case here that fails one.
func TestPidAliveLiveProcess(t *testing.T) {
	if !pidAlive(liveProc(t)) {
		t.Error("a running process must read alive")
	}
}

// TestPidAliveAccessDeniedReadsAlive exercises the branch D9 wrote and nothing had run:
// OpenProcess denied means the process EXISTS and this token may not open it, so it is
// alive. Reading it dead would prune a peer that is running.
//
// The pre-check is mandatory rather than defensive. csrss is alive under either
// elevation state, so asserting pidAlive true WITHOUT first establishing that this token
// is actually denied would pass on an elevated runner while exercising the OPENED path —
// branch coverage in the report, none in fact. When a test asserts a value that also
// holds on the happy path, it has to establish it is on the unhappy one first.
//
// A skip here is the correct outcome on an elevated runner, not a failure to arrange one.
func TestPidAliveAccessDeniedReadsAlive(t *testing.T) {
	pid := 0
	for p, rec := range snapshotProcs() {
		if strings.EqualFold(rec.Comm, "csrss") {
			pid = p
			break
		}
	}
	if pid == 0 {
		t.Skip("no csrss in the toolhelp snapshot: nothing here is expected to deny")
	}
	// the production mask, SYNCHRONIZE included: pidAlive waits on the handle, so it
	// asks for strictly more than procStartTime does and can be denied where that is not.
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|_PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		_ = syscall.CloseHandle(h)
		t.Skipf("pid %d (csrss) OPENED at the pidAlive mask, so this runner is elevated and the deny branch is unreachable from here", pid)
	}
	if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		t.Skipf("pid %d (csrss) failed with %v rather than ERROR_ACCESS_DENIED, so this is not the deny branch", pid, err)
	}
	if !pidAlive(pid) {
		t.Error("a live process this token may not open must read ALIVE: denied means it exists")
	}
}
