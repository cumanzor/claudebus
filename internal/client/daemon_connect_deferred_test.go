package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const brokenPeerDeferred = "snapshot presence recipient dev/broken inbox"

// brokenPresencePeer is a live peer whose inbox entry exists but cannot be
// identified, which fails every presence snapshot in its channel.
func brokenPresencePeer(t *testing.T, channel string) (repair func()) {
	t.Helper()
	inbox := presencePeer(t, channel, "broken")
	if err := os.Remove(inbox); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(inbox), "missing-target"), inbox); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	return func() {
		if err := os.Remove(inbox); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inbox, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDaemonConnectDefersPostRegistrationFailure(t *testing.T) {
	d, q, req := daemonFixture(t)
	observer := presencePeer(t, req.Channel, "observer")
	repair := brokenPresencePeer(t, req.Channel)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	var got *ConnectionState
	var err error
	log := captureStderr(t, func() { got, err = d.connect(req) })
	if err != nil {
		t.Fatalf("registered connect must succeed: %v", err)
	}
	if got.Deferred != brokenPeerDeferred {
		t.Fatalf("connect must name the deferred step: %q", got.Deferred)
	}
	c := d.connections[got.ID]
	if !d.owns(c) || c.Deferred != "" || c.Error != "connected; retrying after connect: "+brokenPeerDeferred {
		t.Fatalf("stored connection: owns=%t deferred=%q error=%q", d.owns(c), c.Deferred, c.Error)
	}
	if !strings.Contains(log, "cbus daemon: dev/worker: connected; retrying after connect: "+brokenPeerDeferred) {
		t.Fatalf("daemon did not log the deferred step: %q", log)
	}
	if reloaded := reloadDaemonFixture(t, q).connections[c.ID]; reloaded == nil || reloaded.Deferred != "" || reloaded.Error != c.Error {
		t.Fatalf("journal: %+v", reloaded)
	}
	if got := presenceEvents(t, observer); len(got) != 0 {
		t.Fatalf("join published despite a failed snapshot: %v", got)
	}
	repair()
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(presenceEvents(t, observer), ","); got != "dev/worker join" {
		t.Fatalf("scheduler retry did not publish the join: %s", got)
	}
}

func TestDaemonReconnectDefersPostRegistrationFailure(t *testing.T) {
	d, _, req := daemonFixture(t)
	probe := onlinePresence(nil)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return probe, nil }
	c := mustDaemonConnect(t, d, req)
	probe = consumerProbe{State: "exited"}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil || c.Consumer.PresenceOnline {
		t.Fatalf("exit not observed: %v", err)
	}
	brokenPresencePeer(t, req.Channel)
	probe = onlinePresence(nil)
	got, err := d.connect(req)
	if err != nil {
		t.Fatalf("registered reconnect must succeed: %v", err)
	}
	if got.ID != c.ID || got.Deferred != brokenPeerDeferred {
		t.Fatalf("reconnect deferred=%q", got.Deferred)
	}
}

func TestDaemonConnectHandlerReturnsDeferredOnlyOnConnect(t *testing.T) {
	d, _, req := daemonFixture(t)
	brokenPresencePeer(t, req.Channel)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	r := httptest.NewRecorder()
	body := string(mustBindingJSON(t, connectWireRequest{req, ""}))
	captureStderr(t, func() {
		d.handler(func() {}).ServeHTTP(r, httptest.NewRequest("POST", "/connect", strings.NewReader(body)))
	})
	var got ConnectionState
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &got) != nil || got.Deferred != brokenPeerDeferred {
		t.Fatalf("connect response %d: %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	d.handler(func() {}).ServeHTTP(r, httptest.NewRequest("GET", "/connections", nil))
	if r.Code != 200 || strings.Contains(r.Body.String(), `"deferred"`) {
		t.Fatalf("status carried a connect-only field: %s", r.Body.String())
	}
}

func TestDaemonScheduledFailureLogsOncePerChange(t *testing.T) {
	d, _, req := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	c := mustDaemonConnect(t, d, req)
	first, second := errors.New("first failure"), errors.New("second failure")
	log := captureStderr(t, func() {
		d.scheduledFailure(c, first)
		d.scheduledFailure(c, first)
		d.scheduledFailure(c, second)
	})
	if got := strings.Count(log, "cbus daemon: dev/worker: "); got != 2 || !strings.Contains(log, "first failure") || !strings.Contains(log, "second failure") {
		t.Fatalf("tick log: %q", log)
	}
	if c.State != "error" || c.Error != "second failure" {
		t.Fatalf("state=%s error=%q", c.State, c.Error)
	}
	c.Consumer.ObservedAt = ""
	log = captureStderr(t, func() { d.runScheduled(c) })
	if c.State != "queue-ready" || c.Error != "" {
		t.Fatalf("a successful tick left the tick failure: state=%s error=%q", c.State, c.Error)
	}
	log += captureStderr(t, func() { d.scheduledFailure(c, second) })
	if strings.Count(log, "cbus daemon: dev/worker: second failure") != 1 {
		t.Fatalf("a failure that recurred after a successful tick was not logged again: %q", log)
	}
}

func TestDaemonScheduledSuccessClearsDeferredConnectError(t *testing.T) {
	d, q, req := daemonFixture(t)
	observer := presencePeer(t, req.Channel, "observer")
	repair := brokenPresencePeer(t, req.Channel)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	var got *ConnectionState
	captureStderr(t, func() { got, _ = d.connect(req) })
	c := d.connections[got.ID]
	repair()
	c.Consumer.ObservedAt = ""
	d.runScheduled(c)
	if c.Error != "" || c.State == "error" {
		t.Fatalf("healed connection still reports: state=%s error=%q", c.State, c.Error)
	}
	if reloaded := reloadDaemonFixture(t, q).connections[c.ID]; reloaded == nil || reloaded.Error != "" {
		t.Fatalf("journal kept the stale deferred error: %+v", reloaded)
	}
	if got := strings.Join(presenceEvents(t, observer), ","); got != "dev/worker join" {
		t.Fatalf("observer presence=%s", got)
	}
}

func TestDaemonScheduledSuccessKeepsOtherErrors(t *testing.T) {
	d, _, req := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	c := mustDaemonConnect(t, d, req)
	c.Error = "set elsewhere"
	d.runScheduled(c)
	if c.Error != "set elsewhere" {
		t.Fatalf("a successful tick cleared an error it did not set: %q", c.Error)
	}
}
