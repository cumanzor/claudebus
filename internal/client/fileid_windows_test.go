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

// TestFileIdentityProbeDoesNotLockOutWriters guards the trap the os.Open choice exists
// for: a CreateFile without the full share set would make this probe a sharing
// violation for any peer appending to the same inbox. The probe would work and the bus
// would break, so the probe must hold the file open WHILE a writer appends.
func TestFileIdentityProbeDoesNotLockOutWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if _, _, _, ok := fileIdentityOf(probe); !ok {
		t.Fatal("no identity from the probe handle")
	}

	w, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("a sender could not open the inbox while an identity probe held it: %v", err)
	}
	if _, err := w.WriteString("two\n"); err != nil {
		t.Errorf("a sender could not append while an identity probe held the file: %v", err)
	}
	w.Close()

	if err := os.Remove(path); err != nil {
		t.Errorf("a rejoin could not remove the inbox while an identity probe held it: %v", err)
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
