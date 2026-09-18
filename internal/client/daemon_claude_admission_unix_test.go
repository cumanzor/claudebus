//go:build darwin || linux

package client

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeAdmissionFixture(t *testing.T) (*busDaemon, *daemonFakeQueue, ConnectRequest) {
	t.Helper()
	d, q, req := daemonFixture(t)
	_, runtime := claudeCallerFixture(t)
	binding, err := claudeConnectIdentity(runtime)
	if err != nil {
		t.Fatal(err)
	}
	d.probeConsumer = nil
	req.Protocol, req.Harness, req.Config, req.Claude = DaemonProtocolVersion, daemonHarnessClaude, CodexQueueConfig{}, &binding
	t.Cleanup(func() {
		for _, c := range d.statusSnapshots() {
			d.closeQueue(c.ID)
		}
		d.cancel()
	})
	return d, q, req
}

func TestClaudeFreshAdmissionPrivateCapabilityAndExactMetadata(t *testing.T) {
	d, codex, req := claudeAdmissionFixture(t)
	const token = "fixture-native-capability"
	r := httptest.NewRecorder()
	d.handler(func() {}).ServeHTTP(r, httptest.NewRequest("POST", "/connect", strings.NewReader(string(mustBindingJSON(t, connectWireRequest{req, token})))))
	var c ConnectionState
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &c) != nil || c.Claude == nil || c.State != "socket-ready" || codex.opens != 0 {
		t.Fatalf("fresh admission failed: %d %s", r.Code, r.Body.String())
	}
	if c.Claude.ReceiptOffset != req.Claude.TranscriptOffset || c.Config != (CodexQueueConfig{}) || c.RecordedVersion != "" || c.Accepted != 0 || c.Pending != nil {
		t.Fatal("fresh admission invented Codex configuration, receipt, or runtime version")
	}
	m, ok := ReadPeerMeta(filepath.Join(d.peerDir(&c), "meta.json"))
	if !ok || m.Harness != "claude" || m.SessionID != req.ThreadID || m.Cwd != req.Claude.Cwd || m.ConnectionID != c.ID {
		t.Fatal("peer metadata did not retain exact Claude identity")
	}
	secret, err := readClaudeCredential(d.root, c.Claude.CredentialRef)
	if err != nil || secret != token {
		t.Fatal("private credential was not stored before admission")
	}
	journal, _ := os.ReadFile(filepath.Join(d.root, "connections", c.ID+".json"))
	if strings.Contains(r.Body.String(), token) || strings.Contains(string(journal), token) {
		t.Fatal("status or journal leaked native capability")
	}
}

func TestClaudeFreshAdmissionRejectsInvalidBindingBeforeMutation(t *testing.T) {
	for _, kind := range []string{"token", "session", "endpoint", "transcript", "cursor", "codex config"} {
		t.Run(kind, func(t *testing.T) {
			d, codex, req := claudeAdmissionFixture(t)
			token := "fixture-secret"
			switch kind {
			case "token":
				token += "\n"
			case "session":
				req.ThreadID = "22222222-2222-4222-8222-222222222222"
			case "endpoint":
				req.Claude.Endpoint.StartToken += "-stale"
			case "transcript":
				req.Claude.TranscriptIno++
			case "cursor":
				req.Claude.TranscriptOffset = 0
			case "codex config":
				req.Config.Home = t.TempDir()
			}
			if _, err := d.connectWithCredential(req, token); err == nil {
				t.Fatal("invalid caller admitted")
			}
			if len(d.statusSnapshots()) != 0 || codex.opens != 0 || dirExists(filepath.Join(CBUSDir(), req.Channel)) || dirExists(filepath.Join(d.root, claudeCredentialDir)) {
				t.Fatal("invalid caller claimed alias, opened Codex, or persisted capability")
			}
		})
	}
}

func TestClaudeFreshAdmissionRefusesUnmanagedAliasWithoutMutation(t *testing.T) {
	d, _, req := claudeAdmissionFixture(t)
	dir := filepath.Join(CBUSDir(), req.Channel, req.Alias)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"sessionId":"` + req.ThreadID + `","harness":"claude"}`)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inbox.jsonl"), []byte("legacy mail\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.connectWithCredential(req, "fixture-secret"); err == nil || !strings.Contains(err.Error(), "Monitor") || !strings.Contains(err.Error(), req.Channel+"/"+req.Alias) {
		t.Fatal("unmanaged alias was adopted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	inbox, _ := os.ReadFile(filepath.Join(dir, "inbox.jsonl"))
	if string(after) != string(meta) || string(inbox) != "legacy mail\n" || len(d.statusSnapshots()) != 0 || dirExists(filepath.Join(d.root, claudeCredentialDir)) {
		t.Fatal("failed unmanaged admission changed existing peer or stored a credential")
	}
}
