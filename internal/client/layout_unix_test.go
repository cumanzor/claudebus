//go:build darwin || linux

package client

import (
	"strings"
	"testing"
)

// These pin PeerPane, the tmux/tty pane resolver that is unix-only (it reaches a peer's
// controlling tty via ttyOf); they moved here with PeerPane. The selfPane tests, the
// parser/planner/pane-table tests and the two tmuxRun fake-run tests stay cross-platform
// in layout_test.go. Windows coverage of the layout runtime is the loud-fallback assertion
// in layout_windows_test.go plus the CLI refusal in unsupported_windows_test.go.

// TestPeerPaneNeverArmedIsNotReportedAsDead: a fresh join writes ownerPid AND
// listenerPid null, and both are only stamped when a listener arms. Calling that
// "not running" is a false statement about a live session, and it sends the user
// hunting a process that is fine — which is exactly what happened the first time
// arrange was pointed at a real orchestrator.
func TestPeerPaneNeverArmedIsNotReportedAsDead(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	seedPeer(t, root, "ch", "orchestrator", "sid-1")

	_, err := PeerPane("ch", "orchestrator", map[string]string{})
	if err == nil {
		t.Fatal("a pidless peer should not resolve")
	}
	if !strings.Contains(err.Error(), "never armed") {
		t.Errorf("error should say it never armed, got %q", err)
	}
	if strings.Contains(err.Error(), "is not running") {
		t.Errorf("a joined-but-unarmed peer is NOT dead; error must not claim it: %q", err)
	}
}

// TestPeerPaneResolvesSelfBeforeMeta pins the LAYER, which is where the first attempt
// at this fix went wrong: the short-circuit lived in ResolvePeerPanes, so arrange got
// it and focus and scatter did not, and scatter reported the very session running it
// as unlocatable. Every verb reaches PeerPane; only one reaches the wrapper.
func TestPeerPaneResolvesSelfBeforeMeta(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CBUS_SESSION_ID", "sid-self")
	t.Setenv("TMUX_PANE", "%7")
	seedPeer(t, root, "ch", "orchestrator", "sid-self") // unarmed: the meta has no pid

	pane, err := PeerPane("ch", "orchestrator", map[string]string{"/dev/ttys001": "%7"})
	if err != nil {
		t.Fatalf("PeerPane should resolve this session from $TMUX_PANE: %v", err)
	}
	if pane != "%7" {
		t.Errorf("PeerPane = %q, want %%7", pane)
	}
}
