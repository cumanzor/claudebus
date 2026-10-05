package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const storeGuardMarker = "CBUS_TEST_STORE_GUARDED"

func TestMain(m *testing.M) {
	// Tests set identity deliberately. A suite run from Codex must not inherit the
	// real session as a fallback in cases exercising sessionless behavior.
	_ = os.Unsetenv("CODEX_THREAD_ID")
	// Point CBUS_DIR at an empty directory for the run, so a write that escapes
	// its test lands there instead of a real store, and fail on anything that
	// did. A helper process re-run from a test keeps the CBUS_DIR it inherits.
	if os.Getenv(storeGuardMarker) != "" {
		os.Exit(m.Run())
	}
	sandbox, err := os.MkdirTemp("", "cbus-test-store-")
	if err == nil {
		err = os.Setenv("CBUS_DIR", sandbox)
	}
	if err == nil {
		err = os.Setenv(storeGuardMarker, "1")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "store guard:", err)
		os.Exit(1)
	}
	code := m.Run()
	var leaked []string
	_ = filepath.WalkDir(sandbox, func(path string, e fs.DirEntry, err error) error {
		if err == nil && path != sandbox {
			rel, _ := filepath.Rel(sandbox, path)
			leaked = append(leaked, rel)
		}
		return nil
	})
	_ = os.RemoveAll(sandbox)
	if len(leaked) > 0 {
		fmt.Fprintf(os.Stderr, "STORE GUARD: tests wrote %d path(s) outside their own store, e.g. %v\n", len(leaked), leaked[:min(len(leaked), 5)])
		code = 1
	}
	os.Exit(code)
}
