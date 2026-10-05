package main

import (
	"strings"
	"testing"
	"time"

	"claudebus/internal/client"
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

func TestRenderGCResultsNamesWhatWasLeft(t *testing.T) {
	results := []client.GCResult{
		{GCRecord: client.GCRecord{Target: "dev/old", Class: client.GCCollect, Reason: "detached by leave or unregister", Unread: 2}, Collected: true, Archive: "/s/.daemon/connections/.archive/2026-10/abc"},
		{GCRecord: client.GCRecord{Target: "dev/busy", Class: client.GCCollect}, Left: "now live: its consumer process is running"},
		{GCRecord: client.GCRecord{Target: "dev/p", Class: client.GCPending}},
	}
	out := renderGCResults(client.GCPass{Records: results, OrphanTokens: 3}, 14*24*time.Hour)
	for _, want := range []string{"collected 1 of 3", "1 pending", "dev/old", "2 unread exported", "left      dev/busy", "now live", "reconcile or abandon", "removed 3 session tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
