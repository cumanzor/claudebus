//go:build darwin || linux

package client

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Shape observed in Claude 2.1.278 when a native message arrives during Bash.
// The attachment record has its own UUID; source_uuid is the original frame UUID.
func claudeBusyReceiptRow(attempt string) map[string]any {
	return map[string]any{
		"type": "attachment", "sessionId": claudeTestSession, "isSidechain": false,
		"uuid": claudeMessageUUID("attachment-record"),
		"attachment": map[string]any{
			"type": "queued_command", "source_uuid": claudeMessageUUID(attempt),
			"commandMode": "prompt", "origin": map[string]any{"kind": "peer", "from": "unknown"},
			"isMeta": true, "prompt": "peer task",
		},
	}
}

func claudeBusyReceiptJSON(t *testing.T, row map[string]any) string {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func TestClaudeBusyReceiptRequiresExactPeerAttachment(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"exact", func(r, a map[string]any) {}},
		{"wrong session", func(r, a map[string]any) { r["sessionId"] = claudeMessageUUID("other") }},
		{"missing session", func(r, a map[string]any) { delete(r, "sessionId") }},
		{"sidechain", func(r, a map[string]any) { r["isSidechain"] = true }},
		{"missing sidechain classification", func(r, a map[string]any) { delete(r, "isSidechain") }},
		{"assistant", func(r, a map[string]any) { r["type"] = "assistant" }},
		{"wrong attachment type", func(r, a map[string]any) { a["type"] = "poll_events" }},
		{"wrong source uuid", func(r, a map[string]any) { a["source_uuid"] = claudeMessageUUID("other") }},
		{"only record uuid matches", func(r, a map[string]any) {
			r["uuid"] = claudeMessageUUID("attempt")
			delete(a, "source_uuid")
		}},
		{"wrong command mode", func(r, a map[string]any) { a["commandMode"] = "task-notification" }},
		{"missing command mode", func(r, a map[string]any) { delete(a, "commandMode") }},
		{"non-peer origin", func(r, a map[string]any) { a["origin"] = map[string]any{"kind": "unclassified"} }},
		{"missing origin", func(r, a map[string]any) { delete(a, "origin") }},
		{"non-meta", func(r, a map[string]any) { a["isMeta"] = false }},
		{"missing meta", func(r, a map[string]any) { delete(a, "isMeta") }},
		{"enqueue only", func(r, a map[string]any) { r["type"], r["operation"] = "queue-operation", "enqueue" }},
		{"absorbed remove only", func(r, a map[string]any) {
			r["type"], r["operation"], r["reason"] = "queue-operation", "remove", "absorbed_mid_turn"
		}},
		{"quoted receipt only", func(r, a map[string]any) {
			r["content"] = claudeBusyReceiptJSON(t, claudeBusyReceiptRow("attempt"))
			delete(r, "attachment")
		}},
		{"prompt uuid only", func(r, a map[string]any) {
			a["prompt"] = claudeMessageUUID("attempt")
			delete(a, "source_uuid")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := claudeBusyReceiptRow("attempt")
			tc.edit(row, row["attachment"].(map[string]any))
			path := filepath.Join(t.TempDir(), "transcript")
			data := claudeBusyReceiptJSON(t, row)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := observeClaudeReceipt(claudeTestContext(t), f, claudeTestSession, claudeMessageUUID("attempt"), 0, 1<<20)
			if err != nil || got.Observed != (tc.name == "exact") || got.NextOffset != int64(len(data)) {
				t.Fatalf("receipt = %+v, error = %v", got, err)
			}
		})
	}
}

func TestClaudeBusyReceiptPartialTailAndOfflineRestart(t *testing.T) {
	q, listener := claudeQueueFixture(t)
	row := claudeBusyReceiptJSON(t, claudeBusyReceiptRow("attempt"))
	appendClaudeQueueRow(t, q, row[:len(row)-1])
	if got, err := q.lookupMessage(claudeTestSession, "attempt"); err != nil || got.State != codexMessageNotFound || q.scanOffset != q.cfg.ReceiptOffset {
		t.Fatalf("incomplete attachment advanced receipt: %+v %v", got, err)
	}
	appendClaudeQueueRow(t, q, "\n")
	listener.Close()
	if err := os.Remove(filepath.Join(q.root, claudeCredentialDir, q.cfg.CredentialRef)); err != nil {
		t.Fatal(err)
	}
	restarted, err := newClaudeQueueContext(q.ctx, q.root, q.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	got, err := restarted.lookupMessage(claudeTestSession, "attempt")
	if err != nil || got.State != codexMessageReceived || got.ItemID != claudeMessageUUID("attempt") || got.ReceiptOffset == nil || *got.ReceiptOffset != q.cfg.ReceiptOffset+int64(len(row)) {
		t.Fatalf("offline attachment receipt lost: %+v %v", got, err)
	}
}

func TestDaemonClaudeBusyReceiptUnblocksFollowingDeliveryAfterRestart(t *testing.T) {
	d, c, q, listener := daemonClaudeFixture(t)
	wire := make(chan string, 2)
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := listener.AcceptUnix()
			if err != nil {
				wire <- ""
				return
			}
			b, _ := io.ReadAll(conn)
			conn.Close()
			wire <- string(b)
		}
	}()
	firstEnd := appendDaemonMessage(t, c, "", "busy task")
	var awaiting *claudeAwaitingReceiptError
	if err := d.deliver(c); !errors.As(err, &awaiting) || c.Pending == nil {
		t.Fatalf("first submission: %v", err)
	}
	firstAttempt := c.Pending.ClientID
	firstUUID := claudeMessageUUID(firstAttempt)
	if got := <-wire; !strings.Contains(got, firstUUID) {
		t.Fatal("first frame has wrong identity")
	}
	secondEnd := firstEnd + appendDaemonMessage(t, c, "", "following task")
	appendClaudeQueueRow(t, q, `{"type":"queue-operation","operation":"remove","reason":"absorbed_mid_turn","sessionId":"`+claudeTestSession+`"}`+"\n")
	if err := d.deliver(c); err == nil || c.Pending.ClientID != firstAttempt || c.Accepted != 0 || c.Offset != 0 {
		t.Fatal("queue removal released uncertain attempt")
	}
	appendClaudeQueueRow(t, q, claudeBusyReceiptJSON(t, claudeBusyReceiptRow(firstAttempt)))
	d.closeQueue(c.ID)
	restarted := newBusDaemon()
	restarted.start = d.start
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.closeQueue(c.ID); restarted.cancel() })
	c = restarted.connections[c.ID]
	if c == nil || c.Pending == nil || c.Pending.ClientID != firstAttempt {
		t.Fatal("restart lost the uncertain attempt")
	}
	if err := restarted.deliver(c); err != nil || c.Pending != nil || c.Accepted != 1 || c.Offset != firstEnd || c.LastAccepted.ItemID != firstUUID {
		t.Fatalf("attachment did not settle pending attempt: %+v %v", c, err)
	}
	if err := restarted.deliver(c); !errors.As(err, &awaiting) || c.Pending == nil || c.Pending.ClientID == firstAttempt {
		t.Fatalf("following message did not submit independently: %v", err)
	}
	secondAttempt := c.Pending.ClientID
	if got := <-wire; !strings.Contains(got, claudeMessageUUID(secondAttempt)) || strings.Contains(got, firstUUID) {
		t.Fatal("pending message was replayed instead of following message")
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(secondAttempt))
	if err := restarted.deliver(c); err != nil || c.Pending != nil || c.Accepted != 2 || c.Offset != secondEnd {
		t.Fatalf("following ordinary user receipt failed: %+v %v", c, err)
	}
}
