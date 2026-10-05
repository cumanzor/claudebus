package main

import (
	"testing"
	"time"
)

func TestParseGCDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := parseGCDuration(in); err != nil || got != want {
			t.Errorf("parseGCDuration(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"0d", "-1d", "d", "abc", "-2h", "0s"} {
		if _, err := parseGCDuration(bad); err == nil {
			t.Errorf("parseGCDuration(%q) accepted", bad)
		}
	}
}

func TestConnectionGCRefusesWithoutDryRun(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	var rc int
	out := captureStderr(t, func() { rc = runConnectionGC(nil) })
	if rc == 0 || out == "" {
		t.Fatalf("gc without --dry-run must refuse, rc=%d out=%q", rc, out)
	}
}
