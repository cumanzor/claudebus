package client

import "testing"

func TestManagedCodexProcess(t *testing.T) {
	for _, argv := range []string{
		"codex app-server --listen unix:// --analytics-default-enabled --managed-daemon",
		"/opt/bin/codex app-server --managed-daemon --listen=unix://",
	} {
		if !managedCodexProcess(argv) {
			t.Errorf("rejected managed launch %q", argv)
		}
	}
	for _, argv := range []string{
		"", "other app-server --listen unix:// --managed-daemon",
		"codex app-server", "codex app-server --listen unix://",
		"codex app-server --managed-daemon", "codex app-server --managed-daemon --listen",
		"codex app-server --listen ws://127.0.0.1:1234 --managed-daemon",
		"codex app-server --listen unix:// --managed-daemon=false",
		"codex app-server --listen unix:// --managed-daemon --managed-daemon",
		"codex app-server --listen unix:// --managed-daemon --listen=unix://",
		"codex app-server --listen unix:// --managed-daemon --future-flag",
		"codex app-server daemon pid-update-loop",
		"codex app-server --listen unix:// --managed-daemon daemon pid-update-loop",
		"codex exec app-server --listen unix:// --managed-daemon",
		"codex explain app-server --listen unix:// --managed-daemon",
		"codex --profile app-server exec --listen unix:// --managed-daemon",
	} {
		if managedCodexProcess(argv) {
			t.Errorf("accepted unrelated launch %q", argv)
		}
	}
}
