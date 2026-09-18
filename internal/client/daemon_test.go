package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/core"
)

const daemonTestThread = "11111111-1111-4111-8111-111111111111"

type daemonQueueCall struct{ thread, clientID, text string }

type daemonFakeQueue struct {
	thread       codexQueueThread
	inspectErr   error
	enqueueErr   error
	findErr      error
	found        bool
	observation  *codexMessageLookup
	opens        int
	inspects     int
	finds        int
	closes       int
	calls        []daemonQueueCall
	afterEnqueue func()
}

func (q *daemonFakeQueue) inspect(id string) (codexQueueThread, error) {
	q.inspects++
	return q.thread, q.inspectErr
}
func (q *daemonFakeQueue) enqueue(thread, clientID, text string) (string, error) {
	q.calls = append(q.calls, daemonQueueCall{thread, clientID, text})
	if q.afterEnqueue != nil {
		q.afterEnqueue()
	}
	return "native-queue-id", q.enqueueErr
}
func (q *daemonFakeQueue) lookupMessage(thread, clientID string) (codexMessageLookup, error) {
	q.finds++
	if q.findErr != nil {
		return codexMessageLookup{}, q.findErr
	}
	if q.observation != nil {
		return *q.observation, nil
	}
	if q.found {
		for _, call := range q.calls {
			if call.thread == thread && call.clientID == clientID {
				return codexMessageLookup{State: codexMessageQueued, QueueID: "native-queue-id"}, nil
			}
		}
	}
	return codexMessageLookup{State: codexMessageNotFound}, nil
}
func (q *daemonFakeQueue) Close() error { q.closes++; return nil }

func daemonFixture(t *testing.T) (*busDaemon, *daemonFakeQueue, ConnectRequest) {
	t.Helper()
	root := setupStore(t)
	q := &daemonFakeQueue{thread: codexQueueThread{ID: daemonTestThread, Source: "cli", CliVersion: "test-version", Cwd: root}}
	d := reloadDaemonFixture(t, q)
	req := ConnectRequest{Channel: "dev", Alias: "worker", ThreadID: daemonTestThread,
		Config: CodexQueueConfig{Binary: filepath.Join(root, "codex"), Home: filepath.Join(root, "codex-home"), Cwd: root, SQLiteHome: filepath.Join(root, "codex-home"), UserHome: root}}
	return d, q, req
}

func reloadDaemonFixture(t *testing.T, q *daemonFakeQueue) *busDaemon {
	t.Helper()
	d := newBusDaemon()
	d.start = selfStart(t)
	d.probeConsumer = func(_ context.Context, c *ConnectionState) (consumerProbe, error) {
		if c.Consumer == nil {
			return onlinePresence(nil), nil // Admission proof; presence stays independent.
		}
		return consumerProbe{State: "unknown"}, nil
	}
	d.openQueue = func(CodexQueueConfig) (nativeQueue, error) { q.opens++; return q, nil }
	if err := d.load(); err != nil {
		t.Fatal(err)
	}
	return d
}

func mustDaemonConnect(t *testing.T, d *busDaemon, req ConnectRequest) *ConnectionState {
	t.Helper()
	c, err := d.connect(req)
	if err != nil {
		t.Fatal(err)
	}
	return d.connections[c.ID]
}

func appendDaemonMessage(t *testing.T, c *ConnectionState, kind, text string) int64 {
	t.Helper()
	line, err := json.Marshal(core.Message{From: "dev/sender", To: c.Channel + "/" + c.Alias, TS: "time", Kind: kind, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if err := appendInbox(InboxPath(c.Channel, c.Alias), line); err != nil {
		t.Fatal(err)
	}
	return int64(len(line) + 1)
}

// blockDaemonJournal forces an actual rename failure without relying on chmod
// (which is ineffective for elevated test users). The prior journal is restored.
func blockDaemonJournal(t *testing.T, d *busDaemon, c *ConnectionState) func() {
	t.Helper()
	path := filepath.Join(d.root, "connections", c.ID+".json")
	backup := path + ".saved"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(backup, path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDaemonConnectUsesCurrentCLINotThreadOrigin(t *testing.T) {
	for _, source := range []string{"cli", "vscode", "appServer", "exec", "unknown", ""} {
		t.Run("source="+source, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			q.thread.Source = source
			c, err := d.connect(req)
			if err != nil || c.RecordedVersion != "test-version" || !d.owns(c) {
				t.Fatalf("current CLI rejected because of historical source: %+v, err=%v", c, err)
			}
		})
	}
	t.Run("wrong thread", func(t *testing.T) {
		d, q, req := daemonFixture(t)
		q.thread.ID = "22222222-2222-4222-8222-222222222222"
		if _, err := d.connect(req); err == nil {
			t.Fatal("backend thread mismatch accepted")
		}
		if dirExists(filepath.Join(CBUSDir(), req.Channel)) {
			t.Fatal("thread mismatch claimed a peer")
		}
	})
}

func TestDaemonRepeatConnectPreservesInboxAndEpoch(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "first")
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	appendDaemonMessage(t, c, "", "still queued")
	before, err := os.ReadFile(InboxPath(c.Channel, c.Alias))
	if err != nil {
		t.Fatal(err)
	}
	again := mustDaemonConnect(t, d, req)
	after, err := os.ReadFile(InboxPath(c.Channel, c.Alias))
	if err != nil || string(before) != string(after) || again.ID != c.ID || again.Offset != end || again.Accepted != 1 || len(q.calls) != 1 {
		t.Fatalf("repeat connect changed inbox/epoch/progress: %+v, err=%v", again, err)
	}
	restarted := reloadDaemonFixture(t, q)
	again = mustDaemonConnect(t, restarted, req)
	if again.ID != c.ID || again.Offset != end || len(restarted.connections) != 1 {
		t.Fatal("repeat connect after daemon restart changed epoch or offset")
	}
}

func TestDaemonEnqueuesOnceAndPersistsAcceptance(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "hello recipient")
	for range 3 {
		if err := d.deliver(c); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.calls) != 1 || q.calls[0].thread != req.ThreadID || q.calls[0].clientID == "" || !strings.Contains(q.calls[0].text, "hello recipient") {
		t.Fatalf("unexpected native queue calls: %+v", q.calls)
	}
	if c.Offset != end || c.Accepted != 1 || c.Pending != nil || c.LastQueueID != "native-queue-id" {
		t.Fatalf("acceptance not recorded: %+v", c)
	}
	restarted := reloadDaemonFixture(t, q)
	if err := restarted.deliver(restarted.connections[c.ID]); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 1 || restarted.connections[c.ID].Offset != end {
		t.Fatal("accepted message replayed after restart")
	}
}

func TestDaemonIdleDoesNotQueryHarnessButRealPresenceIsDelivered(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	opens, inspects := q.opens, q.inspects
	for range 3 {
		if err := d.deliver(c); err != nil {
			t.Fatal(err)
		}
	}
	if q.opens != opens || q.inspects != inspects || len(q.calls) != 0 {
		t.Fatal("idle connection queried the harness")
	}
	line, err := json.Marshal(core.Message{From: "dev/sender", To: "dev/worker", TS: "time", Kind: "presence", Event: "join", Text: "peer joined"})
	if err != nil {
		t.Fatal(err)
	}
	if err := appendInbox(InboxPath(c.Channel, c.Alias), line); err != nil {
		t.Fatal(err)
	}
	end := int64(len(line) + 1)
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if q.opens != opens+1 || q.inspects != inspects+1 || len(q.calls) != 1 || c.Offset != end || c.Accepted != 1 || !strings.Contains(q.calls[0].text, "Briefly tell the user") || !strings.Contains(q.calls[0].text, "Do not send a bus reply") {
		t.Fatalf("presence lacks visible notice or bus-loop protection: queue=%+v, connection=%+v", q, c)
	}
}

func TestDaemonPresenceJournalFailureKeepsOffsetAndMakesNoCall(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "presence", "peer joined")
	restore := blockDaemonJournal(t, d, c)
	err := d.deliver(c)
	restore()
	if err == nil || c.Offset != 0 || len(q.calls) != 0 {
		t.Fatalf("failed presence save advanced the cursor: %+v, err=%v", c, err)
	}
	if err := d.deliver(c); err != nil || c.Offset != end || len(q.calls) != 1 || c.Accepted != 1 {
		t.Fatalf("presence delivery did not recover after storage recovery: %+v, err=%v", c, err)
	}
}

func TestDaemonWaitsForCompleteInboxLine(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	line, err := json.Marshal(core.Message{From: "dev/sender", To: "dev/worker", Text: "partial append"})
	if err != nil {
		t.Fatal(err)
	}
	path := InboxPath(c.Channel, c.Alias)
	if err := os.WriteFile(path, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.deliver(c); err != nil || len(q.calls) != 0 || c.Offset != 0 || c.Pending != nil {
		t.Fatalf("partial record was consumed: %+v, err=%v", c, err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.deliver(c); err != nil || len(q.calls) != 1 || c.Offset != int64(len(line)+1) {
		t.Fatalf("completed record was not delivered: %+v, err=%v", c, err)
	}
}

func TestDaemonReconnectPreservesDisconnectedEpoch(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		name := "queued"
		if ambiguous {
			name = "ambiguous"
		}
		t.Run(name, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			firstEnd := appendDaemonMessage(t, c, "", "accepted")
			if err := d.deliver(c); err != nil {
				t.Fatal(err)
			}
			totalEnd := firstEnd + appendDaemonMessage(t, c, "", "queued across disconnect")
			pendingID := ""
			if ambiguous {
				q.enqueueErr = errors.New("response lost")
				if err := d.deliver(c); err == nil || c.Pending == nil {
					t.Fatalf("pending fixture failed: %v", err)
				}
				pendingID = c.Pending.ClientID
				q.enqueueErr = nil
			}
			id := c.ID
			if err := d.disconnect("dev/worker"); err != nil {
				t.Fatal(err)
			}
			if MetaListenerAlive(filepath.Join(d.peerDir(c), "meta.json")) {
				t.Fatal("disconnected peer still advertises a listener")
			}
			before, err := os.ReadFile(InboxPath(c.Channel, c.Alias))
			if err != nil {
				t.Fatal(err)
			}
			d = reloadDaemonFixture(t, q)
			c = mustDaemonConnect(t, d, req)
			after, err := os.ReadFile(InboxPath(c.Channel, c.Alias))
			if err != nil || string(before) != string(after) || c.ID != id || c.Offset != firstEnd || c.Accepted != 1 {
				t.Fatalf("reconnect changed durable progress or inbox: %+v, err=%v", c, err)
			}
			if ambiguous && (c.Pending == nil || c.Pending.ClientID != pendingID) {
				t.Fatal("reconnect lost ambiguous attempt identity")
			}
			q.found = true
			if err := d.deliver(c); err != nil {
				t.Fatal(err)
			}
			if len(q.calls) != 2 || c.Offset != totalEnd || c.Accepted != 2 {
				t.Fatalf("reconnect replayed or lost queued message: calls=%d, state=%+v", len(q.calls), c)
			}
		})
	}
}

func TestDaemonLostAcknowledgementNeverBlindlyRetries(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "ambiguous")
	q.enqueueErr = &codexQueueTransportError{Method: "thread/queue/add", Err: errors.New("lost response")}
	if err := d.deliver(c); err == nil || c.Pending == nil || c.Offset != 0 {
		t.Fatalf("lost response must retain pending attempt: %+v, err=%v", c, err)
	}
	clientID := c.Pending.ClientID
	q.enqueueErr = nil
	for range 2 {
		if err := d.deliver(c); err == nil {
			t.Fatal("absence in queue/history must remain uncertain")
		}
	}
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	if c.Pending == nil || c.Pending.ClientID != clientID || c.Offset != 0 {
		t.Fatal("pending attempt did not survive daemon reload")
	}
	if err := d.deliver(c); err == nil || len(q.calls) != 1 {
		t.Fatalf("reload blindly retried ambiguous enqueue: calls=%d, err=%v", len(q.calls), err)
	}
	q.found = true
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 1 || c.Pending != nil || c.Offset != end || c.Accepted != 1 {
		t.Fatalf("reconciliation should advance without enqueue: calls=%d, state=%+v", len(q.calls), c)
	}
}

func TestDaemonReconciliationTransportFailureReopensSidecar(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "ambiguous then sidecar lost")
	q.enqueueErr = &codexQueueTransportError{Method: "thread/queue/add", Err: errors.New("lost response")}
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatalf("pending fixture failed: %v", err)
	}
	q.enqueueErr = nil
	q.findErr = &codexQueueTransportError{Method: "thread/queue/list", Err: errors.New("sidecar exited")}
	closes := q.closes
	if err := d.deliver(c); err == nil || c.Pending == nil || c.Offset != 0 {
		t.Fatalf("reconciliation error must retain pending progress: %+v, err=%v", c, err)
	}
	if d.queues[c.ID] != nil || q.closes != closes+1 {
		t.Fatal("failed reconciliation left a dead sidecar cached")
	}
	opens := q.opens
	q.findErr, q.found = nil, true
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if q.opens != opens+1 || len(q.calls) != 1 || c.Offset != end || c.Pending != nil {
		t.Fatalf("fresh sidecar failed to reconcile without resending: queue=%+v, state=%+v", q, c)
	}
}

func TestDaemonExplicitRejectionCanRetryWithoutAdvancing(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "retry after rejection")
	q.enqueueErr = &rpcError{Code: -32600, Message: "queue unavailable"}
	if err := d.deliver(c); err == nil || c.Offset != 0 || c.Pending != nil || c.Accepted != 0 {
		t.Fatalf("explicit rejection changed progress: %+v, err=%v", c, err)
	}
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	q.enqueueErr = nil
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 2 || q.calls[0].clientID != q.calls[1].clientID || c.Offset != end || c.Accepted != 1 {
		t.Fatalf("rejection retry did not retain the message identity: calls=%+v, state=%+v", q.calls, c)
	}
}

func TestDaemonJournalFailureBeforeEnqueueMakesNoCall(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "", "must be journaled")
	restore := blockDaemonJournal(t, d, c)
	err := d.deliver(c)
	restore()
	if err == nil || len(q.calls) != 0 || c.Offset != 0 || c.Accepted != 0 {
		t.Fatalf("journal failure allowed enqueue/progress: calls=%d, state=%+v, err=%v", len(q.calls), c, err)
	}
	if err := d.deliver(c); err != nil || len(q.calls) != 1 {
		t.Fatalf("delivery after storage recovery failed: calls=%d, err=%v", len(q.calls), err)
	}
}

func TestDaemonFailedAcceptanceSaveDoesNotEnqueueAgain(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "same process"
		if restart {
			name = "after restart"
		}
		t.Run(name, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			end := appendDaemonMessage(t, c, "", "accepted but save failed")
			var restore func()
			q.afterEnqueue = func() { restore = blockDaemonJournal(t, d, c) }
			err := d.deliver(c)
			if restore == nil {
				t.Fatal("test did not reach enqueue")
			}
			restore()
			q.afterEnqueue = nil
			if err == nil || c.Pending == nil || c.Offset != 0 || len(q.calls) != 1 {
				t.Fatalf("failed accept save lost pending: %+v, err=%v", c, err)
			}
			if restart {
				d = reloadDaemonFixture(t, q)
				c = d.connections[c.ID]
				q.found = true
			}
			if err := d.deliver(c); err != nil {
				t.Fatal(err)
			}
			if len(q.calls) != 1 || c.Offset != end || c.Pending != nil || c.Accepted != 1 {
				t.Fatalf("accept save recovery duplicated submission: calls=%d, state=%+v", len(q.calls), c)
			}
		})
	}
}

func TestDaemonStopsOnRemovedOrReplacedRegistration(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "removed"
		if replace {
			name = "replaced"
		}
		t.Run(name, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			appendDaemonMessage(t, c, "", "must not reach old binding")
			if err := os.RemoveAll(d.peerDir(c)); err != nil {
				t.Fatal(err)
			}
			if replace {
				seedManagedPeer(t, CBUSDir(), c.Alias, "replacement-session")
			}
			if err := d.deliver(c); err != nil {
				t.Fatal(err)
			}
			if len(q.calls) != 0 || c.State != "detached" {
				t.Fatalf("detached binding injected: %+v", c)
			}
			if !replace && dirExists(d.peerDir(c)) {
				t.Fatal("daemon recreated removed registration")
			}
			d = reloadDaemonFixture(t, q)
			c = d.connections[c.ID]
			if err := d.deliver(c); err != nil || len(q.calls) != 0 || c.State != "detached" {
				t.Fatalf("reload revived detached binding: %+v, err=%v", c, err)
			}
		})
	}
}

func TestDaemonCorruptMetadataDoesNotDetachConnection(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "wait through corrupt metadata")
	path := filepath.Join(d.peerDir(c), "meta.json")
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{torn metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.deliver(c); err == nil || c.State == "detached" || len(q.calls) != 0 || c.Offset != 0 {
		t.Fatalf("corrupt metadata must pause without detaching or injecting: %+v, err=%v", c, err)
	}
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	if c.State == "detached" {
		t.Fatal("metadata read error persisted a detached connection")
	}
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.deliver(c); err != nil || len(q.calls) != 1 || c.Offset != end {
		t.Fatalf("restored metadata did not resume delivery: %+v, err=%v", c, err)
	}
}

func TestDaemonNativeQueueNeverResumesThread(t *testing.T) {
	d, _, req := daemonFixture(t)
	// This helper process exits on thread/start, thread/resume, or any turn API.
	q := fakeCodexQueue(t, "normal", time.Second)
	d.openQueue = func(CodexQueueConfig) (nativeQueue, error) { return q, nil }
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "", "native queue only")
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if c.Accepted != 1 {
		t.Fatalf("expected one accepted queue submission: %+v", c)
	}
}
