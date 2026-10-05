package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const gcThread = "01a0b0c6-ffab-7fd0-9319-1ab0275adc21"

var gcNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func gcTestProbe(alive map[int]string, mtimes map[string]time.Time) gcProbe {
	return gcProbe{
		now:   gcNow,
		alive: func(pid int, start string) bool { return alive[pid] == start && start != "" },
		mtime: func(path string) (time.Time, bool) { t, ok := mtimes[path]; return t, ok },
	}
}

func TestClassifyGCOrder(t *testing.T) {
	limit := 14 * 24 * time.Hour
	transcript := "/t/session.jsonl"
	claude := func() *ClaudeConnectionConfig {
		return &ClaudeConnectionConfig{Binding: ClaudeConnectBinding{TranscriptPath: transcript, Endpoint: claudeEndpoint{PID: 42, StartToken: "s42"}}}
	}
	cases := []struct {
		name  string
		c     ConnectionState
		alive map[int]string
		idle  time.Duration
		want  string
	}{
		{"pending beats a running consumer", ConnectionState{State: "socket-ready", Claude: claude(), Pending: &queueAttempt{ClientID: "a1"}}, map[int]string{42: "s42"}, time.Hour, GCPending},
		{"running consumer is live even when old", ConnectionState{State: "socket-ready", Claude: claude()}, map[int]string{42: "s42"}, 90 * 24 * time.Hour, GCLive},
		{"a reused pid is not the consumer", ConnectionState{State: "socket-ready", Claude: claude()}, map[int]string{42: "other"}, time.Hour, GCKeep},
		{"a stale online observation is not live", ConnectionState{State: "detached", Consumer: &consumerObservation{State: "online", PID: 7, StartToken: "s7"}}, nil, time.Hour, GCCollect},
		{"detached is collectable at once", ConnectionState{State: "detached", Claude: claude()}, nil, time.Hour, GCCollect},
		{"inactive past the limit", ConnectionState{State: "error", Claude: claude()}, nil, 15 * 24 * time.Hour, GCCollect},
		{"inactive under the limit", ConnectionState{State: "disconnected", Claude: claude()}, nil, 3 * 24 * time.Hour, GCKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.ID, tc.c.Channel, tc.c.Alias, tc.c.Harness = "abc", "ch", "al", daemonHarnessClaude
			p := gcTestProbe(tc.alive, map[string]time.Time{transcript: gcNow.Add(-tc.idle)})
			if got := classifyGC(&tc.c, "/nonexistent", limit, p); got.Class != tc.want {
				t.Fatalf("class %s (%s), want %s", got.Class, got.Reason, tc.want)
			}
		})
	}
}

func TestClassifyGCPendingNamesItsCommands(t *testing.T) {
	c := &ConnectionState{ID: "abc", Channel: "ch", Alias: "al", Relay: &RelayConfig{Host: "server"}, Pending: &queueAttempt{ClientID: "cbus-x-1"}}
	r := classifyGC(c, "/nonexistent", time.Hour, gcTestProbe(nil, nil))
	want := "cbus connection reconcile ch@server/al  (then, if still pending: cbus connection abandon ch@server/al --pending cbus-x-1 --reason <text>)"
	if r.Next != want {
		t.Fatalf("next = %q", r.Next)
	}
}

func gcWriteRecord(t *testing.T, daemonRoot string, c ConnectionState) {
	t.Helper()
	dir := filepath.Join(daemonRoot, "connections")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(dir, c.ID+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func gcWriteInbox(t *testing.T, dir, owner string, lines ...string) (dev, ino uint64, size int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if owner != "" {
		b, _ := json.Marshal(map[string]string{"connectionId": owner})
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	inbox := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(inbox, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	dev, ino, size, ok := fileIdentity(inbox)
	if !ok {
		t.Fatal("inbox identity unavailable")
	}
	return dev, ino, size
}

func TestPlanConnectionGCCountsOnlyThisRecordsUnreadMail(t *testing.T) {
	bus := t.TempDir()
	daemon := filepath.Join(bus, ".daemon")
	read := `{"from":"ch/x","kind":"msg","text":"already delivered"}`
	dev, ino, _ := gcWriteInbox(t, filepath.Join(bus, "ch", "mine"), "c1",
		read, `{"from":"ch/x","text":"unread one"}`, `{"kind":"presence","event":"join"}`, `{"from":"ch/y","text":"unread two"}`)
	gcWriteRecord(t, daemon, ConnectionState{ID: "c1", Channel: "ch", Alias: "mine", ThreadID: gcThread, State: "detached", Dev: dev, Ino: ino, Offset: int64(len(read) + 1)})
	gcWriteInbox(t, filepath.Join(bus, "ch", "reused"), "c3", `{"text":"for the new owner"}`)
	gcWriteRecord(t, daemon, ConnectionState{ID: "c2", Channel: "ch", Alias: "reused", ThreadID: gcThread, State: "detached"})
	dev, ino, _ = gcWriteInbox(t, filepath.Join(bus, "ch", "swapped"), "c4", `{"text":"x"}`)
	gcWriteRecord(t, daemon, ConnectionState{ID: "c4", Channel: "ch", Alias: "swapped", ThreadID: gcThread, State: "detached", Dev: dev, Ino: ino + 1})
	if err := os.WriteFile(filepath.Join(daemon, "connections", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := planConnectionGC(daemon, bus, time.Hour, gcTestProbe(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]GCRecord{}
	for _, r := range plan.Records {
		got[r.ID] = r
	}
	if r := got["c1"]; r.Unread != 2 || r.InboxNote != "" {
		t.Fatalf("own inbox: unread=%d note=%q, want 2 non-presence lines past the offset", r.Unread, r.InboxNote)
	}
	if r := got["c2"]; r.Unread != 0 || r.InboxNote != "inbox now belongs to connection c3" {
		t.Fatalf("reused folder: unread=%d note=%q", r.Unread, r.InboxNote)
	}
	if r := got["c4"]; r.Unread != 0 || r.InboxNote != "inbox replaced since this record; unread unknown" {
		t.Fatalf("replaced inbox: unread=%d note=%q", r.Unread, r.InboxNote)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0] != "broken.json" {
		t.Fatalf("skipped = %v", plan.Skipped)
	}
}

func TestPlanConnectionGCWithoutRecords(t *testing.T) {
	plan, err := planConnectionGC(filepath.Join(t.TempDir(), ".daemon"), t.TempDir(), time.Hour, gcTestProbe(nil, nil))
	if err != nil || len(plan.Records) != 0 {
		t.Fatalf("empty store: %+v, %v", plan, err)
	}
}
