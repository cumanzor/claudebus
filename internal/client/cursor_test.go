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
