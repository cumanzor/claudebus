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
	limits := GCLimits{Grace: 15 * time.Minute, Inactive: 14 * 24 * time.Hour}
	transcript := "/t/session.jsonl"
	claude := func() *ClaudeConnectionConfig {
		return &ClaudeConnectionConfig{Binding: ClaudeConnectBinding{TranscriptPath: transcript, Endpoint: claudeEndpoint{PID: 42, StartToken: "s42"}}}
	}
	codexConsumer := &consumerObservation{State: "exited", PID: 9, StartToken: "s9"}
	pending := &queueAttempt{ClientID: "a1"}
	cases := []struct {
		name  string
		c     ConnectionState
		alive map[int]string
		idle  time.Duration
		want  string
	}{
		{"a running consumer is live even with a pending attempt", ConnectionState{State: "socket-ready", Claude: claude(), Pending: pending}, map[int]string{42: "s42"}, time.Hour, GCLive},
		{"a running consumer is live however old", ConnectionState{State: "socket-ready", Claude: claude()}, map[int]string{42: "s42"}, 90 * 24 * time.Hour, GCLive},
		{"a reused pid means the consumer is gone", ConnectionState{State: "socket-ready", Claude: claude()}, map[int]string{42: "other"}, time.Hour, GCCollect},
		{"a gone consumer inside the grace is kept", ConnectionState{State: "error", Claude: claude()}, nil, 5 * time.Minute, GCKeep},
		{"a gone consumer past the grace is collected", ConnectionState{State: "error", Claude: claude()}, nil, time.Hour, GCCollect},
		{"a gone consumer's pending attempt does not block", ConnectionState{State: "socket-ready", Claude: claude(), Pending: pending}, nil, time.Hour, GCCollect},
		{"a gone Codex consumer is treated the same", ConnectionState{State: "queue-ready", Consumer: codexConsumer}, nil, time.Hour, GCCollect},
		{"pending with no consumer on file waits", ConnectionState{State: "error", Pending: pending}, nil, 30 * 24 * time.Hour, GCPending},
		{"a stale online observation is not live", ConnectionState{State: "detached", Consumer: &consumerObservation{State: "online", PID: 7, StartToken: "s7"}}, nil, time.Minute, GCCollect},
		{"detached is collected at once", ConnectionState{State: "detached"}, nil, time.Minute, GCCollect},
		{"no consumer on file, past the inactivity limit", ConnectionState{State: "error", RolloutPath: transcript}, nil, 15 * 24 * time.Hour, GCCollect},
		{"no consumer on file, under the inactivity limit", ConnectionState{State: "disconnected", RolloutPath: transcript}, nil, 3 * 24 * time.Hour, GCKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.ID, tc.c.Channel, tc.c.Alias, tc.c.Harness = "abc", "ch", "al", daemonHarnessClaude
			p := gcTestProbe(tc.alive, map[string]time.Time{transcript: gcNow.Add(-tc.idle)})
			if got := classifyGC(&tc.c, "/nonexistent", limits, p); got.Class != tc.want {
				t.Fatalf("class %s (%s), want %s", got.Class, got.Reason, tc.want)
			}
		})
	}
}

func TestClassifyGCPendingNamesItsCommands(t *testing.T) {
	c := &ConnectionState{ID: "abc", Channel: "ch", Alias: "al", Relay: &RelayConfig{Host: "server"}, Pending: &queueAttempt{ClientID: "cbus-x-1"}}
	r := classifyGC(c, "/nonexistent", GCLimits{Grace: time.Minute, Inactive: time.Hour}, gcTestProbe(nil, nil))
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

	plan, err := planConnectionGC(daemon, bus, GCLimits{Grace: time.Minute, Inactive: time.Hour}, gcTestProbe(nil, nil))
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
	plan, err := planConnectionGC(filepath.Join(t.TempDir(), ".daemon"), t.TempDir(), GCLimits{Grace: time.Minute, Inactive: time.Hour}, gcTestProbe(nil, nil))
	if err != nil || len(plan.Records) != 0 {
		t.Fatalf("empty store: %+v, %v", plan, err)
	}
}

func TestGCManagedConsumerUsesFrontendLifetime(t *testing.T) {
	c := &ConnectionState{State: "queue-ready", Consumer: &consumerObservation{State: "online", PID: 9, StartToken: "backend", Managed: true, Frontend: &codexFrontend{PID: 10, StartToken: "frontend"}}}
	p := gcTestProbe(map[int]string{9: "backend"}, map[string]time.Time{"record": gcNow.Add(-time.Hour)})
	limits := GCLimits{Grace: time.Minute, Inactive: 24 * time.Hour}
	if got := classifyGC(c, "record", limits, p); got.Class != GCCollect {
		t.Fatalf("surviving backend kept expired frontend: %+v", got)
	}
	p = gcTestProbe(map[int]string{9: "backend", 10: "frontend"}, nil)
	if got := classifyGC(c, "record", limits, p); got.Class != GCLive {
		t.Fatalf("live frontend: %+v", got)
	}
	c.Consumer.Frontend = nil
	if gcConsumerKnown(c) || gcConsumerAlive(c, p) {
		t.Fatal("missing frontend borrowed backend lifetime")
	}
}
