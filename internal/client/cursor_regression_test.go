package client

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drainInto performs a real arm and returns everything the follower emitted.
//
// The armMeta call is load-bearing, not decoration: ArmLocalTail resolves the resume
// point and THEN records the listener, so the second arm of a peer sees a non-null
// listenerPid. A helper that skipped it would make every arm look like a first arm,
// the tri-state would take the byte-0 branch every time, and a test claiming to prove
// cbus-8no would instead be proving that a full replay delivers everything — which it
// trivially does. Mirroring ArmLocalTail's exact order is what makes the loss real.
func drainInto(t *testing.T, inbox, metaPath string, settle time.Duration) string {
	t.Helper()
	resume := resolveResume(inbox, metaPath)
	armMeta(metaPath, selfStart(t))
	buf, _, stopJoin := startFollow(t, inbox, resume)
	time.Sleep(settle)
	stopJoin()
	return buf.String()
}

// TestRenameWindowLosesNothing is cbus-8no expressed as an assertion, and it is the
// test this milestone exists to pass.
//
// The bug: a rename invalidates the listener (M2), so the peer reads dead and the
// session re-arms. Under the tri-state that re-arm seeks END, so anything sent between
// the rename and the re-arm is skipped — silently, permanently, with no error anywhere.
// The window is small and entirely ordinary: it is exactly how long a session takes to
// notice its tail died and run `cbus tail` again.
//
// On v0.4.0 semantics this test fails: the windowed message is gone. With the cursor it
// passes, because the re-arm resumes from where the reader actually got to rather than
// from wherever the file happens to end.
func TestRenameWindowLosesNothing(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(root, "dev", "main")
	inbox := filepath.Join(peer, "inbox.jsonl")
	meta := filepath.Join(peer, "meta.json")

	// a first arm reads the peer up to date and leaves a cursor behind
	appendLine(t, inbox, "peer", "me", "before-the-rename")
	if out := drainInto(t, inbox, meta, 400*time.Millisecond); !strings.Contains(out, "before-the-rename") {
		t.Fatalf("first arm did not deliver the pre-rename message: %q", out)
	}

	// the rename: dir moves, M2 clears the identity, the old tail is stale
	if _, _, _, err := Rename("newname", ""); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	newPeer := filepath.Join(root, "dev", "newname")
	newInbox := filepath.Join(newPeer, "inbox.jsonl")
	newMeta := filepath.Join(newPeer, "meta.json")

	// THE WINDOW: a peer sends while nothing is armed. Under the tri-state this is the
	// message that vanishes.
	appendLine(t, newInbox, "peer", "me", "sent-during-the-window")

	out := drainInto(t, newInbox, newMeta, 500*time.Millisecond)
	if !strings.Contains(out, "sent-during-the-window") {
		t.Errorf("cbus-8no: the message sent during the rename window was LOST\n"+
			"re-arm emitted: %q", out)
	}
	if strings.Contains(out, "before-the-rename") {
		t.Errorf("the re-arm replayed a message the previous arm had already delivered — "+
			"the cursor should resume, not restart:\n%q", out)
	}
}

// TestForceIntoDeadGapDelivers is the same defect through the other door, which is why
// it gets its own test rather than a comment on the one above. `cbus send --force` to a
// peer whose listener is dead queues the line and warns that it may never arrive; under
// the tri-state that warning was accurate, because the eventual re-arm seeked END and
// stepped straight over it. With a cursor the queued line is delivered and the warning
// becomes conservative rather than true.
func TestForceIntoDeadGapDelivers(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(root, "dev", "main")
	inbox := filepath.Join(peer, "inbox.jsonl")
	meta := filepath.Join(peer, "meta.json")

	appendLine(t, inbox, "peer", "me", "delivered-normally")
	if out := drainInto(t, inbox, meta, 400*time.Millisecond); !strings.Contains(out, "delivered-normally") {
		t.Fatalf("first arm delivered nothing: %q", out)
	}

	// the listener dies (a reaped pid stands in for the crash) and a --force send lands
	// in the gap
	dead := deadPidFor(t)
	writeMetaJSON(t, peer, fmt.Sprint(dead))
	appendLine(t, inbox, "peer", "me", "forced-into-the-gap")

	if out := drainInto(t, inbox, meta, 500*time.Millisecond); !strings.Contains(out, "forced-into-the-gap") {
		t.Errorf("a --force line queued into a dead gap was never delivered: %q", out)
	}
}

// TestMigrationSelfHeals is rider P5. Seeking END for a cursor-less ever-armed peer is
// correct exactly ONCE — if that arm failed to leave a cursor behind, every subsequent
// arm would seek END again and the peer would keep skipping its dead windows forever,
// which is the pre-cursor bug wearing the migration's clothes.
func TestMigrationSelfHeals(t *testing.T) {
	dir, inbox := seedInbox(t, `{"from":"p","to":"q","text":"history"}`)
	meta := writeMetaJSON(t, dir, "4242") // ever armed, no cursor: the upgrade state

	if got := resolveResume(inbox, meta); !got.seekEnd {
		t.Fatalf("precondition: want the migration rule, got %+v", got)
	}

	buf, _, stopJoin := startFollow(t, inbox, resolveResume(inbox, meta))
	time.Sleep(400 * time.Millisecond)
	if strings.Contains(buf.String(), "history") {
		t.Error("the migration arm replayed history; it must seek END")
	}
	appendLine(t, inbox, "peer", "me", "after-upgrade")
	time.Sleep(400 * time.Millisecond)
	stopJoin()
	if !strings.Contains(buf.String(), "after-upgrade") {
		t.Error("the migration arm did not stream new traffic")
	}

	// the self-heal: a cursor now exists, so the NEXT arm resumes instead of migrating
	_, _, off, state := readCursor(dir)
	if state != cursorValid {
		t.Fatalf("migration arm left no cursor (state %d) — every later arm would seek END again", state)
	}
	if got := resolveResume(inbox, meta); got.seekEnd {
		t.Error("the second arm still takes the migration path; the rule did not self-heal")
	} else if got.offset != off {
		t.Errorf("second arm resumes at %d, want the recorded cursor %d", got.offset, off)
	}
}

// deadPidFor returns a pid that has certainly exited.
func deadPidFor(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}
