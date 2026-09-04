//go:build darwin || linux

package main

import (
	"strings"
	"testing"
)

// arrange/scatter/focus are phase-1 windows-excluded (unsupported_windows.go): the dispatch
// routes them to a refusal there, not to the usage/channel-resolution lines this asserts, so
// this dispatch check is unix-only. Windows coverage is the arrange/scatter/focus refusedVerbs
// rows in unsupported_windows_test.go. TestUsageAdvertisesLayoutVerbs (help text) and the
// TestParseLayoutArgs* tests stay cross-platform in layout_test.go.

// TestLayoutVerbsReachableThroughDispatch goes through the real CLI door: a verb
// defined but never added to the switch in run() is dead surface, reachable only by
// editing the source. Each verb is invoked with no arguments, where the only correct
// outcome is its own usage line — an unknown verb prints "unknown command" instead,
// which is exactly what this catches.
func TestLayoutVerbsReachableThroughDispatch(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	for verb, want := range map[string]string{
		"arrange": "usage: cbus arrange",
		"focus":   "usage: cbus focus",
		// scatter takes no required argument, so its no-arg path lands on channel
		// resolution instead of a usage line — still proof it dispatched.
		"scatter": "joined no channel",
	} {
		out := captureStderr(t, func() {
			if rc := run([]string{verb}); rc == 0 {
				t.Errorf("%s with no args should fail", verb)
			}
		})
		if strings.Contains(out, "unknown command") {
			t.Errorf("%s is not wired into run()'s switch: %q", verb, out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s should print %q, got %q", verb, want, out)
		}
	}
}
