//go:build darwin || linux

package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeQueueFixture(t *testing.T) (*claudeQueue, *net.UnixListener) {
	t.Helper()
	endpoint, listener, _ := testClaudeEndpoint(t)
	root, ref, _ := claudeCredentialFixture(t)
	path := filepath.Join(t.TempDir(), claudeTestSession+".jsonl")
	data := []byte(`{"type":"mode","sessionId":"` + claudeTestSession + `"}` + "\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	dev, ino, size, ok := fileIdentity(path)
	if !ok {
		t.Fatal("transcript identity missing")
	}
	cfg := ClaudeConnectionConfig{Binding: ClaudeConnectBinding{SessionID: claudeTestSession, Cwd: filepath.Dir(path), TranscriptPath: path, TranscriptDev: dev, TranscriptIno: ino, TranscriptSize: size, TranscriptOffset: size, Endpoint: endpoint}, CredentialRef: ref, ReceiptOffset: size}
	queue, err := newClaudeQueueContext(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { queue.Close() })
	return queue.(*claudeQueue), listener
}

func appendClaudeQueueRow(t *testing.T, q *claudeQueue, data string) {
	t.Helper()
	f, err := os.OpenFile(q.cfg.Binding.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
}

func claudeQueueReceiptRow(attempt string) string {
	return `{"type":"user","sessionId":"` + claudeTestSession + `","uuid":"` + claudeMessageUUID(attempt) + `"}` + "\n"
}

func TestClaudeQueueSubmissionNeedsSeparateExactReceipt(t *testing.T) {
	q, listener := claudeQueueFixture(t)
	thread, err := q.inspect(claudeTestSession)
	if err != nil || thread.ID != claudeTestSession || thread.Path != q.cfg.Binding.TranscriptPath || thread.CliVersion != "" || thread.Source != "cli" {
		t.Fatalf("inspect: %+v %v", thread, err)
	}
	wire := make(chan []byte, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			wire <- nil
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(conn)
		wire <- data
	}()
	id, err := q.enqueue(claudeTestSession, "attempt", "peer task")
	var awaiting *claudeAwaitingReceiptError
	if id != "" || !errors.As(err, &awaiting) || awaiting.State != claudeSubmitted {
		t.Fatalf("invented acceptance: %q %v", id, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(<-wire), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatal("native envelope missing")
	}
	var auth map[string]string
	var message struct {
		UUID      string
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(lines[0]), &auth) != nil || auth["token"] != "fixture-secret" || json.Unmarshal([]byte(lines[1]), &message) != nil || message.UUID != claudeMessageUUID("attempt") || message.SessionID != claudeTestSession {
		t.Fatal("incorrect native target or credential")
	}
	if found, err := q.lookupMessage(claudeTestSession, "attempt"); err != nil || found.State != codexMessageNotFound || found.ReceiptOffset != nil {
		t.Fatalf("absence became proof: %+v %v", found, err)
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow("different-attempt"))
	if found, err := q.lookupMessage(claudeTestSession, "attempt"); err != nil || found.State != codexMessageNotFound {
		t.Fatalf("wrong attempt receipt: %+v %v", found, err)
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow("attempt"))
	found, err := q.lookupMessage(claudeTestSession, "attempt")
	if err != nil || found.State != codexMessageReceived || found.QueueID != "" || found.ItemID != message.UUID || found.ReceiptOffset == nil || *found.ReceiptOffset <= q.cfg.ReceiptOffset {
		t.Fatalf("missing exact receipt: %+v %v", found, err)
	}
}

func TestClaudeQueueOfflineRecoveryDoesNotNeedEndpointOrCredential(t *testing.T) {
	q, listener := claudeQueueFixture(t)
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow("attempt"))
	listener.Close()
	if err := os.Remove(filepath.Join(q.root, claudeCredentialDir, q.cfg.CredentialRef)); err != nil {
		t.Fatal(err)
	}
	recovery, err := newClaudeQueueContext(context.Background(), q.root, q.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	if found, err := recovery.lookupMessage(claudeTestSession, "attempt"); err != nil || found.State != codexMessageReceived {
		t.Fatalf("offline receipt lost: %+v %v", found, err)
	}
	var rejected *claudeNotSubmittedError
	if id, err := recovery.enqueue(claudeTestSession, "new-attempt", "task"); id != "" || !errors.As(err, &rejected) {
		t.Fatal("missing credential not classified before submission")
	}
	if _, err := recovery.lookupMessage(claudeMessageUUID("other-session"), "attempt"); err == nil {
		t.Fatal("accepted another thread")
	}
	recovery.Close()
	if _, err := recovery.inspect(claudeTestSession); !errors.Is(err, context.Canceled) {
		t.Fatal("closed adapter remained active")
	}
}

func TestClaudeQueueRejectsChangedTranscriptAndEndpointBeforeSubmission(t *testing.T) {
	for _, change := range []string{"transcript", "endpoint", "session"} {
		t.Run(change, func(t *testing.T) {
			q, _ := claudeQueueFixture(t)
			session := claudeTestSession
			switch change {
			case "transcript":
				path := q.cfg.Binding.TranscriptPath
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "endpoint":
				q.cfg.Binding.Endpoint.StartToken += "-stale"
			case "session":
				session = claudeMessageUUID("wrong-session")
			}
			var rejected *claudeNotSubmittedError
			if id, err := q.enqueue(session, "attempt", "task"); id != "" || !errors.As(err, &rejected) {
				t.Fatalf("binding failure was not definite: %q %v", id, err)
			}
		})
	}
}

func TestClaudeQueueReceiptScanProgressAndPartialTail(t *testing.T) {
	q, _ := claudeQueueFixture(t)
	padding := `{"type":"assistant"}` + "\n"
	appendClaudeQueueRow(t, q, strings.Repeat(padding, 3))
	row := claudeQueueReceiptRow("attempt")
	appendClaudeQueueRow(t, q, row[:len(row)-1])
	q.scanBytes = int64(len(padding) * 2)
	if found, err := q.lookupMessage(claudeTestSession, "attempt"); err != nil || found.State != codexMessageNotFound || q.scanOffset <= q.cfg.ReceiptOffset {
		t.Fatalf("budget made no progress: %+v %v", found, err)
	}
	q.scanBytes = claudeMaxScan
	if found, err := q.lookupMessage(claudeTestSession, "attempt"); err != nil || found.State != codexMessageNotFound {
		t.Fatalf("partial row accepted: %+v %v", found, err)
	}
	appendClaudeQueueRow(t, q, "\n")
	if found, err := q.lookupMessage(claudeTestSession, "attempt"); err != nil || found.State != codexMessageReceived {
		t.Fatalf("completed row not found: %+v %v", found, err)
	}
	q.scanAttempt, q.scanBytes = "", 1
	if _, err := q.lookupMessage(claudeTestSession, "attempt"); err == nil {
		t.Fatal("unadvanceable budget silently stalled")
	}
}
