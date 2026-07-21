package main

import "testing"

// TestCodexWrapArgErrors: the wrapper verb rejects a valueless flag before any launch. The
// success path spawns a real app-server + TUI, so it is validated by the documented smoke.
func TestCodexWrapArgErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"channel no value": {"--channel"},
		"alias no value":   {"--alias"},
	} {
		if rc := runCodexWrap(args); rc == 0 {
			t.Errorf("%s: expected non-zero exit", name)
		}
	}
}
