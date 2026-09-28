package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInboxEpochWindowsKeepsDeviceStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openSharedRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dev, ino, _, ok := fileIdentityOf(f)
	if !ok {
		t.Fatal("identify inbox")
	}
	if _, _, ok := inboxEpochDecision(&ConnectionState{Dev: dev + 1, Ino: ino}, f, 0); ok {
		t.Fatal("a changed volume serial was accepted")
	}
}
