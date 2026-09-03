package main

import (
	"strings"
	"testing"
)

// TestUsageAdvertisesClose: a verb threaded through the CLI but absent from the help
// is dead surface — reachable only by guessing. Same rule the pane target is held to.
// Cross-platform: close stays registered on windows (it refuses rather than vanishing,
// unsupported_windows.go), so --help must advertise it on every build. The close
// behavioral matrix is unix-only and lives in close_unix_test.go.
func TestUsageAdvertisesClose(t *testing.T) {
	out := captureStdout(t, func() { run([]string{"--help"}) })
	if !strings.Contains(out, "cbus close") {
		t.Errorf("help must advertise close:\n%s", out)
	}
	// the two non-obvious halves: it is local-only, and it does NOT prune
	if !strings.Contains(out, "--force") {
		t.Errorf("help must document --force:\n%s", out)
	}
	if !strings.Contains(out, "local only") && !strings.Contains(out, "local-only") {
		t.Errorf("help must say close is local-only:\n%s", out)
	}
}
