//go:build darwin || linux

package client

import (
	"context"
	"testing"
)

func transcriptDev(t *testing.T, path string) uint64 {
	t.Helper()
	dev, _, _, ok := fileIdentity(path)
	if !ok {
		t.Fatal("stat transcript")
	}
	return dev
}

func TestClaudeReconnectRestampsRenumberedTranscript(t *testing.T) {
	d, c, req := admittedClaude(t)
	c.Claude.Binding.TranscriptDev = renumberedDev
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	got, err := d.connectWithCredential(req, admissionTestToken)
	if err != nil {
		t.Fatalf("dev-only transcript change was refused: %v", err)
	}
	if got.Claude.Binding.TranscriptDev != transcriptDev(t, req.Claude.TranscriptPath) || got.Claude.Binding.TranscriptIno != req.Claude.TranscriptIno {
		t.Fatalf("transcript identity was not re-stamped: %+v", got.Claude.Binding)
	}
}

// Receipt lookups open the journaled transcript binding, so a pending attempt must
// stay reconcilable across a renumbering restart, and the cursors must survive.
func TestClaudeReceiptsSurviveRenumberedRestart(t *testing.T) {
	d, c, q, wire := claudeWireFixture(t)
	appendDaemonMessage(t, c, "", "before reboot")
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	<-wire
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(c.Pending.ClientID))
	if err := d.deliver(c); err != nil || c.Accepted != 1 {
		t.Fatalf("first receipt: %v %+v", err, c)
	}
	end := appendDaemonMessage(t, c, "", "pending across reboot") + c.Offset
	if err := d.deliver(c); err != nil || c.Pending == nil {
		t.Fatalf("second submission: %v", err)
	}
	<-wire
	attempt := c.Pending.ClientID
	c.Dev, c.Claude.Binding.TranscriptDev = renumberedDev, renumberedDev
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d.closeQueue(c.ID)
	restarted := newBusDaemon()
	restarted.start = d.start
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.closeQueue(c.ID); restarted.cancel() })
	c = restarted.connections[c.ID]
	restarted.rearmLoaded(context.Background())
	if c.ListenerError != "" {
		t.Fatalf("rearm refused a dev-only change: %s", c.ListenerError)
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(attempt))
	if err := restarted.deliver(c); err != nil || c.Pending != nil || c.Offset != end || c.Accepted != 2 {
		t.Fatalf("pending receipt was stranded by the renumbered transcript: %v %+v", err, c)
	}
	accepted, cursor := *c.LastAccepted, c.Claude.ReceiptOffset
	restarted.closeQueue(c.ID)
	again := newBusDaemon()
	again.start = d.start
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { again.cancel() })
	c = again.connections[c.ID]
	if c.LastAccepted.Attempt != accepted.Attempt || c.Claude.ReceiptOffset != cursor || c.Offset != end {
		t.Fatalf("restart after re-stamp lost receipt history: %+v", c)
	}
	if again.rearmLoaded(context.Background()); c.ListenerError != "" {
		t.Fatalf("re-stamped inbox was refused after restart: %s", c.ListenerError)
	}
}

func TestPresenceReplayDeliversToRenumberedRecipient(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	before := len(presenceMessages(t, inbox))
	if err := d.preparePresence(c, "join", "event"); err != nil {
		t.Fatal(err)
	}
	c.PresenceOutbox[len(c.PresenceOutbox)-1].Recipients[0].Dev = renumberedDev
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	if err := d.flushPresence(c); err != nil {
		t.Fatal(err)
	}
	if got := len(presenceMessages(t, inbox)) - before; got != 1 || len(c.PresenceOutbox) != 0 {
		t.Fatalf("presence to a renumbered recipient was dropped: delivered %d", got)
	}
}

func TestCompactionKeepsCursorAcrossRenumberedRollout(t *testing.T) {
	d, q, c, path, inbox := compactionFixture(t)
	offset := c.Compaction.Offset
	c.Compaction.Dev = renumberedDev
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	appendRollout(t, path, `{"type":"compacted","payload":{"message":"while down"}}`+"\n")
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 1 || c.Compaction.Completed != 1 || c.Compaction.Offset <= offset {
		t.Fatalf("renumbered rollout re-baselined past a compaction: %+v", c.Compaction)
	}
	if c.Compaction.Dev != transcriptDev(t, path) {
		t.Fatal("rollout identity was not re-stamped")
	}
}
