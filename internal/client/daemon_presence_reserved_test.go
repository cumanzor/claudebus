package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	siblingTestThread = "33333333-3333-4333-8333-333333333333"
	thirdTestThread   = "44444444-4444-4444-8444-444444444444"
)

func presenceEvents(t *testing.T, inbox string) []string {
	t.Helper()
	var out []string
	for _, m := range presenceMessages(t, inbox) {
		if m.Kind == "presence" {
			out = append(out, m.From+" "+m.Event)
		}
	}
	return out
}

func TestManagedPresenceReservedSiblingDoesNotFailConnect(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	if _, err := ReserveAlias(req.Channel, "sibling", OriginFresh, "test-model"); err != nil {
		t.Fatal(err)
	}
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	if _, err := d.connect(req); err != nil {
		t.Fatalf("connect with a reserved sibling failed: %v", err)
	}
	if msgs := presenceMessages(t, inbox); len(msgs) != 1 || msgs[0].Event != "join" {
		t.Fatalf("observer join=%+v", msgs)
	}
}

func TestManagedPresenceAbandonedReservationNeverDelaysPresence(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	if _, err := ReserveAlias(req.Channel, "sibling", OriginFresh, "test-model"); err != nil {
		t.Fatal(err)
	}
	probe := onlinePresence(nil)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return probe, nil }
	c := mustDaemonConnect(t, d, req)
	c.Consumer.ObservedAt = ""
	if err := d.scheduledOperation(c); err != nil || c.State == "error" {
		t.Fatalf("tick with an abandoned reservation: err=%v state=%s", err, c.State)
	}
	probe = consumerProbe{State: "exited"}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(presenceEvents(t, inbox), ","); got != "dev/worker join,dev/worker departed" {
		t.Fatalf("observer presence=%s", got)
	}
}

func TestManagedPresenceReservedInboxGetsNothingBeforeClaim(t *testing.T) {
	d, _, req := daemonFixture(t)
	if _, err := ReserveAlias(req.Channel, "sibling", OriginFresh, "test-model"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LocalSend(req.Channel+"/sibling", "dev/sender", false, "queued before launch"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := fileIdentity(InboxPath(req.Channel, "sibling")); !ok {
		t.Fatal("send did not give the reservation an inbox")
	}
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	mustDaemonConnect(t, d, req)
	if got := presenceEvents(t, InboxPath(req.Channel, "sibling")); len(got) != 0 {
		t.Fatalf("reservation received presence before its session existed: %v", got)
	}
}

func TestManagedPresenceReservedSiblingJoinsLaterAndReceivesPresence(t *testing.T) {
	d, _, req := daemonFixture(t)
	observer := presencePeer(t, req.Channel, "observer")
	if _, err := ReserveAlias(req.Channel, "sibling", OriginFresh, "test-model"); err != nil {
		t.Fatal(err)
	}
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	queues := map[string]*daemonFakeQueue{}
	d.openQueue = func(cfg CodexQueueConfig) (nativeQueue, error) {
		return queues[cfg.Home], nil
	}
	peer := func(alias, thread string) ConnectRequest {
		r := req
		r.Alias, r.ThreadID = alias, thread
		r.Config.Home = filepath.Join(filepath.Dir(req.Config.Home), "codex-home-"+alias)
		r.Config.SQLiteHome = r.Config.Home
		queues[r.Config.Home] = &daemonFakeQueue{thread: codexQueueThread{ID: thread, Source: "cli", CliVersion: "test-version", Cwd: req.Config.Cwd}}
		return r
	}
	mustDaemonConnect(t, d, peer("worker", req.ThreadID))
	mustDaemonConnect(t, d, peer("sibling", siblingTestThread))
	mustDaemonConnect(t, d, peer("third", thirdTestThread))
	if got := strings.Join(presenceEvents(t, InboxPath(req.Channel, "sibling")), ","); got != "dev/third join" {
		t.Fatalf("claimed sibling presence=%s", got)
	}
	if got := strings.Join(presenceEvents(t, observer), ","); got != "dev/worker join,dev/sibling join,dev/third join" {
		t.Fatalf("observer presence=%s", got)
	}
}

func TestManagedPresenceSkipsPeerWhoseInboxIsGone(t *testing.T) {
	d, _, req := daemonFixture(t)
	observer := presencePeer(t, req.Channel, "observer")
	if err := os.Remove(presencePeer(t, req.Channel, "leaving")); err != nil {
		t.Fatal(err)
	}
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	if _, err := d.connect(req); err != nil {
		t.Fatalf("connect with a peer mid-removal failed: %v", err)
	}
	if got := strings.Join(presenceEvents(t, observer), ","); got != "dev/worker join" {
		t.Fatalf("observer presence=%s", got)
	}
}

func TestManagedPresenceUnreadableInboxStillFails(t *testing.T) {
	for _, kind := range []string{"dangling", "loop", "unsearchable target"} {
		t.Run(kind, func(t *testing.T) {
			d, _, req := daemonFixture(t)
			inbox := presencePeer(t, req.Channel, "broken")
			if err := os.Remove(inbox); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(filepath.Dir(inbox), "missing-target")
			switch kind {
			case "loop":
				target = inbox
			case "unsearchable target":
				locked := filepath.Join(t.TempDir(), "locked")
				if err := os.MkdirAll(locked, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(locked, "inbox.jsonl"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(locked, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
				if _, err := os.Stat(filepath.Join(locked, "inbox.jsonl")); err == nil {
					t.Skip("permissions not enforced for this user")
				}
				target = filepath.Join(locked, "inbox.jsonl")
			}
			if err := os.Symlink(target, inbox); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
			var got *ConnectionState
			var err error
			captureStderr(t, func() { got, err = d.connect(req) })
			if err != nil {
				t.Fatalf("registered connect must succeed: %v", err)
			}
			if got.Deferred != "snapshot presence recipient dev/broken inbox" {
				t.Fatalf("an inbox that exists but cannot be identified must stay fatal to the snapshot, got %q", got.Deferred)
			}
		})
	}
}
