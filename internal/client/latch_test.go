package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFailedCursorWriteDoesNotLatch is cbus-que.13, and it runs on EVERY platform.
//
// The failure is injected by making the cursor PATH a DIRECTORY: os.Rename onto a
// directory fails deterministically everywhere, so the latch can be pinned without
// windows and without the rename-replace denial that only occurs there. That matters
// because this cluster has never had a locally-killable mutant — every previous
// verification needed the target machine.
//
// The defect: lastSaved used to advance whether or not the write succeeded, so after ONE
// failure the guard went false and no further attempt was ever made. The fix advances
// lastSaved only on success, which leaves the guard true and lets the next tick retry.
//
// KILL MUTATION: restore the unconditional `lastSaved = boundary` and this fails — the
// unblocked write below never happens, because no attempt is ever made again.
func TestFailedCursorWriteDoesNotLatch(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	peer, inbox, _, id := armedPeer(t, "ch", "al")

	// block every write: a directory where the cursor file must go.
	_ = os.Remove(cursorPath(peer))
	if err := os.MkdirAll(cursorPath(peer), 0o755); err != nil {
		t.Fatal(err)
	}

	_, running, stopJoin := startFollowAs(t, inbox, resumePoint{}, id)
	defer stopJoin()
	appendLine(t, inbox, "p", "q", "one")

	// let several ticks pass, each of which must ATTEMPT and fail
	time.Sleep(300 * time.Millisecond)
	if !running() {
		t.Fatal("follower exited on a failing cursor write; it must keep streaming")
	}

	// unblock. If the write latched, no further attempt is ever made and the cursor
	// stays absent forever. If it retries, the very next tick lands it.
	if err := os.Remove(cursorPath(peer)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, off, st := readCursor(peer); st == cursorValid && off > 0 {
			return // retried after the failure: not latched
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err := os.Stat(filepath.Join(peer, cursorFile))
	t.Fatalf("cursor never written after the block was lifted — one failed write latched the guard forever (stat: %v)", err)
}
