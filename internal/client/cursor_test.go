package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedInbox writes lines into a peer's inbox and returns the peer dir + inbox path.
func seedInbox(t *testing.T, lines ...string) (peerDir, inbox string) {
	t.Helper()
	peerDir = t.TempDir()
	inbox = filepath.Join(peerDir, "inbox.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(inbox, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return peerDir, inbox
}

func devInoOfPath(t *testing.T, path string) (uint64, uint64) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, ok := statDevIno(st)
	if !ok {
		t.Fatal("no dev/ino")
	}
	return dev, ino
}

// writeMetaJSON seeds a bare meta.json with the given listenerPid literal.
func writeMetaJSON(t *testing.T, peerDir, listenerPid string) string {
	t.Helper()
	p := filepath.Join(peerDir, "meta.json")
	if err := os.WriteFile(p, []byte(fmt.Sprintf(
		`{"alias":"a","channel":"c","listenerPid":%s,"ownerPid":null}`, listenerPid)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, st := readCursor(dir); st != cursorAbsent {
		t.Error("a missing cursor must read ABSENT (the migration signal)")
	}
	writeCursor(dir, 7, 4242, 1337)
	dev, ino, off, st := readCursor(dir)
	if st != cursorValid || dev != 7 || ino != 4242 || off != 1337 {
		t.Errorf("readCursor = %d,%d,%d,state=%d", dev, ino, off, st)
	}
	// a torn or hand-mangled cursor must read not-ok rather than half-parse
	// two fields is the OLD format: it must read not-ok rather than half-parse (P2)
	for _, junk := range []string{"", "garbage", "1", "1 2", "1 2 3 4", "a b c", "1 2 -5"} {
		if err := os.WriteFile(cursorPath(dir), []byte(junk), 0o644); err != nil {
			t.Fatal(err)
		}
		// CORRUPT, not ABSENT: a damaged record must not be mistaken for "never had one"
		if _, _, _, st := readCursor(dir); st != cursorCorrupt {
			t.Errorf("cursor %q read state %d, want cursorCorrupt", junk, st)
		}
	}
}

// TestResolveResumeTable walks the decision table. Each row is a state a real peer can
// actually be in, not a shape invented to exercise a branch.
func TestResolveResumeTable(t *testing.T) {
	t.Run("no cursor, never armed -> byte 0", func(t *testing.T) {
		dir, inbox := seedInbox(t, "a", "b")
		meta := writeMetaJSON(t, dir, "null")
		if got := resolveResume(inbox, meta); got != (resumePoint{offset: 0}) {
			t.Errorf("got %+v, want offset 0", got)
		}
	})

	// the migration rule: this is the state EVERY live peer is in at upgrade
	t.Run("no cursor, ever armed -> seek END", func(t *testing.T) {
		dir, inbox := seedInbox(t, "a", "b")
		meta := writeMetaJSON(t, dir, "4242")
		got := resolveResume(inbox, meta)
		if !got.seekEnd {
			t.Errorf("got %+v, want seekEnd — byte 0 would replay every peer's whole history at upgrade", got)
		}
	})

	t.Run("valid cursor -> resume at offset", func(t *testing.T) {
		dir, inbox := seedInbox(t, "a", "b", "c")
		meta := writeMetaJSON(t, dir, "4242")
		d, i := devInoOfPath(t, inbox)
		writeCursor(dir, d, i, 4)
		if got := resolveResume(inbox, meta); got != (resumePoint{offset: 4}) {
			t.Errorf("got %+v, want offset 4", got)
		}
	})

	// a rejoin does rm+recreate, so the old offset points into a file that is gone
	t.Run("cursor inode mismatch -> byte 0", func(t *testing.T) {
		dir, inbox := seedInbox(t, "a", "b", "c")
		meta := writeMetaJSON(t, dir, "4242")
		d, i := devInoOfPath(t, inbox)
		writeCursor(dir, d, i+1, 4)
		if got := resolveResume(inbox, meta); got != (resumePoint{offset: 0}) {
			t.Errorf("got %+v, want offset 0 for a stale inode", got)
		}
	})

	t.Run("cursor past EOF -> byte 0", func(t *testing.T) {
		dir, inbox := seedInbox(t, "a")
		meta := writeMetaJSON(t, dir, "4242")
		d, i := devInoOfPath(t, inbox)
		writeCursor(dir, d, i, 9999)
		if got := resolveResume(inbox, meta); got != (resumePoint{offset: 0}) {
			t.Errorf("got %+v, want offset 0 when the file shrank under us", got)
		}
	})

	t.Run("corrupt cursor -> byte 0, never seekEnd", func(t *testing.T) {
		dir, inbox := seedInbox(t, "a", "b")
		meta := writeMetaJSON(t, dir, "4242")
		if err := os.WriteFile(cursorPath(dir), []byte("nonsense"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := resolveResume(inbox, meta)
		// deliberately NOT the migration rule: a corrupt cursor means we cannot know,
		// and a full replay costs duplicates while seeking END costs silence.
		if got != (resumePoint{offset: 0}) {
			t.Errorf("got %+v, want offset 0", got)
		}
	})
}

// TestFollowerPersistsCursor: the follower records how far it read, so a later arm can
// resume there. Without this the cursor is a decision table with nothing to decide on.
func TestFollowerPersistsCursor(t *testing.T) {
	dir, inbox := seedInbox(t, `{"from":"p","to":"q","text":"one"}`)
	buf, _, stopJoin := startFollow(t, inbox, resumePoint{})
	defer stopJoin()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "one") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "one") {
		t.Fatal("follower never emitted the seeded frame")
	}
	// the cursor lands on the next idle tick after the drain
	for time.Now().Before(deadline) {
		if _, _, off, st := readCursor(dir); st == cursorValid && off > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	dev, ino, off, st := readCursor(dir)
	if st != cursorValid {
		t.Fatalf("follower wrote no valid cursor (state %d)", st)
	}
	fi, err := os.Stat(inbox)
	if err != nil {
		t.Fatal(err)
	}
	wantDev, wantIno := devInoOfPath(t, inbox)
	if dev != wantDev || ino != wantIno {
		t.Errorf("cursor dev/ino %d/%d != inbox %d/%d", dev, ino, wantDev, wantIno)
	}
	if off != fi.Size() {
		t.Errorf("cursor offset %d, want %d (everything read)", off, fi.Size())
	}
}

// TestCursorNeverPointsMidFrame is F2. consumed counts bytes pulled off the fd, which
// includes a partial line still buffered in pend. Persisting that points the next arm
// INTO a message: the writer completes the line, the resuming follower starts mid-frame,
// the head is lost and the tail surfaces as a raw fragment. Silent loss — the one
// outcome the cursor trades duplicates to avoid.
//
// The assertion is on the BOUNDARY, not on a byte count, so it stays true if framing
// changes: whatever is persisted must be a position the follower could legitimately
// resume from, i.e. immediately after a newline.
func TestCursorNeverPointsMidFrame(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	peer, inbox, _, id := armedPeer(t, "ch", "al")

	complete := `{"from":"p","to":"q","text":"whole"}` + "\n"
	partial := `{"from":"p","to":"q","text":"half` // no newline: still in pend
	if err := os.WriteFile(inbox, []byte(complete+partial), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, stopJoin := startFollowAs(t, inbox, resumePoint{}, id)
	defer stopJoin()

	boundary := int64(len(complete))
	waitFor(t, func() bool {
		_, _, off, st := readCursor(peer)
		return st == cursorValid && off > 0
	}, "the cursor to land")
	time.Sleep(200 * time.Millisecond) // let several idle ticks pass

	_, _, off, st := readCursor(peer)
	if st != cursorValid {
		t.Fatalf("cursor state %d", st)
	}
	if off > boundary {
		t.Errorf("cursor %d is %d bytes past the last complete frame (boundary %d) — "+
			"a re-arm would resume MID-FRAME and lose the head of that message",
			off, off-boundary, boundary)
	}
	// and the persisted position must actually be a frame boundary in the file
	if off > 0 {
		b, err := os.ReadFile(inbox)
		if err != nil {
			t.Fatal(err)
		}
		if b[off-1] != '\n' {
			t.Errorf("cursor %d does not sit just after a newline; byte before it is %q", off, b[off-1])
		}
	}
}

// TestUnreadableCursorIsNotAbsent is rider n1. Absent means "no cursor-aware binary has
// read this peer" and routes an ever-armed peer to seek END; unreadable means the
// position is unknown. Collapsing them makes an EACCES silently skip everything queued
// while the peer was away, which is the exact polarity the design forbids.
func TestUnreadableCursorIsNotAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable file is still readable")
	}
	dir := t.TempDir()
	writeCursor(dir, 1, 2, 3)
	if err := os.Chmod(cursorPath(dir), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cursorPath(dir), 0o644) })

	if _, _, _, st := readCursor(dir); st != cursorCorrupt {
		t.Errorf("unreadable cursor read state %d, want cursorCorrupt — ABSENT would send an "+
			"ever-armed peer to seek END and skip its backlog", st)
	}
	// and the decision table must then replay rather than migrate
	inbox := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(inbox, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := writeMetaJSON(t, dir, "4242") // ever armed
	if got := resolveResume(inbox, meta); got.seekEnd {
		t.Error("an unreadable cursor took the migration path; it must replay from 0")
	}
}
