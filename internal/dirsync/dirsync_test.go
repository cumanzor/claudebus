package dirsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncAcceptsAFreshDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Sync(dir); err != nil {
		t.Fatalf("Sync(%s): %v", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := SyncRoot(root); err != nil {
		t.Fatalf("SyncRoot(%s): %v", dir, err)
	}
}
