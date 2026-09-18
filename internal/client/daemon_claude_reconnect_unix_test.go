//go:build darwin || linux

package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const admissionTestToken = "fixture-initial-capability"

func admittedClaude(t *testing.T) (*busDaemon, *ConnectionState, ConnectRequest) {
	t.Helper()
	d, _, req := claudeAdmissionFixture(t)
	got, err := d.connectWithCredential(req, admissionTestToken)
	if err != nil {
		t.Fatal(err)
	}
	return d, d.connections[got.ID], req
}

func uncertainClaude(t *testing.T, d *busDaemon, c *ConnectionState) (*daemonFakeQueue, int64) {
	t.Helper()
	end := appendDaemonMessage(t, c, "", "uncertain native submission")
	q := &daemonFakeQueue{enqueueErr: errors.New("submission outcome unknown")}
	d.closeQueue(c.ID)
	if err := d.cacheQueue(c.ID, q); err != nil {
		t.Fatal(err)
	}
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatal("fixture did not retain uncertain attempt")
	}
	return q, end
}

func appendAdmissionTranscript(t *testing.T, req ConnectRequest, text string) {
	t.Helper()
	f, err := os.OpenFile(req.Claude.TranscriptPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeReconnectSameEpochRetainsPendingAndReceiptCursor(t *testing.T) {
	d, c, req := admittedClaude(t)
	q, _ := uncertainClaude(t, d, c)
	before := cloneConnection(c)
	appendAdmissionTranscript(t, req, `{"type":"assistant","sessionId":"`+req.ThreadID+`"}`)
	b := *req.Claude
	b.Cwd = t.TempDir()
	_, _, b.TranscriptSize, _ = fileIdentity(b.TranscriptPath)
	b.TranscriptOffset = b.TranscriptSize
	req.Claude = &b
	got, err := d.connectWithCredential(req, admissionTestToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != before.ID || got.Dev != before.Dev || got.Ino != before.Ino || got.Offset != before.Offset || !reflect.DeepEqual(got.Pending, before.Pending) || got.State != "uncertain" || len(q.calls) != 1 {
		t.Fatal("same-epoch reconnect changed or replayed pending inbox mail")
	}
	if got.Claude.CredentialRef != before.Claude.CredentialRef || got.Claude.ReceiptOffset != before.Claude.ReceiptOffset || got.Claude.ReceiptOffset == b.TranscriptOffset {
		t.Fatal("reconnect rotated an unchanged credential or skipped unobserved transcript bytes")
	}
	m, _ := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
	if m.Cwd != b.Cwd || m.Harness != "claude" {
		t.Fatal("resume metadata lost actual Claude cwd/harness")
	}
}

func TestClaudeReconnectChangedEpochWaitsForExactReceipt(t *testing.T) {
	d, c, req := admittedClaude(t)
	q, end := uncertainClaude(t, d, c)
	old := cloneConnection(c)
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	before, _ := os.ReadFile(journal)
	next := req
	b := *req.Claude
	b.Endpoint, _, _ = testClaudeEndpoint(t)
	next.Claude = &b
	for _, tc := range []struct {
		request ConnectRequest
		token   string
	}{{next, admissionTestToken}, {req, "fixture-new-capability"}} {
		if _, err := d.connectWithCredential(tc.request, tc.token); err == nil || !strings.Contains(err.Error(), "unresolved pending") {
			t.Fatal("changed runtime/capability redirected pending mail")
		}
		after, _ := os.ReadFile(journal)
		if string(after) != string(before) || !reflect.DeepEqual(cloneConnection(c), old) || len(q.calls) != 1 {
			t.Fatal("rejected reconnect changed the original epoch")
		}
	}
	appendAdmissionTranscript(t, req, fmt.Sprintf(`{"type":"user","sessionId":%q,"uuid":%q}`, req.ThreadID, claudeMessageUUID(c.Pending.ClientID)))
	if _, err := d.reconcile(ConnectionTarget(c)); err != nil {
		t.Fatal(err)
	}
	receiptCursor := c.Claude.ReceiptOffset
	appendAdmissionTranscript(t, req, `{"type":"assistant","sessionId":"`+req.ThreadID+`"}`)
	_, _, b.TranscriptSize, _ = fileIdentity(b.TranscriptPath)
	b.TranscriptOffset = b.TranscriptSize
	got, err := d.connectWithCredential(next, "fixture-new-capability")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != old.ID || got.Offset != end || got.Accepted != 1 || got.Pending != nil || got.Claude.ReceiptOffset != receiptCursor || got.Claude.CredentialRef == old.Claude.CredentialRef || got.Claude.Binding.Endpoint != b.Endpoint || len(q.calls) != 1 {
		t.Fatal("positive receipt did not allow a new runtime epoch without replay")
	}
	if token, err := readClaudeCredential(d.root, got.Claude.CredentialRef); err != nil || token != "fixture-new-capability" {
		t.Fatal("new epoch credential unavailable")
	}
}

func TestClaudeReconnectAfterExplicitAbandonment(t *testing.T) {
	d, c, req := admittedClaude(t)
	q, end := uncertainClaude(t, d, c)
	if _, err := d.abandon(AbandonRequest{Target: ConnectionTarget(c), ClientID: c.Pending.ClientID, Reason: "operator accepts unresolved delivery"}); err != nil {
		t.Fatal(err)
	}
	b := *req.Claude
	b.Endpoint, _, _ = testClaudeEndpoint(t)
	req.Claude = &b
	got, err := d.connectWithCredential(req, "fixture-resumed-capability")
	if err != nil || got.Pending != nil || got.Offset != end || got.Abandoned != 1 || len(q.calls) != 1 {
		t.Fatalf("explicit abandonment could not rebind safely: %v", err)
	}
}

func TestClaudeReconnectRefusesTranscriptAndAliasRedirect(t *testing.T) {
	for _, kind := range []string{"transcript inode", "transcript path", "alias", "owner", "inbox"} {
		t.Run(kind, func(t *testing.T) {
			d, c, req := admittedClaude(t)
			before := cloneConnection(c)
			switch kind {
			case "transcript inode":
				req.Claude.TranscriptIno++
			case "transcript path":
				req.Claude.TranscriptPath += ".different"
			case "alias":
				req.Alias = "other-alias"
			case "owner":
				data, _ := os.ReadFile(filepath.Join(d.peerDir(c), "meta.json"))
				var m peerMeta
				if err := json.Unmarshal(data, &m); err != nil {
					t.Fatal(err)
				}
				m.ConnectionID = "replacement"
				if err := writeDaemonMeta(d.peerDir(c), m); err != nil {
					t.Fatal(err)
				}
			case "inbox":
				path := filepath.Join(d.peerDir(c), "inbox.jsonl")
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.connectWithCredential(req, admissionTestToken); err == nil {
				t.Fatal("redirected exact managed connection")
			}
			if !reflect.DeepEqual(cloneConnection(c), before) {
				t.Fatal("rejected rebind changed existing state")
			}
		})
	}
}

func TestClaudeReconnectSaveFailureRetainsOldBindingAndCapability(t *testing.T) {
	d, c, req := admittedClaude(t)
	old := cloneConnection(c)
	b := *req.Claude
	b.Endpoint, _, _ = testClaudeEndpoint(t)
	req.Claude = &b
	restore := blockDaemonJournal(t, d, c)
	_, err := d.connectWithCredential(req, "fixture-new-capability")
	restore()
	if err == nil || !reflect.DeepEqual(cloneConnection(c), old) || d.cachedQueue(c.ID) != nil {
		t.Fatal("failed journal save replaced live binding or retained candidate queue")
	}
	if token, err := readClaudeCredential(d.root, old.Claude.CredentialRef); err != nil || token != admissionTestToken {
		t.Fatal("failed reconnect overwrote prior immutable capability")
	}
	if _, err := d.connectWithCredential(req, "fixture-new-capability"); err != nil {
		t.Fatal(err)
	}
}
