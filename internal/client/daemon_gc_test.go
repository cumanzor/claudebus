package client

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var gcTestLimits = GCLimits{Grace: 15 * time.Minute, Inactive: time.Hour}

// gcDaemonProbe treats every session as inactive for 30 days; alive answers
// the consumer-liveness question in call order, repeating its last answer.
func gcDaemonProbe(alive ...bool) gcProbe {
	calls := 0
	return gcProbe{
		now: gcNow,
		alive: func(int, string) bool {
			if len(alive) == 0 {
				return false
			}
			i := min(calls, len(alive)-1)
			calls++
			return alive[i]
		},
		mtime: func(string) (time.Time, bool) { return gcNow.Add(-30 * 24 * time.Hour), true },
	}
}

// staleConnection connects the fixture's peer, leaves one unread message in its
// inbox and marks it detached, as leave or unregister would.
func staleConnection(t *testing.T) (*busDaemon, *daemonFakeQueue, *ConnectionState) {
	t.Helper()
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "msg", "never read")
	c.State = "detached"
	c.Consumer = &consumerObservation{State: "online", PID: 4242, StartToken: "s"}
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d.publish(c)
	return d, q, c
}

func gcArchiveOf(d *busDaemon, id string) string {
	return filepath.Join(d.root, "connections", ".archive", gcNow.Format("2006-01"), id)
}

func TestCollectArchivesADetachedRecordAndForgetsIt(t *testing.T) {
	d, q, c := staleConnection(t)
	peer := d.peerDir(c)
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	results := pass.Records
	if err != nil || len(results) != 1 || !results[0].Collected {
		t.Fatalf("collect: %+v, %v", results, err)
	}
	archive := gcArchiveOf(d, c.ID)
	if results[0].Archive != archive {
		t.Fatalf("archive = %q, want %q", results[0].Archive, archive)
	}
	for _, f := range []string{"record.json", filepath.Join("peer", "inbox.jsonl"), filepath.Join("peer", "meta.json")} {
		if _, err := os.Stat(filepath.Join(archive, f)); err != nil {
			t.Errorf("archive lacks %s: %v", f, err)
		}
	}
	unread, err := os.ReadFile(filepath.Join(archive, "unread.jsonl"))
	if err != nil || !strings.Contains(string(unread), "never read") {
		t.Fatalf("unread export = %q, %v", unread, err)
	}
	for _, gone := range []string{peer, d.recordPath(c.ID)} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still in place after collection: %v", gone, err)
		}
	}
	d.mu.Lock()
	_, inMemory := d.connections[c.ID]
	_, snapshot := d.snapshots[c.ID]
	d.mu.Unlock()
	if inMemory || snapshot {
		t.Fatalf("collected record still in memory: connections=%v snapshots=%v", inMemory, snapshot)
	}
	if reloaded := reloadDaemonFixture(t, q); len(reloaded.connections) != 0 {
		t.Fatalf("a restarted daemon loaded %d collected records", len(reloaded.connections))
	}
}

func TestCollectLeavesUnconfirmedPendingAndLiveRecords(t *testing.T) {
	t.Run("pending with no consumer on file", func(t *testing.T) {
		d, _, c := staleConnection(t)
		c.State, c.Consumer = "error", nil
		c.Pending = &queueAttempt{ClientID: "cbus-x-1"}
		d.publish(c)
		pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
		results := pass.Records
		if err != nil || results[0].Class != GCPending || results[0].Collected {
			t.Fatalf("pending: %+v, %v", results, err)
		}
		if _, err := os.Stat(d.recordPath(c.ID)); err != nil {
			t.Fatalf("pending record moved: %v", err)
		}
	})
	t.Run("live", func(t *testing.T) {
		d, _, c := staleConnection(t)
		pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(true))
		results := pass.Records
		if err != nil || results[0].Class != GCLive || results[0].Collected {
			t.Fatalf("live: %+v, %v", results, err)
		}
		if _, err := os.Stat(d.peerDir(c)); err != nil {
			t.Fatalf("live peer folder moved: %v", err)
		}
	})
}

// a session that resumes between the plan and the record's lane wins.
func TestCollectLeavesARecordThatCameBackDuringThePass(t *testing.T) {
	d, _, c := staleConnection(t)
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false, true))
	results := pass.Records
	if err != nil || results[0].Collected || !strings.HasPrefix(results[0].Left, "now live") {
		t.Fatalf("recheck under the lane: %+v, %v", results, err)
	}
	if _, err := os.Stat(d.recordPath(c.ID)); err != nil {
		t.Fatalf("resumed record moved: %v", err)
	}
}

func TestCollectLeavesAnInboxANewerConnectionOwns(t *testing.T) {
	d, _, c := staleConnection(t)
	peer := d.peerDir(c)
	metaPath := filepath.Join(peer, "meta.json")
	var m map[string]any
	if b, err := os.ReadFile(metaPath); err != nil || json.Unmarshal(b, &m) != nil {
		t.Fatalf("fixture peer meta: %v", err)
	}
	m["connectionId"] = "newer"
	b, _ := json.Marshal(m)
	if err := os.WriteFile(metaPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	results := pass.Records
	if err != nil || !results[0].Collected {
		t.Fatalf("collect: %+v, %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(peer, "inbox.jsonl")); err != nil {
		t.Fatalf("the newer connection's inbox was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gcArchiveOf(d, c.ID), "peer")); !os.IsNotExist(err) {
		t.Fatalf("archived a folder the record does not own: %v", err)
	}
}

// a step that fails before the record moves commits nothing, and the next
// pass finishes the job.
func TestCollectFailureBeforeTheRecordMovesLeavesItForTheNextPass(t *testing.T) {
	d, _, c := staleConnection(t)
	peer := d.peerDir(c)
	blocker := filepath.Join(gcArchiveOf(d, c.ID), "peer", "occupied")
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	results := pass.Records
	if err != nil || results[0].Collected || !strings.HasPrefix(results[0].Left, "archive inbox") {
		t.Fatalf("blocked archive: %+v, %v", results, err)
	}
	for _, kept := range []string{d.recordPath(c.ID), filepath.Join(peer, "inbox.jsonl")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s did not survive the failed step: %v", kept, err)
		}
	}
	if d.snapshot(c.ID) == nil {
		t.Fatal("a failed collection dropped the record from memory")
	}
	if err := os.RemoveAll(filepath.Dir(blocker)); err != nil {
		t.Fatal(err)
	}
	if pass, err = d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false)); err != nil || !pass.Records[0].Collected {
		t.Fatalf("second pass: %+v, %v", pass, err)
	}
}

func TestCollectRemovesOnlyAnUnsharedCredential(t *testing.T) {
	d, _, c := staleConnection(t)
	dir := filepath.Join(d.root, claudeCredentialDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ref := "11111111-2222-4333-8444-555555555555.token"
	if err := os.WriteFile(filepath.Join(dir, ref), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.Claude = &ClaudeConnectionConfig{CredentialRef: ref}
	other := &ConnectionState{ID: "other", Claude: &ClaudeConnectionConfig{CredentialRef: ref}}
	d.mu.Lock()
	d.connections[other.ID] = other
	d.mu.Unlock()
	if err := d.removeClaudeCredential(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ref)); err != nil {
		t.Fatalf("a credential another record uses was removed: %v", err)
	}
	d.mu.Lock()
	delete(d.connections, other.ID)
	d.mu.Unlock()
	if err := d.removeClaudeCredential(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ref)); !os.IsNotExist(err) {
		t.Fatalf("an unshared credential survived collection: %v", err)
	}
}

func TestCollectWaitsForAConnectInProgress(t *testing.T) {
	d, _, _ := staleConnection(t)
	d.connectGate <- struct{}{}
	done := make(chan []GCResult, 1)
	go func() {
		pass, _ := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
		done <- pass.Records
	}()
	select {
	case <-done:
		t.Fatal("collection ran while a connect held the gate")
	case <-time.After(150 * time.Millisecond):
	}
	<-d.connectGate
	select {
	case results := <-done:
		if len(results) != 1 || !results[0].Collected {
			t.Fatalf("after the connect: %+v", results)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("collection never resumed after the connect finished")
	}
}

func TestCollectSweepsTokensNoRecordReferences(t *testing.T) {
	d, _, c := staleConnection(t)
	c.State = "socket-ready" // keep the record: only the sweep acts
	d.publish(c)
	dir := filepath.Join(d.root, claudeCredentialDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	kept, orphan := "11111111-2222-4333-8444-555555555555.token", "66666666-7777-4888-8999-aaaaaaaaaaaa.token"
	for _, name := range []string{kept, orphan, "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.Claude = &ClaudeConnectionConfig{CredentialRef: kept}

	d.skipped = []string{"broken.json"}
	pass, err := d.collectConnections(context.Background(), GCLimits{Grace: 365 * 24 * time.Hour, Inactive: 365 * 24 * time.Hour}, gcDaemonProbe(true))
	if err != nil || pass.OrphanTokens != 0 || pass.TokenSweepOff == "" {
		t.Fatalf("an unreadable record must stop the sweep: %+v, %v", pass, err)
	}
	if _, err := os.Stat(filepath.Join(dir, orphan)); err != nil {
		t.Fatalf("swept while a record was unreadable: %v", err)
	}

	d.skipped = nil
	if pass, err = d.collectConnections(context.Background(), GCLimits{Grace: 365 * 24 * time.Hour, Inactive: 365 * 24 * time.Hour}, gcDaemonProbe(true)); err != nil || pass.OrphanTokens != 1 {
		t.Fatalf("sweep: %+v, %v", pass, err)
	}
	for name, want := range map[string]bool{kept: true, orphan: false, "notes.txt": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", name, err == nil, want)
		}
	}
}

// once its consumer is confirmed gone, a pending attempt no longer blocks: its
// message is past the delivered offset, so the unread export carries it.
func TestCollectArchivesAGoneConsumersPendingMessage(t *testing.T) {
	d, _, c := staleConnection(t)
	c.State = "socket-ready"
	c.Pending = &queueAttempt{ClientID: "cbus-x-1", End: c.Offset + 1}
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d.publish(c)
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	if err != nil || !pass.Records[0].Collected || !strings.Contains(pass.Records[0].Reason, "pending attempt is archived") {
		t.Fatalf("collect: %+v, %v", pass, err)
	}
	unread, err := os.ReadFile(filepath.Join(gcArchiveOf(d, c.ID), "unread.jsonl"))
	if err != nil || !strings.Contains(string(unread), "never read") {
		t.Fatalf("the pending message is not in the export: %q, %v", unread, err)
	}
	record, err := os.ReadFile(filepath.Join(gcArchiveOf(d, c.ID), "record.json"))
	if err != nil || !strings.Contains(string(record), "cbus-x-1") {
		t.Fatalf("the archived record lost its pending attempt: %v", err)
	}
}

func TestGCPassRecordsStatusAndPrunesOnlyOldArchives(t *testing.T) {
	d, _, c := staleConnection(t)
	archive := filepath.Join(d.root, "connections", ".archive", "2026-08")
	old, recent := filepath.Join(archive, "old"), filepath.Join(archive, "recent")
	for _, dir := range []string{old, recent} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(old, gcNow.Add(-40*24*time.Hour), gcNow.Add(-40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(recent, gcNow.Add(-2*24*time.Hour), gcNow.Add(-2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	d.gcPass(context.Background(), GCSettings{Auto: true, Limits: gcTestLimits, ArchiveKeep: 30 * 24 * time.Hour}, gcDaemonProbe(false))
	d.mu.Lock()
	st := d.lastGC
	d.mu.Unlock()
	if st == nil || st.Collected != 1 || st.ArchivesPruned != 1 || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	for path, want := range map[string]bool{old: false, recent: true, gcArchiveOf(d, c.ID): true} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", path, err == nil, want)
		}
	}
}

func TestRunGCPassesAfterItsDelayAndStopsWithTheDaemon(t *testing.T) {
	d, _, _ := staleConnection(t)
	d.gcFirst, d.gcEvery = 10*time.Millisecond, time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.runGC(ctx, GCSettings{Auto: true, Limits: gcTestLimits, ArchiveKeep: time.Hour})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.mu.Lock()
		st := d.lastGC
		d.mu.Unlock()
		if st != nil {
			if st.Collected != 1 {
				t.Fatalf("first automatic pass: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no automatic pass after the first delay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the GC loop outlived its daemon")
	}
}

func TestRunGCOffDoesNothing(t *testing.T) {
	d, _, c := staleConnection(t)
	d.gcFirst = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		d.runGC(ctx, GCSettings{Auto: false, Limits: gcTestLimits})
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		cancel()
		<-returned
		t.Fatal("with CBUS_GC=off the loop must return at once, not schedule passes")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastGC != nil {
		t.Fatalf("a disabled GC ran a pass: %+v", d.lastGC)
	}
	if _, err := os.Stat(d.recordPath(c.ID)); err != nil {
		t.Fatalf("a disabled GC moved a record: %v", err)
	}
}

func TestLoadGCSettings(t *testing.T) {
	t.Setenv("CBUS_GC", "")
	t.Setenv("CBUS_GC_GRACE", "")
	t.Setenv("CBUS_GC_INACTIVE", "")
	t.Setenv("CBUS_GC_ARCHIVE", "")
	s, err := LoadGCSettings()
	if err != nil || !s.Auto || s.Limits != (GCLimits{Grace: 15 * time.Minute, Inactive: 14 * 24 * time.Hour}) || s.ArchiveKeep != 30*24*time.Hour {
		t.Fatalf("defaults: %+v, %v", s, err)
	}
	t.Setenv("CBUS_GC", "off")
	t.Setenv("CBUS_GC_GRACE", "1h")
	t.Setenv("CBUS_GC_INACTIVE", "3d")
	t.Setenv("CBUS_GC_ARCHIVE", "7d")
	s, err = LoadGCSettings()
	if err != nil || s.Auto || s.Limits != (GCLimits{Grace: time.Hour, Inactive: 3 * 24 * time.Hour}) || s.ArchiveKeep != 7*24*time.Hour {
		t.Fatalf("overrides: %+v, %v", s, err)
	}
	for name, bad := range map[string]string{"CBUS_GC": "maybe", "CBUS_GC_GRACE": "-5m", "CBUS_GC_ARCHIVE": "0d"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, bad)
			if _, err := LoadGCSettings(); err == nil {
				t.Fatalf("%s=%q accepted", name, bad)
			}
		})
	}
}

func TestConnectNamesTheArchivedPredecessorOfTheSameSession(t *testing.T) {
	d, _, c := staleConnection(t)
	if pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false)); err != nil || !pass.Records[0].Collected {
		t.Fatalf("collect: %+v, %v", pass, err)
	}
	fresh := &ConnectionState{ID: "fresh", Channel: c.Channel, Alias: c.Alias, ThreadID: c.ThreadID}
	got := d.archivedPredecessor(fresh)
	if got == nil || got.ID != c.ID || got.Unread != 1 || got.Path != gcArchiveOf(d, c.ID) {
		t.Fatalf("predecessor = %+v", got)
	}
	for name, other := range map[string]*ConnectionState{
		"another channel": {ID: "x", Channel: "other", ThreadID: c.ThreadID},
		"another session": {ID: "x", Channel: c.Channel, ThreadID: "01a0b0c6-0000-7fd0-9319-1ab0275adc21"},
		"over a relay":    {ID: "x", Channel: c.Channel, ThreadID: c.ThreadID, Relay: &RelayConfig{Host: "server"}},
	} {
		if got := d.archivedPredecessor(other); got != nil {
			t.Errorf("%s matched %+v", name, got)
		}
	}
}

// stalePresenceConnection is staleConnection whose undelivered tail holds the
// given lines instead of one message.
func stalePresenceConnection(t *testing.T, lines ...string) (*busDaemon, *ConnectionState) {
	t.Helper()
	d, q, req := daemonFixture(t)
	_ = q
	c := mustDaemonConnect(t, d, req)
	f, err := os.OpenFile(InboxPath(c.Channel, c.Alias), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	c.State = "detached"
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d.publish(c)
	return d, c
}

func TestCollectExportsNoUnreadFileForPresenceOnly(t *testing.T) {
	d, c := stalePresenceConnection(t,
		`{"from":"dev/a","kind":"presence","event":"join","text":"joined"}`+"\n",
		"\n",
		`{"from":"dev/a","kind":"presence","event":"departed","text":"departed"}`+"\n")
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	if err != nil || !pass.Records[0].Collected || pass.Records[0].Unread != 0 {
		t.Fatalf("collect: %+v, %v", pass, err)
	}
	if _, err := os.Stat(filepath.Join(gcArchiveOf(d, c.ID), "unread.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a presence-only tail produced an unread export: %v", err)
	}
}

func TestCollectExportsOnlyMailAndKeepsATornLine(t *testing.T) {
	d, c := stalePresenceConnection(t,
		`{"from":"dev/a","kind":"presence","event":"join","text":"joined"}`+"\n",
		`{"from":"dev/a","text":"real message"}`+"\n",
		`{"from":"dev/a","kind":"presence","event":"departed","text":"departed"}`+"\n",
		`{"from":"dev/b","text":"torn mid-wri`)
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	if err != nil || !pass.Records[0].Collected {
		t.Fatalf("collect: %+v, %v", pass, err)
	}
	b, err := os.ReadFile(filepath.Join(gcArchiveOf(d, c.ID), "unread.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"from":"dev/a","text":"real message"}` + "\n" + `{"from":"dev/b","text":"torn mid-wri` + "\n"
	if string(b) != want {
		t.Fatalf("export = %q, want %q", b, want)
	}
	fresh := &ConnectionState{ID: "fresh", Channel: c.Channel, ThreadID: c.ThreadID}
	if counted, named := pass.Records[0].Unread, d.archivedPredecessor(fresh).Unread; counted != 2 || named != 2 {
		t.Fatalf("the plan counted %d and connect would report %d; both must match the 2 exported lines", counted, named)
	}
}
