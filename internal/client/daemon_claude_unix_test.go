//go:build darwin || linux

package client

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Admission remains off in this milestone. Build an internal Claude connection
// from a normal owned inbox, then exercise real socket/credential/receipt paths.
func daemonClaudeFixture(t *testing.T) (*busDaemon, *ConnectionState, *claudeQueue, *net.UnixListener) {
	t.Helper()
	d, legacy, req := daemonFixture(t)
	req.ThreadID, legacy.thread.ID = claudeTestSession, claudeTestSession
	c := mustDaemonConnect(t, d, req)
	d.closeQueue(c.ID)
	q, listener := claudeQueueFixture(t)
	ref, err := storeClaudeCredential(d.root, claudeMessageUUID("daemon-binding"), "fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := q.cfg
	cfg.CredentialRef = ref
	cfg.Binding.ConfigHome, cfg.Binding.UserHome = d.root, d.root
	writeClaudeSessionRegistry(t, cfg.Binding)
	c.Harness, c.Claude, c.RolloutPath = daemonHarnessClaude, &cfg, cfg.Binding.TranscriptPath
	d.probeConsumer = nil
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.closeQueue(c.ID); d.cancel() })
	return d, c, q, listener
}

func TestDaemonClaudeReceiptAloneAdvancesBothCursors(t *testing.T) {
	d, c, q, listener := daemonClaudeFixture(t)
	wire := make(chan []byte, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			wire <- nil
			return
		}
		defer conn.Close()
		b, _ := io.ReadAll(conn)
		wire <- b
	}()
	end := appendDaemonMessage(t, c, "", "one actual submission")
	var awaiting *claudeAwaitingReceiptError
	if err := d.scheduledOperation(c); !errors.As(err, &awaiting) || c.Pending == nil || c.Accepted != 0 || c.Offset != 0 {
		t.Fatalf("socket write became acceptance: %v", err)
	}
	if len(<-wire) == 0 {
		t.Fatal("message did not reach socket fixture")
	}
	if err := d.accept(c, nil); err == nil {
		t.Fatal("accepted socket submission without receipt")
	}
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatal("absent receipt released uncertainty")
	}
	listener.SetDeadline(time.Now().Add(30 * time.Millisecond))
	if conn, err := listener.AcceptUnix(); err == nil {
		conn.Close()
		t.Fatal("uncertain message was replayed")
	}
	listener.SetDeadline(time.Time{})
	// Reload the write-ahead attempt before observing receipt. A daemon restart
	// must dispatch Claude recovery without treating the socket as a Codex queue.
	d.closeQueue(c.ID)
	listener.Close() // Receipt recovery does not require a live socket.
	restarted := newBusDaemon()
	restarted.start = d.start
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.closeQueue(c.ID); restarted.cancel() })
	d, c = restarted, restarted.connections[c.ID]
	if c == nil || c.Pending == nil || c.Harness != daemonHarnessClaude {
		t.Fatal("restart lost native pending attempt")
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(c.Pending.ClientID))
	oldCursor := c.Claude.ReceiptOffset
	restore := blockDaemonJournal(t, d, c)
	if err := d.deliver(c); err == nil || c.Pending == nil || c.Claude.ReceiptOffset != oldCursor || c.Offset != 0 {
		t.Fatal("failed journal update advanced a cursor")
	}
	restore()
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if c.Pending != nil || c.Offset != end || c.Accepted != 1 || c.State != "socket-ready" || c.Claude.ReceiptOffset <= oldCursor || c.LastAccepted.State != "received" || c.LastQueueID != "" {
		t.Fatal("positive receipt did not commit cursor/observation atomically")
	}
	b, err := os.ReadFile(filepath.Join(d.root, "connections", c.ID+".json"))
	var saved ConnectionState
	if err != nil || json.Unmarshal(b, &saved) != nil || saved.Offset != c.Offset || saved.Claude.ReceiptOffset != c.Claude.ReceiptOffset || strings.Contains(string(b), "fixture-secret") {
		t.Fatal("journal lost checkpoint or leaked credential")
	}
	snapshot := d.snapshot(c.ID)
	snapshot.Claude.ReceiptOffset = 0
	if c.Claude.ReceiptOffset == 0 {
		t.Fatal("status snapshot aliases live adapter config")
	}
}

func TestDaemonClaudeClearsOnlyKnownPreSubmissionFailure(t *testing.T) {
	t.Run("missing credential", func(t *testing.T) {
		d, c, _, _ := daemonClaudeFixture(t)
		if err := os.Remove(filepath.Join(d.root, claudeCredentialDir, c.Claude.CredentialRef)); err != nil {
			t.Fatal(err)
		}
		appendDaemonMessage(t, c, "", "not submitted")
		var rejected *claudeNotSubmittedError
		if err := d.deliver(c); !errors.As(err, &rejected) || c.Pending != nil || c.Offset != 0 || c.Accepted != 0 {
			t.Fatalf("known pre-send failure lost mail or retained false submission: %v", err)
		}
	})
	t.Run("Codex RPC code is not Claude evidence", func(t *testing.T) {
		d, c, _, _ := daemonClaudeFixture(t)
		fake := &daemonFakeQueue{enqueueErr: &rpcError{Code: -32600, Message: "unrelated protocol"}}
		if err := d.cacheQueue(c.ID, fake); err != nil {
			t.Fatal(err)
		}
		appendDaemonMessage(t, c, "", "uncertain")
		if err := d.deliver(c); err == nil || c.Pending == nil || c.Offset != 0 {
			t.Fatal("Codex error code was mistaken for Claude rejection proof")
		}
	})
}

func TestDaemonClaudeConsumerAndCompactionDispatch(t *testing.T) {
	d, c, _, listener := daemonClaudeFixture(t)
	p, err := d.consumerProbe(c)
	if err != nil || p.State != "online" {
		t.Fatalf("bound consumer: %+v %v", p, err)
	}
	listener.Close()
	p, err = d.consumerProbe(c)
	if err == nil || p.State != "unknown" {
		t.Fatal("missing socket became a confirmed process exit")
	}
	c.Claude.Binding.Endpoint.StartToken += "-old-incarnation"
	p, err = d.consumerProbe(c)
	if err != nil || p.State != "exited" {
		t.Fatalf("stale owner: %+v %v", p, err)
	}
	c.RolloutPath = "not-a-Codex-rollout"
	if err := d.observeCompaction(c); err != nil {
		t.Fatal("Claude entered Codex compaction parser")
	}
}
