//go:build darwin || linux

// branch is a phase-1 windows-excluded verb (unsupported_windows.go): runBranch refuses
// ahead of the --role check, so this refusal proof only reaches the role guard on unix.
// Windows coverage is the branch refusal row in unsupported_windows_test.go.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"claudebus/internal/client"
)

// TestBranchRefusesRole: roles are spawn-only; branch dies before any side
// effect (a fork inherits its parent's intent).
func TestBranchRefusesRole(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	for _, args := range [][]string{
		{"tab", "rolechan", "--role", "coder"},
		{"--role", "coder"},
		{"tab", "rolechan", "--role"},
	} {
		if rc := runBranch(args); rc == 0 {
			t.Fatalf("runBranch(%v) = 0, want refusal", args)
		}
	}
	if _, err := os.Stat(filepath.Join(client.CBUSDir(), "rolechan")); err == nil {
		t.Fatal("refusal must precede any reservation")
	}
}
