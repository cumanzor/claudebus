package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func pendingRecoveryFixture(t *testing.T) (*busDaemon, *daemonFakeQueue, *ConnectionState, int64) {
	t.Helper()
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "first unresolved message")
	q.enqueueErr = &codexQueueTransportError{Method: "thread/queue/add", Err: errors.New("lost acknowledgement")}
	if err := d.deliver(c); err == nil || c.Pending == nil || c.Offset != 0 {
		t.Fatalf("pending fixture failed: state=%+v, err=%v", c, err)
	}
	q.enqueueErr = nil
	return d, q, c, end
}

func recoveryStateJSON(t *testing.T, c *ConnectionState) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDaemonRecoveryLatestAcceptedObservations(t *testing.T) {
	for _, observation := range []codexMessageLookup{
		{State: codexMessageQueued, QueueID: "still-queued"},
		{State: codexMessageReceived, ItemID: "receipt-item"},
		{State: codexMessageNotFound},
	} {
		t.Run(observation.State, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			end := appendDaemonMessage(t, c, "", "accepted")
			if err := d.deliver(c); err != nil {
				t.Fatal(err)
			}
			clientID := c.LastAccepted.Attempt.ClientID
			q.observation = &observation
			got, err := d.reconcile("dev/worker")
			wantState := observation.State
			if wantState == codexMessageNotFound {
				wantState = "unknown"
			}
			if err != nil || !reflect.DeepEqual(got, c) || c.LastAccepted.State != wantState || c.LastAccepted.ItemID != observation.ItemID || c.LastAccepted.ObservedAt == "" {
				t.Fatalf("observation lost: %+v, err=%v", c.LastAccepted, err)
			}
			if c.Accepted != 1 || c.Offset != end || c.Pending != nil || c.Abandoned != 0 || len(q.calls) != 1 || q.finds != 1 || c.LastAccepted.Attempt.ClientID != clientID {
				t.Fatalf("read-only observation changed delivery: state=%+v, queue=%+v", c, q)
			}
			if observation.QueueID != "" && c.LastAccepted.Attempt.QueueID != observation.QueueID {
				t.Fatal("queue observation lost its native queue ID")
			}
			reloaded := reloadDaemonFixture(t, q).connections[c.ID]
			if !reflect.DeepEqual(reloaded.LastAccepted, c.LastAccepted) {
				t.Fatal("observation did not persist")
			}
			if observation.State == codexMessageReceived {
				before := recoveryStateJSON(t, c)
				q.findErr = errors.New("history pruned or backend unavailable")
				if _, err := d.reconcile("dev/worker"); err != nil || q.finds != 1 || recoveryStateJSON(t, c) != before {
					t.Fatalf("positive history evidence was queried again or downgraded: %v", err)
				}
			}
		})
	}
}

func TestDaemonRecoveryPendingObservationDoesNotResend(t *testing.T) {
	for _, observation := range []codexMessageLookup{
		{State: codexMessageQueued, QueueID: "queued-after-lost-ack"},
		{State: codexMessageReceived, ItemID: "received-after-lost-ack"},
		{State: codexMessageNotFound},
	} {
		t.Run(observation.State, func(t *testing.T) {
			d, q, c, end := pendingRecoveryFixture(t)
			pendingID := c.Pending.ClientID
			q.observation = &observation
			if _, err := d.reconcile("dev/worker"); err != nil {
				t.Fatal(err)
			}
			if len(q.calls) != 1 || q.finds != 1 {
				t.Fatalf("reconciliation resent or repeated lookup: %+v", q)
			}
			if observation.State == codexMessageNotFound {
				if c.Pending == nil || c.Pending.ClientID != pendingID || c.Offset != 0 || c.Accepted != 0 || c.State != "uncertain" || !strings.Contains(c.Error, "absence does not prove rejection") {
					t.Fatalf("absence released the delivery fence: %+v", c)
				}
				return
			}
			if c.Pending != nil || c.Offset != end || c.Accepted != 1 || c.LastAccepted.State != observation.State || c.LastAccepted.Attempt.ClientID != pendingID || c.LastAccepted.ItemID != observation.ItemID {
				t.Fatalf("positive reconciliation was not accepted exactly once: %+v", c)
			}
		})
	}
}

func TestDaemonRecoveryDoesNotReactivateDisconnectedPeer(t *testing.T) {
	for _, action := range []string{"pending-found", "pending-absent", "latest", "abandon"} {
		t.Run(action, func(t *testing.T) {
			d, q, c, _ := pendingRecoveryFixture(t)
			pendingID := c.Pending.ClientID
			if action == "latest" {
				q.observation = &codexMessageLookup{State: codexMessageQueued, QueueID: "queue-1"}
				if _, err := d.reconcile("dev/worker"); err != nil {
					t.Fatal(err)
				}
			}
			if err := d.disconnect("dev/worker"); err != nil {
				t.Fatal(err)
			}
			q.observation = &codexMessageLookup{State: codexMessageQueued, QueueID: "queue-1"}
			if action == "pending-absent" {
				q.observation = &codexMessageLookup{State: codexMessageNotFound}
			}
			var err error
			if action == "abandon" {
				_, err = d.abandon(AbandonRequest{Target: "dev/worker", ClientID: pendingID, Reason: "operator accepted uncertainty"})
			} else {
				_, err = d.reconcile("dev/worker")
			}
			if err != nil || c.State != "disconnected" || MetaListenerAlive(filepath.Join(d.peerDir(c), "meta.json")) || len(q.calls) != 1 {
				t.Fatalf("recovery reactivated stopped peer: state=%+v, err=%v", c, err)
			}
			if got := reloadDaemonFixture(t, q).connections[c.ID]; got.State != "disconnected" {
				t.Fatalf("reload lost disconnected state: %+v", got)
			}
		})
	}
}

func TestDaemonRecoveryAbandonRejectsUnsafeSelection(t *testing.T) {
	for _, mode := range []string{"unowned", "stale-id", "no-pending", "epoch", "hash", "known-queue", "known-history"} {
		t.Run(mode, func(t *testing.T) {
			d, q, c, _ := pendingRecoveryFixture(t)
			req := AbandonRequest{Target: "dev/worker", ClientID: c.Pending.ClientID, Reason: "operator accepted uncertainty"}
			switch mode {
			case "unowned":
				req.Target = "dev/missing"
			case "stale-id":
				req.ClientID = "old-attempt"
			case "no-pending":
				c.Pending = nil
			case "epoch":
				path := InboxPath(c.Channel, c.Alias)
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
			case "hash":
				c.Pending.Hash = "not-the-inbox-hash"
			case "known-queue":
				c.Pending.QueueID = "already-acknowledged"
			case "known-history":
				c.Pending.Evidence = &codexMessageLookup{State: codexMessageReceived, ItemID: "already-received"}
			}
			before := recoveryStateJSON(t, c)
			if _, err := d.abandon(req); err == nil {
				t.Fatal("unsafe abandonment accepted")
			}
			if recoveryStateJSON(t, c) != before || len(q.calls) != 1 || q.finds != 0 {
				t.Fatal("rejected abandonment changed delivery or queried the runtime")
			}
		})
	}
}

func TestDaemonRecoveryRejectsReplacedOrRemovedRegistration(t *testing.T) {
	for _, action := range []string{"reconcile", "abandon"} {
		for _, mutation := range []string{"connection-id", "session-id", "removed-metadata"} {
			t.Run(action+"/"+mutation, func(t *testing.T) {
				d, q, c, _ := pendingRecoveryFixture(t)
				metaPath := filepath.Join(d.peerDir(c), "meta.json")
				if mutation == "removed-metadata" {
					if err := os.Remove(metaPath); err != nil {
						t.Fatal(err)
					}
				} else {
					data, err := os.ReadFile(metaPath)
					if err != nil {
						t.Fatal(err)
					}
					var meta peerMeta
					if err := json.Unmarshal(data, &meta); err != nil {
						t.Fatal(err)
					}
					if mutation == "connection-id" {
						meta.ConnectionID = "replacement-connection"
					} else {
						meta.SessionID = "22222222-2222-4222-8222-222222222222"
					}
					if err := writeMeta(d.peerDir(c), meta); err != nil {
						t.Fatal(err)
					}
				}
				before := recoveryStateJSON(t, c)
				journalPath := filepath.Join(d.root, "connections", c.ID+".json")
				journalBefore, err := os.ReadFile(journalPath)
				if err != nil {
					t.Fatal(err)
				}
				opens, finds, calls := q.opens, q.finds, len(q.calls)
				if action == "reconcile" {
					_, err = d.reconcile("dev/worker")
				} else {
					_, err = d.abandon(AbandonRequest{Target: "dev/worker", ClientID: c.Pending.ClientID, Reason: "must not affect replacement"})
				}
				if err == nil || !strings.Contains(err.Error(), "no owned managed connection") {
					t.Fatalf("recovery accepted changed registration: %v", err)
				}
				journalAfter, err := os.ReadFile(journalPath)
				if err != nil {
					t.Fatal(err)
				}
				if recoveryStateJSON(t, c) != before || string(journalAfter) != string(journalBefore) || q.opens != opens || q.finds != finds || len(q.calls) != calls {
					t.Fatal("unowned recovery changed pending state or called the native backend")
				}
			})
		}
	}
}

func TestDaemonRecoveryAbandonUnavailableBackendSkipsOnlyPendingLine(t *testing.T) {
	d, q, c, firstEnd := pendingRecoveryFixture(t)
	secondSize := appendDaemonMessage(t, c, "", "second message must remain deliverable")
	attempt := *c.Pending
	q.inspectErr, q.findErr = errors.New("backend unavailable"), errors.New("backend unavailable")
	opens := q.opens
	if _, err := d.abandon(AbandonRequest{Target: "dev/worker", ClientID: attempt.ClientID, Reason: "  explicit operator choice  "}); err != nil {
		t.Fatal(err)
	}
	if c.Offset != firstEnd || c.Pending != nil || c.Accepted != 0 || c.Abandoned != 1 || len(c.Resolutions) != 1 || q.opens != opens || q.finds != 0 || len(q.calls) != 1 {
		t.Fatalf("abandon touched more than its local pending line: state=%+v, queue=%+v", c, q)
	}
	audit := c.Resolutions[0]
	if audit.Attempt != attempt || audit.Start != 0 || audit.Reason != "explicit operator choice" || audit.At == "" {
		t.Fatalf("incomplete abandonment audit: %+v", audit)
	}
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	if len(c.Resolutions) != 1 || !reflect.DeepEqual(c.Resolutions[0], audit) || c.Offset != firstEnd || c.Abandoned != 1 {
		t.Fatalf("abandonment audit did not survive reload: %+v", c)
	}
	q.inspectErr, q.findErr = nil, nil
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 2 || strings.Contains(q.calls[1].text, "first unresolved") || !strings.Contains(q.calls[1].text, "second message") || c.Offset != firstEnd+secondSize || c.Accepted != 1 || c.Abandoned != 1 {
		t.Fatalf("abandonment resent or skipped the wrong line: calls=%+v, state=%+v", q.calls, c)
	}
}

func TestDaemonRecoverySaveFailureDoesNotPublishCopiedState(t *testing.T) {
	for _, action := range []string{"abandon", "pending-absent", "latest-received"} {
		t.Run(action, func(t *testing.T) {
			d, q, c, _ := pendingRecoveryFixture(t)
			pendingID := c.Pending.ClientID
			if action == "latest-received" {
				q.observation = &codexMessageLookup{State: codexMessageQueued, QueueID: "queue-1"}
				if _, err := d.reconcile("dev/worker"); err != nil {
					t.Fatal(err)
				}
				q.observation = &codexMessageLookup{State: codexMessageReceived, ItemID: "receipt"}
			}
			before := recoveryStateJSON(t, c)
			restore := blockDaemonJournal(t, d, c)
			var err error
			if action == "abandon" {
				_, err = d.abandon(AbandonRequest{Target: "dev/worker", ClientID: pendingID, Reason: "operator choice"})
			} else {
				_, err = d.reconcile("dev/worker")
			}
			restore()
			if err == nil || recoveryStateJSON(t, c) != before || len(q.calls) != 1 {
				t.Fatalf("failed save published copied state: state=%+v, err=%v", c, err)
			}
		})
	}
}

func TestDaemonRecoveryPositiveEvidenceSurvivesAcceptanceSaveFailure(t *testing.T) {
	for _, state := range []string{codexMessageQueued, codexMessageReceived} {
		t.Run(state, func(t *testing.T) {
			d, q, c, end := pendingRecoveryFixture(t)
			pendingID := c.Pending.ClientID
			observation := codexMessageLookup{State: state}
			if state == codexMessageReceived {
				observation.ItemID = "receipt"
			} else {
				observation.QueueID = "queued"
			}
			q.observation = &observation
			restore := blockDaemonJournal(t, d, c)
			_, err := d.reconcile("dev/worker")
			restore()
			if err == nil || c.Pending == nil || c.Pending.Evidence == nil || *c.Pending.Evidence != observation || c.Offset != 0 || c.Accepted != 0 {
				t.Fatalf("failed save discarded positive acceptance evidence: %+v, err=%v", c, err)
			}
			if _, err := d.abandon(AbandonRequest{Target: "dev/worker", ClientID: pendingID, Reason: "must be rejected"}); err == nil {
				t.Fatal("known positive evidence was abandoned")
			}
			finds := q.finds
			q.findErr = errors.New("runtime unavailable after positive observation")
			q.inspectErr = q.findErr
			delete(d.queues, c.ID)
			if _, err := d.reconcile("dev/worker"); err != nil || q.finds != finds || c.Pending != nil || c.Offset != end || c.Accepted != 1 || len(q.calls) != 1 {
				t.Fatalf("saving retained evidence required another lookup or enqueue: state=%+v, err=%v", c, err)
			}
		})
	}
}

func TestDaemonRecoveryAcknowledgedQueueIDNeedsNoBackendToPersist(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "acknowledged before journal failure")
	var restore func()
	q.afterEnqueue = func() { restore = blockDaemonJournal(t, d, c) }
	err := d.deliver(c)
	if restore == nil {
		t.Fatal("fixture did not reach native enqueue")
	}
	restore()
	q.afterEnqueue = nil
	if err == nil || c.Pending == nil || c.Pending.QueueID == "" || c.Offset != 0 {
		t.Fatalf("fixture did not preserve acknowledged queue ID: %+v, err=%v", c, err)
	}
	pendingID := c.Pending.ClientID
	if _, err := d.abandon(AbandonRequest{Target: "dev/worker", ClientID: pendingID, Reason: "must reject"}); err == nil {
		t.Fatal("acknowledged queue ID was abandoned")
	}
	delete(d.queues, c.ID)
	q.inspectErr, q.findErr = errors.New("backend unavailable"), errors.New("backend unavailable")
	opens := q.opens
	if _, err := d.reconcile("dev/worker"); err != nil || c.Pending != nil || c.Accepted != 1 || c.Offset != end || c.LastAccepted.Attempt.ClientID != pendingID || c.LastAccepted.Attempt.QueueID != "native-queue-id" || q.opens != opens || q.finds != 0 || len(q.calls) != 1 {
		t.Fatalf("known acknowledgement required another native operation: state=%+v, queue=%+v, err=%v", c, q, err)
	}
}

func TestDaemonRecoveryUnprovenRPCErrorStaysAmbiguous(t *testing.T) {
	for _, code := range []int{-32601, -32602, -32603, -32000} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			appendDaemonMessage(t, c, "", "possibly inserted before RPC error")
			q.enqueueErr = &rpcError{Code: code, Message: "RPC failure without verified pre-insertion semantics"}
			if err := d.deliver(c); err == nil || c.Pending == nil || c.Offset != 0 || c.Accepted != 0 {
				t.Fatalf("RPC error %d was treated as safe rejection: state=%+v, err=%v", code, c, err)
			}
			pendingID := c.Pending.ClientID
			d = reloadDaemonFixture(t, q)
			c = d.connections[c.ID]
			q.enqueueErr = nil
			if _, err := d.reconcile("dev/worker"); err != nil {
				t.Fatal(err)
			}
			if err := d.deliver(c); err == nil || c.Pending == nil || c.Pending.ClientID != pendingID || len(q.calls) != 1 || c.Offset != 0 || c.Accepted != 0 {
				t.Fatalf("ambiguous RPC failure was blindly retried: state=%+v, calls=%+v, err=%v", c, q.calls, err)
			}
		})
	}
}
