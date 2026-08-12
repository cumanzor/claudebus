package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPruneDoesNotSkipADeadPeerInSilence pins the messageless silent-skip, which logos
// measured as the FIRST thing a held handle blocks. PruneChannel claims a dead peer by
// renaming its dir aside, removes it, and then says three things that all assert the
// peer is gone: a "pruned" message, a terminal LedgerLeave, and a "departed" broadcast.
//
// On windows the claim itself fails, because a directory cannot be renamed while
// anything inside it is open. The old bare `continue` discarded that error and the whole
// peer vanished from the report — no prune, no message, nothing. That is worse than the
// false success at the removal below it: a false success at least says something.
//
// The fixture is the measured mechanism, not a staged one. An os.Open handle on a file
// inside the peer dir is exactly the state a live follower is in, since os.Open omits
// FILE_SHARE_DELETE.
//
// A clean pass was never a live outcome here, and that is worth stating because it looks
// like one: the logos POSIX-delete datum was measured for a handle CARRYING the delete
// flag, and this fixture deliberately omits it. Different mask, different question. At
// this mask something always blocks; only which half is in doubt.
//
// Kill mutations: restore the bare `continue` at the rename claim and this fails on the
// silence assertion; restore `_ = os.RemoveAll(tmp)` and the retained branch below fails
// on volumes where the rename succeeds.
func TestPruneDoesNotSkipADeadPeerInSilence(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "ch", "observer", "OTHER") // receives any departed broadcast
	seedPeerPid(t, root, "ch", "ghost", "GONE", "4242")

	ghostDir := filepath.Join(root, "ch", "ghost")
	held, err := os.Open(filepath.Join(ghostDir, "inbox.jsonl"))
	if err != nil {
		t.Fatalf("open the ghost's inbox the way a follower does: %v", err)
	}
	defer held.Close()

	msgs := strings.Join(PruneChannel("ch"), "\n")

	// 1. it must not claim a prune it did not perform
	if strings.Contains(msgs, "pruned ch/ghost") {
		t.Errorf("announced a prune it could not perform: %q", msgs)
	}
	// 2. it must not tell the channel the peer departed
	inbox, _ := os.ReadFile(filepath.Join(root, "ch", "observer", "inbox.jsonl"))
	if strings.Contains(string(inbox), "departed") {
		t.Errorf("broadcast a departure for a peer still on disk: %q", inbox)
	}
	// 3. THE PRIMARY ASSERTION: it must not skip the peer in silence.
	if !strings.Contains(msgs, "could NOT prune ch/ghost") {
		t.Fatalf("a dead peer was skipped with no message at all; an operator reads that as a clean channel. msgs=%q", msgs)
	}

	// which half blocked is a property of the VOLUME, so the case reports it rather than
	// assuming it. The peer dir still at its original path means the rename never ran.
	if _, statErr := os.Stat(ghostDir); statErr == nil {
		if !strings.Contains(msgs, "claim failed") {
			t.Errorf("the rename claim blocked but the message does not say so: %q", msgs)
		}
		// site 526's removal half is UNEXERCISED on this volume, not green: the rename
		// blocked first, so RemoveAll was never reached. Recorded, never asserted.
		t.Log("removal half (site 526) UNEXERCISED here: the rename claim blocked first")
		return
	}
	// retained for volumes where the rename succeeds and the removal is what blocks.
	if !strings.Contains(msgs, ".reap.") {
		t.Errorf("the removal blocked, so the message must name the glob-invisible orphan: %q", msgs)
	}
}

// TestUnregisterDoesNotAnnounceAFailedRemoval is site 383, the same shape one verb over:
// Unregister records a LedgerLeave and broadcasts "departed" after a removal whose error
// used to be discarded. No rename stands in front of it, so this one reaches the removal
// directly and exercises the surfaced error rather than recording it unexercised.
func TestUnregisterDoesNotAnnounceAFailedRemoval(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "ch", "observer", "OTHER")
	seedPeerPid(t, root, "ch", "ghost", "GONE", "4242")

	held, err := os.Open(filepath.Join(root, "ch", "ghost", "inbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if err := Unregister("ch", "ghost"); err == nil {
		t.Error("Unregister reported success while the peer dir was still held open")
	}
	inbox, _ := os.ReadFile(filepath.Join(root, "ch", "observer", "inbox.jsonl"))
	if strings.Contains(string(inbox), "departed") {
		t.Errorf("broadcast a departure for a peer it could not remove: %q", inbox)
	}
}
