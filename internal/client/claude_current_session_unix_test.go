//go:build darwin || linux

package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeClaudeSessionRegistry(t *testing.T, b ClaudeConnectBinding) string {
	t.Helper()
	dir := filepath.Join(b.ConfigHome, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"pid": b.Endpoint.PID, "sessionId": b.SessionID, "kind": "interactive", "messagingSocketPath": b.Endpoint.Socket})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(b.Endpoint.PID)+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeCurrentSessionRegistryIsExactAndFailClosed(t *testing.T) {
	for _, kind := range []string{"missing", "malformed", "partial", "oversize", "symlink", "writable", "directory writable", "PID", "socket", "kind", "session"} {
		t.Run(kind, func(t *testing.T) {
			q, _ := claudeQueueFixture(t)
			b := q.cfg.Binding
			path := writeClaudeSessionRegistry(t, b)
			if err := validateCurrentClaudeSession(b); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".saved", path); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			case "directory writable":
				if err := os.Chmod(filepath.Dir(path), 0777); err != nil {
					t.Fatal(err)
				}
			default:
				switch kind {
				case "PID":
					record["pid"] = b.Endpoint.PID + 1
				case "socket":
					record["messagingSocketPath"] = b.Endpoint.Socket + ".other"
				case "kind":
					record["kind"] = "headless"
				case "session":
					record["sessionId"] = daemonTestThread
				}
				data, _ = json.Marshal(record)
				if kind == "malformed" {
					data = []byte("not JSON")
				}
				if kind == "partial" {
					data = []byte(`{"pid":`)
				}
				if kind == "oversize" {
					data = []byte(strings.Repeat(" ", (64<<10)+1))
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			err := validateCurrentClaudeSession(b)
			if err == nil || errors.Is(err, errClaudeSessionChanged) != (kind == "session") {
				t.Fatalf("unsafe registry classification: %v", err)
			}
		})
	}
}

func TestClaudeCurrentSessionSwitchStopsSendButRetainsOldReceipts(t *testing.T) {
	q, listener := claudeQueueFixture(t)
	b := q.cfg.Binding
	other := b
	other.SessionID = daemonTestThread
	writeClaudeSessionRegistry(t, other) // Same native process and socket, new SID.
	var rejected *claudeNotSubmittedError
	if _, err := q.enqueue(b.SessionID, "old-attempt", "must not reach another session"); !errors.As(err, &rejected) || !errors.Is(err, errClaudeSessionChanged) {
		t.Fatalf("old SID send was not fenced: %v", err)
	}
	listener.SetDeadline(time.Now().Add(20 * time.Millisecond))
	if conn, err := listener.AcceptUnix(); err == nil {
		conn.Close()
		t.Fatal("stale SID reached the socket")
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow("old-attempt"))
	seen, err := q.lookupMessage(b.SessionID, "old-attempt")
	if err != nil || seen.State != codexMessageReceived {
		t.Fatalf("old receipt lost after session switch: %+v %v", seen, err)
	}
}

func TestClaudeCurrentSessionSwitchChangesConsumerAndRefusesAdmission(t *testing.T) {
	d, c, req := admittedClaude(t)
	b := *req.Claude
	b.SessionID = "22222222-2222-4222-8222-222222222222"
	path := writeClaudeSessionRegistry(t, b)
	p, err := d.consumerProbe(c)
	if err != nil || p.State != "exited" {
		t.Fatalf("old SID remained online: %+v %v", p, err)
	}
	before := cloneConnection(c)
	if _, err := d.connectWithCredential(req, admissionTestToken); err == nil {
		t.Fatal("historical transcript allowed admission to switched session")
	}
	if c.Claude.CredentialRef != before.Claude.CredentialRef || c.Offset != before.Offset {
		t.Fatal("failed switched-session admission changed state")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	p, err = d.consumerProbe(c)
	if err == nil || p.State != "unknown" {
		t.Fatal("missing registry falsely confirmed departure")
	}
}
