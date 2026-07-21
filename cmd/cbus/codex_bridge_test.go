package main

import "testing"

// TestCodexBridgeArgErrors covers the verb's parse guards, all of which fail before any
// dial. A valid-args success path dials + blocks in the follower, so it is exercised by the
// client-level bridge tests and the real-codex smoke, not here.
func TestCodexBridgeArgErrors(t *testing.T) {
	cases := map[string][]string{
		"missing sock":     {"ch/al"},
		"missing target":   {"--sock", "/tmp/x.sock"},
		"remote target":    {"ch@host/al", "--sock", "/tmp/x.sock"},
		"extra positional": {"ch/al", "extra", "--sock", "/tmp/x.sock"},
		"sock no value":    {"ch/al", "--sock"},
		"thread no value":  {"ch/al", "--sock", "/tmp/x.sock", "--thread"},
	}
	for name, args := range cases {
		if rc := runCodexBridge(args); rc == 0 {
			t.Errorf("%s: expected non-zero exit, got 0", name)
		}
	}
}
