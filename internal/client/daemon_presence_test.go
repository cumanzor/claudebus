package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/core"
)

func presencePeer(t *testing.T, channel, alias string) string {
	t.Helper()
	dir := filepath.Join(CBUSDir(), channel, alias)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeMeta(dir, peerMeta{Alias: alias, Channel: channel, SessionID: "observer-" + alias, TS: Now(), LastActivity: Now(), ListenerPid: jsonNull, OwnerPid: jsonNull}); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(inbox, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return inbox
}

func presenceMessages(t *testing.T, inbox string) []core.Message {
	t.Helper()
	b, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.Message
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var m core.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func onlinePresence(*ConnectionState) consumerProbe {
	return consumerProbe{State: "online", PID: 123, StartToken: "native-client"}
}

func TestManagedPresenceTransitionsAndDaemonRestart(t *testing.T) {
	d, q, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	probe := consumerProbe{State: "online", PID: 123, StartToken: "native-client"}
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return probe, nil }
	c := mustDaemonConnect(t, d, req)
	if messages := presenceMessages(t, inbox); len(messages) != 1 || messages[0].Event != "join" || messages[0].EventID == "" {
		t.Fatalf("join=%+v", messages)
	}
	if _, err := d.connect(req); err != nil {
		t.Fatal(err)
	}
	if got := len(presenceMessages(t, inbox)); got != 1 {
		t.Fatalf("repeat connect emitted %d events", got)
	}
	d2 := reloadDaemonFixture(t, q)
	d2.probeConsumer = d.probeConsumer
	c = d2.connections[c.ID]
	c.Consumer.ObservedAt = ""
	if err := d2.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if got := len(presenceMessages(t, inbox)); got != 1 {
		t.Fatalf("restart emitted %d events", got)
	}
	probe = consumerProbe{State: "unknown", Detail: "permission denied"}
	c.Consumer.ObservedAt = ""
	if err := d2.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.Consumer.State != "unknown" || !c.Consumer.PresenceOnline {
		t.Fatalf("unknown lost last announced state: %+v", c.Consumer)
	}
	probe = onlinePresence(c)
	c.Consumer.ObservedAt = ""
	if err := d2.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if got := len(presenceMessages(t, inbox)); got != 1 {
		t.Fatalf("unknown recovery emitted %d events", got)
	}
	probe = consumerProbe{State: "exited"}
	c.Consumer.ObservedAt = ""
	if err := d2.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	probe = consumerProbe{State: "online", PID: 456, StartToken: "resumed"}
	c.Consumer.ObservedAt = ""
	if err := d2.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	msgs := presenceMessages(t, inbox)
	if len(msgs) != 3 || msgs[1].Event != "departed" || msgs[2].Event != "join" || msgs[1].EventID == msgs[2].EventID {
		t.Fatalf("exit/resume=%+v", msgs)
	}
	if c.Consumer.PID != 456 || c.State == "disconnected" {
		t.Fatalf("resume=%+v", c)
	}
	if len(q.calls) != 0 {
		t.Fatalf("ownership probes called native queue: %+v", q.calls)
	}
}

func TestManagedPresenceDisconnectRemainsDisconnected(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	c := mustDaemonConnect(t, d, req)
	if err := d.disconnect(req.Channel + "/" + req.Alias); err != nil {
		t.Fatal(err)
	}
	c = d.connections[c.ID]
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		t.Fatal("explicit disconnect probed consumer")
		return consumerProbe{}, nil
	}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	msgs := presenceMessages(t, inbox)
	if len(msgs) != 2 || msgs[1].Event != "leave" || c.State != "disconnected" {
		t.Fatalf("disconnect: %+v %+v", msgs, c)
	}
	if !dirExists(d.peerDir(c)) {
		t.Fatal("disconnect deleted durable registration")
	}
}

func TestManagedPresenceAppendBeforeJournalAckIsDeduplicated(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	if err := d.preparePresence(c, "join", "test durable event"); err != nil {
		t.Fatal(err)
	}
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	p := c.PresenceOutbox[0]
	// Emulate crash after append+Sync and before the source journal ack.
	if err := appendManagedPresence(c.Channel, c.Alias, p, p.Recipients[0]); err != nil {
		t.Fatal(err)
	}
	d2 := reloadDaemonFixture(t, &daemonFakeQueue{})
	c = d2.connections[c.ID]
	if err := d2.flushPresence(c); err != nil {
		t.Fatal(err)
	}
	if got := len(presenceMessages(t, inbox)); got != 1 {
		t.Fatalf("duplicate after unacknowledged append: %d", got)
	}
	if len(c.PresenceOutbox) != 0 {
		t.Fatalf("outbox not drained: %+v", c.PresenceOutbox)
	}
}

func TestManagedPresenceSaveFailureDoesNotPublish(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	journalDir := filepath.Join(d.root, "connections")
	if err := os.Rename(journalDir, journalDir+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalDir, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.connectPresence(c, true); err == nil {
		t.Fatal("presence save unexpectedly succeeded")
	}
	if got := len(presenceMessages(t, inbox)); got != 0 {
		t.Fatalf("uncommitted presence published: %d", got)
	}
	if c.Consumer.PresenceOnline {
		t.Fatal("failed save advanced live presence")
	}
	if err := os.Remove(journalDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(journalDir+"-saved", journalDir); err != nil {
		t.Fatal(err)
	}
	if err := d.connectPresence(c, true); err != nil {
		t.Fatal(err)
	}
	if err := d.flushPresence(c); err != nil {
		t.Fatal(err)
	}
	if got := len(presenceMessages(t, inbox)); got != 1 {
		t.Fatalf("retry=%d events", got)
	}
}

func TestManagedPresenceFailedFanoutAckRetriesWithoutAppend(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	if err := d.preparePresence(c, "join", "event"); err != nil {
		t.Fatal(err)
	}
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	if err := os.Rename(journal, journal+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journal, 0700); err != nil {
		t.Fatal(err)
	}
	if err := d.flushPresence(c); err == nil {
		t.Fatal("fanout ack save succeeded unexpectedly")
	}
	if len(presenceMessages(t, inbox)) != 1 || c.PresenceOutbox[0].Recipients[0].Done {
		t.Fatal("failed ack lost pending recipient")
	}
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(journal+"-saved", journal); err != nil {
		t.Fatal(err)
	}
	if err := d.flushPresence(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 1 || len(c.PresenceOutbox) != 0 {
		t.Fatal("fanout ack retry duplicated event")
	}
}

func TestManagedPresenceUnchangedProbeDoesNotRewriteJournal(t *testing.T) {
	d, _, req := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	before, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("unchanged idle ownership rewrote journal")
	}
}

func TestManagedPresenceRecipientEpochAndMalformedTail(t *testing.T) {
	for _, mode := range []string{"replace", "remove", "partial", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			d, _, req := daemonFixture(t)
			inbox := presencePeer(t, req.Channel, "observer")
			c := mustDaemonConnect(t, d, req)
			c = d.connections[c.ID]
			if err := d.preparePresence(c, "join", "event"); err != nil {
				t.Fatal(err)
			}
			if err := d.save(c); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "replace":
				if err := os.RemoveAll(filepath.Dir(inbox)); err != nil {
					t.Fatal(err)
				}
				presencePeer(t, req.Channel, "observer")
			case "remove":
				if err := os.RemoveAll(filepath.Dir(inbox)); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := os.WriteFile(inbox, []byte(`{"eventId":`), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(inbox, []byte("bad-json\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := d.flushPresence(c)
			if mode == "partial" || mode == "corrupt" {
				if err == nil || len(c.PresenceOutbox) == 0 {
					t.Fatalf("bad inbox not fenced: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "replace" && len(presenceMessages(t, inbox)) != 0 {
				t.Fatal("event sent to replacement inbox")
			}
			if mode == "remove" && dirExists(filepath.Dir(inbox)) {
				t.Fatal("removed recipient was recreated")
			}
		})
	}
}

func TestManagedPresenceRemovedSourceCannotPublish(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	if err := d.preparePresence(c, "join", "event"); err != nil {
		t.Fatal(err)
	}
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(d.peerDir(c)); err != nil {
		t.Fatal(err)
	}
	if err := d.flushPresence(c); err != nil {
		t.Fatal(err)
	}
	if len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("removed source emitted stale join")
	}
}

func TestManagedPresenceRemovedDisconnectedRegistrationDetaches(t *testing.T) {
	d, _, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	if err := d.disconnect(req.Channel + "/" + req.Alias); err != nil {
		t.Fatal(err)
	}
	c = d.connections[c.ID]
	if err := os.RemoveAll(d.peerDir(c)); err != nil {
		t.Fatal(err)
	}
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.State != "detached" {
		t.Fatalf("removed disconnected registration remained %q", c.State)
	}
}

func TestManagedPresenceRemoteOutboxNeverUsesLocalChannel(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	c := &ConnectionState{ID: "remote-epoch", Channel: req.Channel, Alias: req.Alias, Relay: &RelayConfig{Host: "relay"}}
	if err := d.preparePresence(c, "join", "remote CLI connected"); err != nil {
		t.Fatal(err)
	}
	if len(c.PresenceOutbox) != 1 || len(c.PresenceOutbox[0].Recipients) != 0 {
		t.Fatalf("remote transition captured local peers: %+v", c.PresenceOutbox)
	}
	if err := d.flushPresence(c); err != nil {
		t.Fatal(err)
	}
	if len(c.PresenceOutbox) != 1 || len(presenceMessages(t, inbox)) != 0 {
		t.Fatal("remote presence was discarded before relay ACK or leaked locally")
	}
	c.Channel = "no-local-directory"
	if err := d.preparePresence(c, "departed", "remote CLI exited"); err != nil {
		t.Fatalf("remote transition requires unrelated local channel: %v", err)
	}
	if c.PresenceSequence != 2 || c.PresenceOutbox[0].ID == c.PresenceOutbox[1].ID {
		t.Fatal("remote transition IDs are not stable unique sequence values")
	}
}

func TestManagedPresencePayloadDistinguishesMembershipFromCompaction(t *testing.T) {
	for _, event := range []string{"join", "leave", "departed", "rename", "compact-post", "future-event"} {
		t.Run(event, func(t *testing.T) {
			// The generic framer omits Event. The native payload must retain it,
			// even when the body does not explain the transition.
			msg := core.Message{From: "dev@relay/reviewer", To: "dev@relay/worker", TS: "2026-09-18T12:00:00Z", Kind: "presence", Event: event, Text: "observation", EventID: "presence-1"}
			line, _ := json.Marshal(msg)
			payload := nativeBusPayload(line, msg)
			if !strings.HasPrefix(payload, string(core.LocalEmit(line))) || !strings.Contains(payload, "event=\""+event+"\"") {
				t.Fatal("lost original frame, remote identity or event type")
			}
			membership := event == "join" || event == "leave" || event == "departed" || event == "rename"
			if strings.Contains(payload, "Briefly tell the user") != membership {
				t.Fatal("membership notification guidance applied to the wrong event")
			}
			if !strings.Contains(payload, "Do not send a bus reply or acknowledgment") || !strings.Contains(payload, "No polling") {
				t.Fatal("presence lost its bus-loop and idle-work restrictions")
			}
			msg.Kind = ""
			line, _ = json.Marshal(msg)
			if nativeBusPayload(line, msg) != string(core.LocalEmit(line)) {
				t.Fatal("ordinary peer message gained presence instructions")
			}
		})
	}
}

func TestManagedPresenceSnapshotIsolation(t *testing.T) {
	c := &ConnectionState{Consumer: &consumerObservation{State: "online"}, PresenceOutbox: []presenceTransition{{Recipients: []presenceRecipient{{Alias: "other"}}}}}
	next := cloneConnection(c)
	next.Consumer.State = "exited"
	next.PresenceOutbox[0].Recipients[0].Done = true
	if c.Consumer.State != "online" || c.PresenceOutbox[0].Recipients[0].Done {
		t.Fatal("published presence snapshot aliases live state")
	}
}

func TestManagedPresenceProbeErrorNeverLeaves(t *testing.T) {
	d, _, req := daemonFixture(t)
	inbox := presencePeer(t, req.Channel, "observer")
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return onlinePresence(nil), nil }
	c := mustDaemonConnect(t, d, req)
	c = d.connections[c.ID]
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{}, errors.New("process inspection denied")
	}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.Consumer.State != "unknown" || len(presenceMessages(t, inbox)) != 1 {
		t.Fatalf("transient probe emitted departure: %+v", c.Consumer)
	}
}
