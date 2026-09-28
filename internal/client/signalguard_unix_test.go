//go:build darwin || linux

package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

var signalViolations, expectedRefusals atomic.Int64

// installSignalGuard confines every close signal in this package's tests to
// processes the tests started. Anything else is refused, reported, and fails
// the run, even if a mutated identity check selected it.
func installSignalGuard() func() int64 {
	signalProcess = func(pid int, sig syscall.Signal) error {
		// the production refusal as shipped; it only checks, so a mutant that
		// disables it falls through to the registry below instead of signalling
		if err := ownAncestryRefusal(pid); err != nil {
			return err
		}
		if isTestOwned(pid) {
			return syscall.Kill(pid, sig)
		}
		if expectedRefusals.Add(-1) >= 0 {
			fmt.Fprintf(os.Stderr, "SIGNAL GUARD: expected refusal of %v to pid %d\n", sig, pid)
			return syscall.EPERM
		}
		expectedRefusals.Add(1)
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
	expectedRefusals.Store(1)
	rep := ClosePeer("ch", "peer", true)
	caught := 1 - expectedRefusals.Swap(0)
	if caught != 1 || rep.Ok || !processRunning(owner) {
		t.Fatalf("the guard did not stop a signal to an unregistered owner: caught=%d report=%+v", caught, rep)
	}
}

// Legacy close resolves a live listener's owner by walking its ancestry. From a
// process this test starts, that walk climbs through the test binary into
// whatever launched it; when a harness sits there the production seam must
// refuse it before the pid registry is even consulted.
func TestCloseRefusesOwnAncestry(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	listener := liveProc(t)
	owner, ok := ownerFromPid(listener)
	if !ok || !ownAncestor(owner) {
		t.Skip("no harness process above this test run")
	}
	seedClosePeerWithStart(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(listener), startTokenOf(t, listener))
	before := signalViolations.Load()
	rep := ClosePeer("ch", "peer", true)
	if rep.Ok || !strings.Contains(rep.Detail, errOwnAncestry.Error()) || signalViolations.Load() != before {
		t.Fatalf("close aimed at its own ancestry was not refused by the production seam: %+v", rep)
	}
}

func TestSignalSeamRefusesSelfAndAncestors(t *testing.T) {
	for _, pid := range []int{0, 1, os.Getpid(), os.Getppid()} {
		if err := signalUnlessOwnAncestry(pid, 0); !errors.Is(err, errOwnAncestry) {
			t.Errorf("pid %d was not refused: %v", pid, err)
		}
	}
}
