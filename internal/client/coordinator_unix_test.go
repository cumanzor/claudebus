//go:build darwin || linux

package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFailedBindStaysUnread: settling is best-effort at join, so its failure has to
// leave a seat with no coordinator and a working registration.
func TestFailedBindStaysUnread(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory mode this depends on")
	}
	t.Setenv("CBUS_DIR", t.TempDir())
	spawnedChild(t, "dev", "kid")
	dir := filepath.Dir(coordinatorRecordPath("dev", "kid"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	sessionJoins(t, "sid-kid", "dev", "kid")
	wantCoordinator(t, "dev", "kid", "")
}
