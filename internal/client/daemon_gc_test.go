package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	pass, err := d.collectConnections(time.Hour, gcDaemonProbe(false))
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

func TestCollectNeverTouchesPendingOrLiveRecords(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		d, _, c := staleConnection(t)
		c.Pending = &queueAttempt{ClientID: "cbus-x-1"}
		d.publish(c)
		pass, err := d.collectConnections(time.Hour, gcDaemonProbe(false))
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
		pass, err := d.collectConnections(time.Hour, gcDaemonProbe(true))
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
	pass, err := d.collectConnections(time.Hour, gcDaemonProbe(false, true))
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
	pass, err := d.collectConnections(time.Hour, gcDaemonProbe(false))
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
	pass, err := d.collectConnections(time.Hour, gcDaemonProbe(false))
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
	if pass, err = d.collectConnections(time.Hour, gcDaemonProbe(false)); err != nil || !pass.Records[0].Collected {
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
		pass, _ := d.collectConnections(time.Hour, gcDaemonProbe(false))
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
	pass, err := d.collectConnections(24*time.Hour*365, gcDaemonProbe(true))
	if err != nil || pass.OrphanTokens != 0 || pass.TokenSweepOff == "" {
		t.Fatalf("an unreadable record must stop the sweep: %+v, %v", pass, err)
	}
	if _, err := os.Stat(filepath.Join(dir, orphan)); err != nil {
		t.Fatalf("swept while a record was unreadable: %v", err)
	}

	d.skipped = nil
	if pass, err = d.collectConnections(24*time.Hour*365, gcDaemonProbe(true)); err != nil || pass.OrphanTokens != 1 {
		t.Fatalf("sweep: %+v, %v", pass, err)
	}
	for name, want := range map[string]bool{kept: true, orphan: false, "notes.txt": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", name, err == nil, want)
		}
	}
}
