package client

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// testDaemon is a daemon whose delivery and relay workers are stopped and
// waited for before the test's environment is restored: a worker still running
// afterwards would resolve CBUS_DIR to whatever the process started with.
func testDaemon(t testing.TB) *busDaemon {
	t.Helper()
	d := newBusDaemon()
	t.Cleanup(func() {
		d.cancel()
		d.workers.Wait()
		d.relayWorkers.Wait()
	})
	return d
}

const storeGuardMarker = "CBUS_TEST_STORE_GUARDED"

// guardStore points CBUS_DIR at an empty directory for the whole run, so a
// write that escapes its test lands there instead of in a real store, and
// reports anything that did.
func guardStore() (finish func() []string, err error) {
	// a helper process re-run from a test inherits that test's CBUS_DIR and
	// must keep it; the parent's guard already covers the run
	if os.Getenv(storeGuardMarker) != "" {
		return func() []string { return nil }, nil
	}
	if err := os.Setenv(storeGuardMarker, "1"); err != nil {
		return nil, err
	}
	sandbox, err := os.MkdirTemp("", "cbus-test-store-")
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("CBUS_DIR", sandbox); err != nil {
		return nil, err
	}
	return func() []string {
		var leaked []string
		_ = filepath.WalkDir(sandbox, func(path string, e fs.DirEntry, err error) error {
			if err == nil && path != sandbox {
				rel, _ := filepath.Rel(sandbox, path)
				leaked = append(leaked, rel)
			}
			return nil
		})
		_ = os.RemoveAll(sandbox)
		return leaked
	}, nil
}

func reportStoreLeaks(leaked []string) {
	if len(leaked) > 0 {
		fmt.Fprintf(os.Stderr, "STORE GUARD: tests wrote %d path(s) outside their own store, e.g. %v\n", len(leaked), leaked[:min(len(leaked), 5)])
	}
}
