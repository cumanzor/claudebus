package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M6.1. `register` (a v1 alias for `join global`) and `peers` (a v1 alias for `list`)
// are gone. Both were undocumented — neither appeared in the usage text and no test
// invoked either — so the drop has no doc half and no pin to retire.
//
// Asserted through the CLI door, because what is being deleted IS a dispatch entry:
// calling runJoin or runList directly could never tell whether the verb still reaches
// them.
func TestDeprecatedVerbsAreUnknownCommands(t *testing.T) {
	for _, verb := range []string{"register", "peers"} {
		t.Run(verb, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CBUS_DIR", root)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-drop")

			var rc int
			errOut := captureStderr(t, func() {
				_ = captureStdout(t, func() { rc = run([]string{verb}) })
			})
			if rc != 1 {
				t.Errorf("rc = %d, want 1", rc)
			}
			// the frozen unknown-verb line, quoting included
			want := "cbus: unknown command '" + verb + "' (cbus --help)"
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr = %q, want %q", errOut, want)
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Errorf("%s touched the store: %v", verb, entries)
			}
		})
	}
}

// register used to mean `join global`, so the drop has to leave no path that still
// creates that channel — the failure mode is a verb that errors while having already
// done its work.
func TestRegisterNoLongerCreatesTheGlobalChannel(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-drop-global")

	captureStderr(t, func() {
		_ = captureStdout(t, func() { run([]string{"register", "me"}) })
	})
	if _, err := os.Stat(filepath.Join(root, "global")); err == nil {
		t.Error("register still created the global channel")
	}
}

// The verbs they aliased must be untouched: this milestone drops two dispatch entries,
// not two features.
func TestListAndJoinSurviveTheDrop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-drop-survive")

	if rc := captureRC(t, func() int { return run([]string{"join", "global", "me"}) }); rc != 0 {
		t.Fatalf("join rc = %d", rc)
	}
	out := captureStdout(t, func() {
		if rc := run([]string{"list"}); rc != 0 {
			t.Errorf("list rc = %d", rc)
		}
	})
	if !strings.Contains(out, "global/me") {
		t.Errorf("list output = %q", out)
	}
}
