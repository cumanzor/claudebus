package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestCaptureStdoutDrainsLargeOutput pins the concurrent drain: a callback that writes
// 1 MiB must be captured whole and return in milliseconds. Against a helper that only
// starts reading after the callback returns, the callback blocks once the pipe buffer
// fills and this wedges, killable under go test -timeout. The trigger is output SIZE,
// so it reproduces on darwin and linux, not only on the 4KB windows pipe.
func TestCaptureStdoutDrainsLargeOutput(t *testing.T) {
	const n = 1 << 20
	out := captureStdout(t, func() { fmt.Print(strings.Repeat("x", n)) })
	if len(out) != n {
		t.Fatalf("captured %d bytes, want %d", len(out), n)
	}
}

// TestCaptureStderrDrainsLargeOutput is the same guard for captureStderr.
func TestCaptureStderrDrainsLargeOutput(t *testing.T) {
	const n = 1 << 20
	out := captureStderr(t, func() { fmt.Fprint(os.Stderr, strings.Repeat("x", n)) })
	if len(out) != n {
		t.Fatalf("captured %d bytes, want %d", len(out), n)
	}
}
