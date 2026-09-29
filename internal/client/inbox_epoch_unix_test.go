//go:build darwin || linux

package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const renumberedDev = 4242

// A connection that accepted one message, with a second one unread.
func acceptedInboxFixture(t *testing.T) (*busDaemon, *daemonFakeQueue, *ConnectionState, int64) {
	t.Helper()
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	first := appendDaemonMessage(t, c, "", "consumed")
	if err := d.deliver(c); err != nil || c.Offset != first || c.LastAccepted == nil {
		t.Fatalf("fixture delivery: %v %+v", err, c)
	}
	appendDaemonMessage(t, c, "", "unread")
	return d, q, c, first
}

// The journal keeps the old device number, as it does across a renumbering reboot.
func renumberJournal(t *testing.T, d *busDaemon, q *daemonFakeQueue, c *ConnectionState) (*busDaemon, *ConnectionState) {
	t.Helper()
	c.Dev = renumberedDev
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d = reloadDaemonFixture(t, q)
	return d, d.connections[c.ID]
}

func rearmError(d *busDaemon, c *ConnectionState) string {
	d.rearmLoaded(context.Background())
	return d.snapshot(c.ID).ListenerError
}

func realInboxDev(t *testing.T, d *busDaemon, c *ConnectionState) uint64 {
	t.Helper()
	dev, _, _, ok := fileIdentity(filepath.Join(d.peerDir(c), "inbox.jsonl"))
	if !ok {
		t.Fatal("stat inbox")
	}
	return dev
}

func TestInboxDevOnlyChangeRestampsAndDelivers(t *testing.T) {
	d, q, c, first := acceptedInboxFixture(t)
	accepted := *c.LastAccepted
	d, c = renumberJournal(t, d, q, c)
	if msg := rearmError(d, c); msg != "" {
		t.Fatalf("dev-only change was refused: %s", msg)
	}
	if c.Dev != realInboxDev(t, d, c) {
		t.Fatal("identity was not re-stamped")
	}
	// The re-stamp is durable: a further restart needs no second re-stamp.
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	if c.Dev != realInboxDev(t, d, c) || c.Offset != first || c.LastAccepted.Attempt != accepted.Attempt {
		t.Fatalf("restart after re-stamp lost the journal: %+v", c)
	}
	if err := d.deliver(c); err != nil || c.Accepted != 2 || len(q.calls) != 2 {
		t.Fatalf("delivery did not continue after re-stamp: %v %+v", err, c)
	}
}

func TestInboxDevChangeInDeliverUsesTheReadHandle(t *testing.T) {
	d, q, c, _ := acceptedInboxFixture(t)
	d, c = renumberJournal(t, d, q, c)
	if err := d.deliver(c); err != nil || c.Accepted != 2 || c.Dev != realInboxDev(t, d, c) {
		t.Fatalf("deliver did not re-stamp and continue: %v %+v", err, c)
	}
}

func TestInboxEpochStillRefusesRealChanges(t *testing.T) {
	cases := map[string]func(t *testing.T, path string, first int64){
		"replaced inode": func(t *testing.T, path string, _ int64) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tmp := path + ".new"
			if err := os.WriteFile(tmp, b, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
		},
		"truncated": func(t *testing.T, path string, first int64) {
			if err := os.Truncate(path, first-1); err != nil {
				t.Fatal(err)
			}
		},
		"rewritten consumed record": func(t *testing.T, path string, first int64) {
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteAt([]byte("X"), first-3); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		for _, renumbered := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s renumbered=%v", name, renumbered), func(t *testing.T) {
				d, q, c, first := acceptedInboxFixture(t)
				mutate(t, filepath.Join(d.peerDir(c), "inbox.jsonl"), first)
				dev := c.Dev
				if renumbered {
					d, c = renumberJournal(t, d, q, c)
					dev = renumberedDev
				} else {
					d = reloadDaemonFixture(t, q)
					c = d.connections[c.ID]
				}
				msg := rearmError(d, c)
				if name == "rewritten consumed record" && !renumbered {
					// Same device: the check is identity and size only, as before.
					if msg != "" {
						t.Fatalf("unchanged device read record content: %q", msg)
					}
					return
				}
				if !strings.Contains(msg, "inbox changed or truncated") || !strings.Contains(msg, "cbus unregister dev/worker") {
					t.Fatalf("real epoch change was not refused with a recovery: %q", msg)
				}
				if c.Dev != dev {
					t.Fatal("refused epoch was re-stamped")
				}
			})
		}
	}
}

func TestInboxDevChangeWithoutAnchorRefuses(t *testing.T) {
	d, q, c, _ := acceptedInboxFixture(t)
	c.LastAccepted = nil
	d, c = renumberJournal(t, d, q, c)
	if msg := rearmError(d, c); !strings.Contains(msg, "inbox changed or truncated") {
		t.Fatalf("consumed offset without an anchor record was trusted: %q", msg)
	}
}

func TestInboxDevChangeAnchorsOnAbandonedRecord(t *testing.T) {
	d, q, c, _ := acceptedInboxFixture(t)
	q.enqueueErr = &codexQueueTransportError{Method: "thread/queue/add", Err: errors.New("lost response")}
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatal("fixture did not leave a pending attempt")
	}
	if _, err := d.abandon(AbandonRequest{Target: "dev/worker", ClientID: c.Pending.ClientID, Reason: "operator choice"}); err != nil {
		t.Fatal(err)
	}
	if c.LastAccepted.Attempt.End == c.Offset || len(c.Resolutions) != 1 {
		t.Fatalf("fixture must end on an abandon: %+v", c)
	}
	d, c = renumberJournal(t, d, q, c)
	if msg := rearmError(d, c); msg != "" {
		t.Fatalf("abandoned record was not used as the anchor: %s", msg)
	}
}

func TestInboxDevChangeChecksPendingRecord(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		d, q, c, _ := acceptedInboxFixture(t)
		q.enqueueErr = &codexQueueTransportError{Method: "thread/queue/add", Err: errors.New("lost response")}
		if err := d.deliver(c); err == nil || c.Pending == nil {
			t.Fatal("fixture did not leave a pending attempt")
		}
		if rewrite {
			f, err := os.OpenFile(filepath.Join(d.peerDir(c), "inbox.jsonl"), os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteAt([]byte("X"), c.Pending.End-3)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		d, c = renumberJournal(t, d, q, c)
		if rewrite {
			// Rearm reads no pending bytes itself, so only the epoch check can refuse.
			if msg := rearmError(d, c); !strings.Contains(msg, "inbox changed or truncated") || c.Dev != renumberedDev {
				t.Fatalf("rewritten pending record was trusted: %q", msg)
			}
			continue
		}
		q.enqueueErr, q.found = nil, true
		_, err := d.reconcile("dev/worker")
		if err != nil || c.Pending != nil || c.Accepted != 2 || c.Dev != realInboxDev(t, d, c) {
			t.Fatalf("reconcile after a dev-only change failed: %v %+v", err, c)
		}
	}
}

func TestRelayAppendDefersDevChangeToLaneOwner(t *testing.T) {
	d, c, _, _, _, _ := relayFixture(t)
	path := filepath.Join(d.peerDir(c), "inbox.jsonl")
	_, _, before, _ := fileIdentity(path)
	d.mu.Lock()
	live := d.connections[c.ID]
	live.Dev = renumberedDev
	d.snapshots[c.ID] = cloneConnection(live)
	d.mu.Unlock()
	err := d.appendRelay(context.Background(), c, relayMessage(t, "9.1.json", "held"))
	if err == nil || !strings.Contains(err.Error(), "device changed") {
		t.Fatalf("relay append did not defer a dev-only change: %v", err)
	}
	if _, _, after, _ := fileIdentity(path); after != before || d.snapshot(c.ID).Dev != renumberedDev {
		t.Fatal("relay append wrote mail or re-stamped identity")
	}
}
