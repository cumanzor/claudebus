package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileIdentityDistinguishesFiles is the property the cursor and the rotation check
// both rest on: two files are never the same identity, and one file is always itself.
func TestFileIdentityDistinguishesFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	devA, inoA, sizeA, ok := fileIdentity(a)
	if !ok {
		t.Fatal("no identity for a")
	}
	devB, inoB, _, ok := fileIdentity(b)
	if !ok {
		t.Fatal("no identity for b")
	}
	if devA == devB && inoA == inoB {
		t.Error("two files on one volume share an identity")
	}
	if sizeA != int64(len("hello world")) {
		t.Errorf("size = %d, want %d", sizeA, len("hello world"))
	}
	if _, _, _, ok := fileIdentity(filepath.Join(dir, "absent")); ok {
		t.Error("a missing file reported an identity")
	}
}

// TestFileIdentitySurvivesReopenAndAppend is the D3 failure mode stated as a property.
// If identity changed across a reopen, resolveResume would reject its own valid cursor
// on every arm and replay the whole inbox each time — green tests, silent replay storm.
func TestFileIdentitySurvivesReopenAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dev1, ino1, size1, ok := fileIdentity(path)
	if !ok {
		t.Fatal("no identity on the first read")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("two\n"); err != nil {
		t.Fatal(err)
	}
	dev2, ino2, size2, ok := fileIdentityOf(f)
	if !ok {
		t.Fatal("no identity from the open handle")
	}
	f.Close()

	if dev1 != dev2 || ino1 != ino2 {
		t.Errorf("identity changed across reopen+append: (%d,%d) then (%d,%d)", dev1, ino1, dev2, ino2)
	}
	if size2 <= size1 {
		t.Errorf("size did not grow after an append: %d then %d", size1, size2)
	}

	// a rm+recreate is a DIFFERENT file and must read as rotated, or a rejoin would
	// resume at a stale offset in a file that no longer exists.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dev3, ino3, size3, ok := fileIdentity(path)
	if !ok {
		t.Fatal("no identity after recreate")
	}
	if !rotated(dev1, ino1, size1, dev3, ino3, size3) {
		t.Error("rm+recreate did not read as rotated")
	}
}

// TestFileIdentityProbeDoesNotLockOutWriters is the D38 case, driven through the real
// door. fileIdentity opens with a hand-rolled CreateFile naming all three share flags,
// so a peer appending to the inbox and a rejoin removing it both keep working while the
// probe runs. Go's os.Open names only FILE_SHARE_READ|FILE_SHARE_WRITE, so a probe built
// on it would block deletion and turn an identity read into a lock.
//
// The previous version of this test opened its own probe with os.Open and then asserted
// the delete. That measured the STDLIB's share mode, not ours: it could not pass however
// fileIdentity was written, and it could not fail if fileIdentity were wrong. The subject
// has to be the production open.
//
// Two halves with different strengths, stated rather than blurred:
//   - the no-leak half is deterministic. fileIdentity closes its handle before returning,
//     so a sequential remove afterwards must always succeed.
//   - the share-flag half is a RACE WINDOW. The handle only exists inside the call, so
//     the probe has to be running concurrently for a missing flag to bite. It is caught
//     with high probability across the loop below, not with certainty on any one pass.
//     A deterministic version needs fileIdentity to hand back its open handle, which is a
//     production change and not this batch's to make.
func TestFileIdentityProbeDoesNotLockOutWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := fileIdentity(path); !ok {
		t.Fatal("no identity from the production probe")
	}

	w, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("a sender could not open the inbox after an identity probe: %v", err)
	}
	if _, err := w.WriteString("two\n"); err != nil {
		t.Errorf("a sender could not append after an identity probe: %v", err)
	}
	w.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("the probe left a handle behind — a rejoin could not remove the inbox: %v", err)
	}

	// the share-flag half: probe continuously while a rejoin rm+recreates underneath.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				fileIdentity(path) // return ignored: a vanished file is expected mid-loop
			}
		}
	}()
	defer func() { close(stop); <-done }()

	for i := 0; i < 50; i++ {
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("recreate %d: %v", i, err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("rejoin %d could not remove the inbox while the identity probe ran: %v", i, err)
		}
	}
}

// TestFileIdentityRoundTripsThroughTheCursor: the identity has to survive the on-disk
// cursor format, which stores both halves as decimal uint64. A windows file index is
// two 32-bit words composed into one, so this is the case where a composition mistake
// would show up as a cursor that never matches.
func TestFileIdentityRoundTripsThroughTheCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dev, ino, size, ok := fileIdentity(path)
	if !ok {
		t.Fatal("no identity")
	}
	writeCursor(dir, dev, ino, size)
	gotDev, gotIno, gotOff, state := readCursor(dir)
	if state != cursorValid {
		t.Fatalf("cursor did not read back valid: state %v", state)
	}
	if gotDev != dev || gotIno != ino || gotOff != size {
		t.Errorf("cursor round trip: wrote (%d,%d,%d) read (%d,%d,%d)", dev, ino, size, gotDev, gotIno, gotOff)
	}
}
