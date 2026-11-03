package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifierHooksExitZeroAndPrintNothing: stdout is parsed for a decision, a retry on
// one hook and a blocked stop on the other, and neither must ever ask for one.
func TestClassifierHooksExitZeroAndPrintNothing(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CBUS_HOST", "bad host label")
	for name, tc := range map[string]struct{ verb, stdin string }{
		"denied garbage":    {"hook-denied", "not json"},
		"denied empty":      {"hook-denied", ""},
		"denied valid":      {"hook-denied", `{"session_id":"sid-x","tool_name":"Bash","tool_input":{"command":"ls"},"reason":"r"}`},
		"regressed garbage": {"hook-regressed", "not json"},
		"regressed empty":   {"hook-regressed", ""},
		"regressed valid":   {"hook-regressed", `{"session_id":"sid-x","transcript_path":"/nonexistent/sid-x.jsonl"}`},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "in")
			if err := os.WriteFile(path, []byte(tc.stdin), 0o600); err != nil {
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
			out := captureStdout(t, func() { rc = run([]string{tc.verb}) })
			if rc != 0 || strings.TrimSpace(out) != "" {
				t.Fatalf("rc=%d stdout=%q", rc, out)
			}
		})
	}
}
