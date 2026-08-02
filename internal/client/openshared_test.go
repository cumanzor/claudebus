package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenSharedReadSurvivesRemoval is the seam's whole contract in one case, and the
// deterministic version of the share-flag test D59 asked for: a file opened through
// openSharedRead can be REMOVED while the handle is still open, and the holder keeps
// reading what it already had.
//
// That is ordinary unix behaviour and the reason this reads as trivial on darwin and
// linux. On windows it is the entire milestone: os.Open omits FILE_SHARE_DELETE, so a
// handle it returns blocks the remove outright, and the follower holds exactly such a
// handle for the life of an arm.
//
// Deterministic because the TEST owns the handle's lifetime. The M4 version could only
// probe in a loop and hope to overlap a call that closed its own handle; here the
// removal happens at a moment the test chooses, with the handle provably open.
//
// Pre-registered mutation: narrow the windows mask in openshared_windows.go back to
// FILE_SHARE_READ|FILE_SHARE_WRITE and the os.Remove below must fail on windows. If it
// still passes, this case is not measuring the share set.
func TestOpenSharedReadSurvivesRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openSharedRead(path)
	if err != nil {
		t.Fatalf("openSharedRead: %v", err)
	}
	defer f.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("a rejoin could not remove the inbox while the follower held it open: %v", err)
	}

	// the other half of the contract: the holder is not cut off by the unlink.
	buf := make([]byte, 8)
	n, err := f.Read(buf)
	if err != nil || string(buf[:n]) != "one\ntwo\n" {
		t.Errorf("holder read %q (%v) after the unlink, want the original contents", buf[:n], err)
	}
}
