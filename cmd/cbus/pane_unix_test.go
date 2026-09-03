//go:build darwin || linux

// branch and spawn are phase-1 windows-excluded verbs (unsupported_windows.go): runBranch
// and runSpawn refuse ahead of usage/target validation, so these per-verb usage-string and
// unknown-target checks only reach the target set on unix. Windows coverage is the branch
// and spawn refusal rows in unsupported_windows_test.go. The pane help-text tests
// (TestUsageAdvertisesPane, TestUsageAdvertisesSplitField, TestPaneIsNotAVerb) stay
// cross-platform in pane_test.go.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
)

// TestBranchSpawnUsageStringsListPane pins the per-verb `use` strings printed on a
// usage error — the other place the target set is spelled out, and the one a user
// hits when they got the invocation wrong.
func TestBranchSpawnUsageStringsListPane(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	for verb, fn := range map[string]func([]string) int{
		"branch": runBranch,
		"spawn":  runSpawn,
	} {
		// too many positionals -> the usage line, which carries the target set
		out := captureStderr(t, func() {
			if rc := fn([]string{"tab", "ch", "extra", "more"}); rc == 0 {
				t.Errorf("%s with extra args should fail", verb)
			}
		})
		if !strings.Contains(out, "window|tab|tmux|pane") {
			t.Errorf("%s usage line must list pane: %q", verb, out)
		}
	}
}

// TestCLIRejectsUnknownTargetWithPaneInMessage drives the real CLI entry: an
// unknown target is refused with the full set, and the refusal lands BEFORE any
// channel directory or reservation is created.
func TestCLIRejectsUnknownTargetWithPaneInMessage(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-cli-target")
	for _, args := range [][]string{
		{"branch", "popup", "clichan"},
		{"spawn", "popup", "clichan"},
	} {
		out := captureStderr(t, func() {
			if rc := run(args); rc != 1 {
				t.Errorf("%v = %d, want 1", args, rc)
			}
		})
		if !strings.Contains(out, "window|tab|tmux|pane") {
			t.Errorf("%v stderr = %q, want the target set including pane", args, out)
		}
	}
	if _, err := os.Stat(filepath.Join(client.CBUSDir(), "clichan")); err == nil {
		t.Error("a rejected target must not leave a channel dir behind")
	}
}
