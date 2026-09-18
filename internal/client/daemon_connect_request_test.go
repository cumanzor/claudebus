package client

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectCapabilityOnlyUsesPrivateEnvelope(t *testing.T) {
	const secret = "test-private-connect-capability"
	req := ConnectRequest{Protocol: DaemonProtocolVersion, Harness: daemonHarnessClaude, Claude: &ClaudeConnectBinding{SessionID: daemonTestThread}}
	wire := connectWireRequest{req, secret}
	if err := validateConnectEnvelope(req, secret); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(wire)
	var decoded connectWireRequest
	if err != nil || json.Unmarshal(b, &decoded) != nil || decoded.ClaudeToken != secret || decoded.Claude.SessionID != daemonTestThread {
		t.Fatal("private wire envelope lost request fields")
	}
	for _, value := range []any{req, *req.Claude, ConnectionState{Claude: &ClaudeConnectionConfig{Binding: *req.Claude}}} {
		b, err := json.Marshal(value)
		if err != nil || strings.Contains(string(b), secret) || strings.Contains(string(b), "claudeToken") {
			t.Fatal("ordinary request/binding/state serialized the capability")
		}
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", wire, wire, wire), secret) {
		t.Fatal("formatted wire request leaked capability")
	}
}

func TestConnectProtocolAndHarnessRejectBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		harness  string
		protocol int
		binding  bool
		token    string
	}{
		{"claude", 0, true, "secret"}, {"claude", 2, true, "secret"},
		{"claude", 3, false, "secret"}, {"claude", 3, true, ""},
		{"codex", 3, false, "secret"}, {"", 3, true, ""},
		{"codex", 99, false, ""},
	} {
		t.Run(fmt.Sprintf("%s/%d/%t/%t", tc.harness, tc.protocol, tc.binding, tc.token != ""), func(t *testing.T) {
			d, q, req := daemonFixture(t)
			req.Harness, req.Protocol = tc.harness, tc.protocol
			if tc.binding {
				req.Claude = &ClaudeConnectBinding{}
			}
			if err := validateConnectEnvelope(req, tc.token); err == nil {
				t.Fatal("incompatible protocol/envelope accepted")
			}
			r := httptest.NewRecorder()
			body := string(mustBindingJSON(t, connectWireRequest{req, tc.token}))
			d.handler(func() {}).ServeHTTP(r, httptest.NewRequest("POST", "/connect", strings.NewReader(body)))
			if r.Code != 400 || len(d.statusSnapshots()) != 0 || q.opens != 0 || strings.Contains(r.Body.String(), "secret") {
				t.Fatal("invalid envelope mutated state or reflected its capability")
			}
		})
	}
}
