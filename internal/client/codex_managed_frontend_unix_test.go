//go:build darwin || linux

package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedFrontendOriginRequiresLiteralLocalEndpoint(t *testing.T) {
	for _, origin := range []string{"", "http://localhost:1234", "http://127.0.0.2:1234", "https://127.0.0.1:1234", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:0123", "http://user@127.0.0.1:1234", "http://127.0.0.1:1234/", "http://127.0.0.1:1234?", "http://127.0.0.1:1234#"} {
		if _, err := codexFrontendPort(origin); err == nil {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	if port, err := codexFrontendPort("http://127.0.0.1:1234"); err != nil || port != 1234 {
		t.Fatalf("local origin: %d %v", port, err)
	}
}

func TestManagedFrontendRejectsWrongControlSocketOwner(t *testing.T) {
	c, backend, _ := managedConsumerFixture(t, nil, "managed", false)
	fake := startFakeCodex(t, nil)
	home, err := os.MkdirTemp("", "cbus-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	c.Config.Home = home
	control := filepath.Join(c.Config.Home, "app-server-control")
	if err := os.MkdirAll(control, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fake.sock, filepath.Join(control, "app-server-control.sock")); err != nil {
		t.Fatal(err)
	}
	if _, err := managedCodexFrontend(context.Background(), c, backend); err == nil || !strings.Contains(err.Error(), "socket peer") {
		t.Fatalf("wrong backend accepted: %v", err)
	}
	if len(fake.recorded()) != 0 {
		t.Fatal("queried an unverified backend")
	}
}

func TestManagedLifecycleUnknownFrontendRetainsNewMail(t *testing.T) {
	fixture, _, _ := managedConsumerFixture(t, nil, "managed", false)
	d, q, req := daemonFixture(t)
	req.Config, q.thread.Path = fixture.Config, fixture.RolloutPath
	d.probeConsumer = observeCodexConsumer
	c := mustDaemonConnect(t, d, req)
	if err := os.WriteFile(filepath.Join(fixture.Config.Home, "frontend-origin"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	appendDaemonMessage(t, c, "", "wait for positive frontend proof")
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 0 || c.Pending != nil || c.Consumer.State != "unknown" || !c.Consumer.PresenceOnline {
		t.Fatalf("inconclusive frontend changed delivery/presence: %+v calls=%v", c, q.calls)
	}
}

func TestManagedLifecycleOfflineReconcilesPriorAttemptWithoutResending(t *testing.T) {
	fixture, _, stopFrontend := managedConsumerFixture(t, nil, "managed", false)
	d, q, req := daemonFixture(t)
	req.Config, q.thread.Path = fixture.Config, fixture.RolloutPath
	d.probeConsumer = observeCodexConsumer
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "", "accepted before CLI exit")
	q.enqueueErr = errors.New("response lost")
	if err := d.deliver(c); err == nil || c.Pending == nil || len(q.calls) != 1 {
		t.Fatal("missing uncertain attempt")
	}
	stopFrontend()
	q.found = true
	if err := d.deliver(c); err != nil || c.Pending != nil || c.Accepted != 1 || len(q.calls) != 1 {
		t.Fatalf("offline reconciliation: %v calls=%v connection=%+v", err, q.calls, c)
	}
	appendDaemonMessage(t, c, "", "new mail must wait")
	if err := d.deliver(c); err != nil || len(q.calls) != 1 || c.Accepted != 1 {
		t.Fatalf("new offline mail submitted: %v calls=%v", err, q.calls)
	}
}
