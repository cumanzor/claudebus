package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookDeniedExitsZeroAndPrintsNothing: stdout is parsed for a retry decision, and a
// denial must never ask for one.
func TestHookDeniedExitsZeroAndPrintsNothing(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CBUS_HOST", "bad host label")
	for name, stdin := range map[string]string{
		"garbage": "not json",
		"empty":   "",
		"valid":   `{"session_id":"sid-x","tool_name":"Bash","tool_input":{"command":"ls"},"reason":"r"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "in")
			if err := os.WriteFile(path, []byte(stdin), 0o600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			old := os.Stdin
			os.Stdin = f
			defer func() { os.Stdin = old }()
			rc := -1
			out := captureStdout(t, func() { rc = run([]string{"hook-denied"}) })
			if rc != 0 || strings.TrimSpace(out) != "" {
				t.Fatalf("rc=%d stdout=%q", rc, out)
			}
		})
	}
}
