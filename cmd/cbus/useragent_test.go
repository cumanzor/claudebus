package main

import (
	"testing"

	"claudebus/internal/wire"
)

// the wire default is cbus/unstamped, so this fails if main's init stops stamping.
func TestUserAgentStampedFromVersion(t *testing.T) {
	if want := "cbus/" + version; wire.UserAgent != want {
		t.Fatalf("wire.UserAgent = %q, want %q", wire.UserAgent, want)
	}
}
