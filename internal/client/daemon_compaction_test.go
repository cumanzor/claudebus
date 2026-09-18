package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compactionFixture(t *testing.T) (*busDaemon, *daemonFakeQueue, *ConnectionState, string, string) {
	t.Helper()
	d, q, req := daemonFixture(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"`+req.ThreadID+`"}}`+"\n"+`{"type":"compacted","payload":{"message":"historical private summary"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	q.thread.Path = path
	inbox := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	return d, q, c, path, inbox
}

func appendRollout(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonCompactionBaselinePartialTailAndRestart(t *testing.T) {
	d, q, c, path, inbox := compactionFixture(t)
	if len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("replayed historical compaction")
	}
	before := c.Compaction.Offset
	appendRollout(t, path, `{"type":"compacted"`)
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if c.Compaction.Offset != before || len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("consumed incomplete physical tail")
	}
	appendRollout(t, path, `,"payload":{"message":"PRIVATE SUMMARY CONTENT"}}`+"\n"+`{"type":"event_msg","payload":{"type":"context_compacted"}}`+"\n")
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	messages := presenceMessages(t, inbox)
	if len(messages) != 1 || messages[0].Event != "compact-post" || strings.Contains(messages[0].Text, "PRIVATE") || c.Compaction.Completed != 1 {
		t.Fatalf("completion fanout: %+v %+v", messages, c.Compaction)
	}
	d2 := reloadDaemonFixture(t, q)
	c = d2.connections[c.ID]
	if err := d2.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 1 {
		t.Fatal("restart duplicated completed compaction")
	}
	if len(q.calls) != 0 {
		t.Fatal("compaction observation invoked native queue")
	}
}

func TestDaemonCompactionCursorAndOutboxCommitTogether(t *testing.T) {
	d, _, c, path, inbox := compactionFixture(t)
	before := c.Compaction.Offset
	appendRollout(t, path, `{"type":"compacted","payload":{}}`+"\n")
	restore := blockDaemonJournal(t, d, c)
	err := d.observeCompaction(c)
	restore()
	if err == nil || c.Compaction.Offset != before || c.Compaction.Completed != 0 || len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("failed journal save advanced cursor or emitted completion")
	}
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 1 || c.Compaction.Completed != 1 {
		t.Fatal("retry lost or duplicated completion")
	}
}

func TestDaemonCompactionDisconnectedAndReplacementBaseline(t *testing.T) {
	d, _, c, path, inbox := compactionFixture(t)
	c.State = "disconnected"
	appendRollout(t, path, `{"type":"compacted","payload":{}}`+"\n")
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	c.State = "queue-ready"
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("disconnect-period compaction replayed")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("replacement history replayed compactions")
	}
	appendRollout(t, path, `{"type":"compacted","payload":{}}`+"\n")
	if err := d.observeCompaction(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 1 {
		t.Fatal("replacement baseline lost future completion")
	}
}

func TestDaemonCompactionCorruptRecordDoesNotAdvance(t *testing.T) {
	d, _, c, path, inbox := compactionFixture(t)
	before := c.Compaction.Offset
	appendRollout(t, path, "{invalid completed record}\n")
	if err := d.observeCompaction(c); err == nil {
		t.Fatal("corrupt complete record accepted")
	}
	if c.Compaction.Offset != before || len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("corrupt record advanced observation")
	}
}

func TestCompactionOpenedDescriptorMustMatchThread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"different-thread"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := validateCompactionFile(f, daemonTestThread); err == nil {
		t.Fatal("replacement descriptor borrowed previously validated identity")
	}
}
