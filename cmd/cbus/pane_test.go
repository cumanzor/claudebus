package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
)

// TestUsageAdvertisesPane: the help text is the only place a user learns a target
// exists. A target threaded through the client but missing from usage is dead
// surface — reachable only by guessing.
func TestUsageAdvertisesPane(t *testing.T) {
	out := captureStdout(t, func() { run([]string{"--help"}) })
	if !strings.Contains(out, "window|tab|tmux|pane") {
		t.Errorf("help must list pane in the target set:\n%s", out)
	}
	// listed for BOTH verbs, not just the one that happened to get edited
	if n := strings.Count(out, "window|tab|tmux|pane"); n < 2 {
		t.Errorf("target set appears %d time(s), want it on both branch and spawn:\n%s", n, out)
	}
	// pane is non-obvious (it splits the caller, and refuses when it cannot find
	// one), so the help owes the reader that much
	if !strings.Contains(out, "pane splits") {
		t.Errorf("help should say what pane splits:\n%s", out)
	}
}

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

// TestPaneIsNotAVerb: pane is a TARGET, so it must keep falling through to the
// unknown-command path rather than becoming a bare subcommand. This is also what
// keeps it out of the update-check exclusion switch, which keys on args[0] only.
func TestPaneIsNotAVerb(t *testing.T) {
	out := captureStderr(t, func() {
		if rc := run([]string{"pane"}); rc != 1 {
			t.Errorf("bare `cbus pane` = %d, want 1", rc)
		}
	})
	if !strings.Contains(out, "unknown command 'pane'") {
		t.Errorf("stderr = %q, want the unknown-command form", out)
	}
}

// TestUsageAdvertisesSplitField: the split direction is envelope-only — there is no
// flag that reveals it — so the help is the ONLY place a user can learn it exists.
// An undocumented envelope field is unreachable surface.
func TestUsageAdvertisesSplitField(t *testing.T) {
	out := captureStdout(t, func() { run([]string{"--help"}) })
	if !strings.Contains(out, `"split"`) {
		t.Errorf("help must name the split field:\n%s", out)
	}
	for _, want := range []string{"right", "down"} {
		if !strings.Contains(out, want) {
			t.Errorf("help must list the %q direction:\n%s", want, out)
		}
	}
	// the run-level consequence is the surprising half: one peer's direction changes
	// how every pane in the run is laid out
	if !strings.Contains(out, "whole run") {
		t.Errorf("help should say a declared direction applies run-wide:\n%s", out)
	}
}
