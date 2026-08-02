package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenSharedReadRefusesADirectory pins the one place this seam diverges from unix,
// where os.Open on a directory succeeds. CreateFile cannot open a directory without
// FILE_FLAG_BACKUP_SEMANTICS, and D64 ruled that flag OUT: no caller opens a directory
// through here, and granting it would widen the seam for no live consumer.
//
// What the ruling actually buys is the shape of the failure. A seam that quietly
// answered for a directory would hand its caller a false negative — fileIdentity's ok
// return is exactly that shape, and a directory reading "no identity" is
// indistinguishable from a file that vanished. An error cannot be mistaken for either.
//
// It is pinned rather than commented because the constraint was carried for a week as
// "harmless, nobody exercises it", and this milestone is the one that routes more opens
// onto the seam. Harmless-because-unexercised stops being true the first time someone
// adds a caller, and a test is what tells them.
func TestOpenSharedReadRefusesADirectory(t *testing.T) {
	dir := t.TempDir()
	f, err := openSharedRead(dir)
	if err == nil {
		f.Close()
		t.Fatal("openSharedRead opened a DIRECTORY; the seam is file-only by ruling, and a caller that gets a handle here would read identity fields that mean nothing")
	}
	// a real error, not a nil file with a nil error
	if f != nil {
		t.Errorf("refused with a non-nil file: %v", f)
	}
	// and the same path as a FILE must still open, or the case above proves nothing:
	// a seam that refused everything would pass it.
	path := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := openSharedRead(path)
	if err != nil {
		t.Fatalf("openSharedRead refused a FILE too, so the directory case discriminates nothing: %v", err)
	}
	g.Close()
}
