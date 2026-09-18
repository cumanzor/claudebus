package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDaemonConnectRequiresPositiveCurrentConsumer(t *testing.T) {
	for _, source := range []string{"cli", "vscode"} {
		for _, tc := range []struct {
			name  string
			probe consumerProbe
			err   error
		}{
			{name: "unknown", probe: consumerProbe{State: "unknown"}},
			{name: "exited", probe: consumerProbe{State: "exited"}},
			{name: "missing PID", probe: consumerProbe{State: "online", StartToken: "start"}},
			{name: "missing start", probe: consumerProbe{State: "online", PID: 123}},
			{name: "inspection denied", err: errors.New("permission denied")},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				d, q, req := daemonFixture(t)
				q.thread.Source = source
				d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return tc.probe, tc.err }
				if _, err := d.connect(req); err == nil {
					t.Fatal("unverified consumer admitted")
				}
				if dirExists(filepath.Join(CBUSDir(), req.Channel)) || len(d.statusSnapshots()) != 0 || q.closes != 1 || len(d.queues) != 0 {
					t.Fatal("rejected admission claimed state or retained sidecar")
				}
			})
		}
	}
}

func TestDaemonReconnectRevalidatesRuntimeAndPreservesPending(t *testing.T) {
	d, q, req := daemonFixture(t)
	req.Config.RuntimePID, req.Config.RuntimeStartToken = 123, "native-client"
	req.Config.BindingSource = "runtime-open-queue"
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "", "pending input")
	q.enqueueErr = errors.New("response lost")
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatal("missing pending fixture")
	}
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	before, _ := os.ReadFile(journal)
	calls := len(q.calls)
	for _, changedProcess := range []bool{false, true} {
		next := req
		next.Config.RuntimeStartToken = "reused PID"
		if changedProcess {
			next.Config.Binary = filepath.Join(req.Config.Home, "new-codex")
		}
		if _, err := d.connect(next); err == nil {
			t.Fatal("stale caller identity passed cached/reopened queue")
		}
		after, _ := os.ReadFile(journal)
		if string(before) != string(after) || d.cachedQueue(c.ID) != nil || len(q.calls) != calls {
			t.Fatal("failed rebind changed journal, cached wrong process or retried pending")
		}
	}
	next := req
	next.Config.RuntimePID, next.Config.RuntimeStartToken = 456, "resumed"
	d.probeConsumer = func(_ context.Context, observed *ConnectionState) (consumerProbe, error) {
		if observed.Consumer == nil && observed.Config != next.Config {
			t.Fatal("admission did not use incoming binding")
		}
		return consumerProbe{State: "online", PID: 456, StartToken: "resumed"}, nil
	}
	original := cloneConnection(c)
	got, err := d.connect(next)
	if err != nil || got.ID != original.ID || got.Dev != original.Dev || got.Ino != original.Ino || got.Offset != original.Offset || !reflect.DeepEqual(got.Pending, original.Pending) || got.Config != next.Config || len(q.calls) != calls {
		t.Fatalf("resume lost durable state: %+v %v", got, err)
	}
}

func TestDaemonQueueReopenDoesNotRequireLiveConsumer(t *testing.T) {
	d, q, req := daemonFixture(t)
	q.thread.Source = "vscode"
	c := mustDaemonConnect(t, d, req)
	end := appendDaemonMessage(t, c, "", "queued while CLI offline")
	d.closeQueue(c.ID)
	d = reloadDaemonFixture(t, q)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		t.Fatal("offline delivery required a live consumer")
		return consumerProbe{}, nil
	}
	c = d.connections[c.ID]
	if err := d.deliver(c); err != nil || c.Offset != end || len(q.calls) != 1 {
		t.Fatalf("offline queue delivery failed: %+v %v", c, err)
	}
}
