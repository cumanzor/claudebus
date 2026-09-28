//go:build darwin || linux

package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
)

var signalViolations atomic.Int64

// installSignalGuard confines every close signal in this package's tests to
// processes the tests started. Anything else is refused, reported, and fails
// the run, even if a mutated identity check selected it.
func installSignalGuard() func() int64 {
	signalProcess = func(pid int, sig syscall.Signal) error {
		if pid > 1 && isTestOwned(pid) {
			return syscall.Kill(pid, sig)
		}
		signalViolations.Add(1)
		fmt.Fprintf(os.Stderr, "SIGNAL GUARD: refused %v to pid %d, which this test binary did not start\n", sig, pid)
		return syscall.EPERM
	}
	return signalViolations.Load
}

// The failure that ended a real session: close walked a listener's ancestry to
// a harness process the tests never started. Here that process is a fake claude
// deliberately left out of the registry, so the guard must refuse the signal.
func TestSignalGuardRefusesAnUnregisteredOwner(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	needle := filepath.Join(root, "ch", "peer", "inbox.jsonl")
	owner, child := fakeSessionTree(t, "claude", needle)
	testOwnedPids.Delete(owner)
	seedClosePeerWithStart(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(child), startTokenOf(t, child))
	before := signalViolations.Load()
	rep := ClosePeer("ch", "peer", true)
	caught := signalViolations.Load() - before
	signalViolations.Add(-caught) // expected here; must not fail the run
	if caught != 1 || rep.Ok || !processRunning(owner) {
		t.Fatalf("the guard did not stop a signal to an unregistered owner: caught=%d report=%+v", caught, rep)
	}
}
