package client

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

const replacementThread = "55555555-5555-4555-8555-555555555555"

// The alias is unregistered and claimed by another session between close's
// resolve and its disconnect; the fence must spare the replacement.
func TestFencedDisconnectSparesAReplacementRegistration(t *testing.T) {
	d, q, req := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil
	}
	first := mustDaemonConnect(t, d, req)
	stale := disconnectFence{ConnectionID: first.ID, ThreadID: first.ThreadID, ConsumerPID: 4321, ConsumerStart: "codex-cli"}
	if err := Unregister(req.Channel, req.Alias); err != nil {
		t.Fatal(err)
	}
	req.ThreadID, q.thread.ID = replacementThread, replacementThread
	replacement := mustDaemonConnect(t, d, req)
	if replacement.ID == first.ID {
		t.Fatal("precondition: the alias must now hold a different registration")
	}
	if _, err := d.disconnectExact(ConnectionTarget(replacement), stale); err == nil || !strings.Contains(err.Error(), "different registration") {
		t.Fatalf("a stale fence disconnected the replacement: %v", err)
	}
	if got := d.snapshot(replacement.ID); got.State == "disconnected" {
		t.Fatal("the replacement registration was disconnected")
	}
}

func TestFencedDisconnectChecksTheConsumerWitness(t *testing.T) {
	d, _, req := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil
	}
	c := mustDaemonConnect(t, d, req)
	wrong := disconnectFence{ConnectionID: c.ID, ThreadID: c.ThreadID, ConsumerPID: 4321, ConsumerStart: "a-later-process"}
	if _, err := d.disconnectExact(ConnectionTarget(c), wrong); err == nil || d.snapshot(c.ID).State == "disconnected" {
		t.Fatalf("a changed consumer incarnation was disconnected: %v", err)
	}
	right := wrong
	right.ConsumerStart = "codex-cli"
	if id, err := d.disconnectExact(ConnectionTarget(c), right); err != nil || id != c.ID || d.snapshot(c.ID).State != "disconnected" {
		t.Fatalf("the exact registration was not disconnected: %v", err)
	}
}

func TestDisconnectHandlerCarriesTheFence(t *testing.T) {
	d, _, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		d.handler(func() {}).ServeHTTP(r, httptest.NewRequest("POST", "/disconnect", strings.NewReader(body)))
		return r
	}
	if r := post(`{"target":"` + ConnectionTarget(c) + `","connectionId":"someone-else"}`); r.Code != 400 || d.snapshot(c.ID).State == "disconnected" {
		t.Fatalf("the wire fence was ignored: %d %s", r.Code, r.Body.String())
	}
	r := post(`{"target":"` + ConnectionTarget(c) + `"}`)
	var out struct {
		Disconnected bool   `json:"disconnected"`
		ConnectionID string `json:"connectionId"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || !out.Disconnected || out.ConnectionID != c.ID {
		t.Fatalf("the operator disconnect changed: %d %s", r.Code, r.Body.String())
	}
}
