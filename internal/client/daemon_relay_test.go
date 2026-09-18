package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"claudebus/internal/core"
	"claudebus/internal/wire"
)

type relayTestPacket struct {
	op   byte
	data []byte
}
type relayTestSocket struct {
	in      chan relayTestPacket
	out     chan relayTestPacket
	closed  chan struct{}
	once    sync.Once
	failACK atomic.Bool
}

func newRelayTestSocket() *relayTestSocket {
	return &relayTestSocket{in: make(chan relayTestPacket, 32), out: make(chan relayTestPacket, 32), closed: make(chan struct{})}
}
func (s *relayTestSocket) ReadFrame() (byte, []byte, error) {
	select {
	case p := <-s.in:
		return p.op, p.data, nil
	case <-s.closed:
		return 0, nil, io.EOF
	}
}
func (s *relayTestSocket) WriteFrame(op byte, b []byte) error {
	var f relayFrame
	_ = json.Unmarshal(b, &f)
	if f.Type == "ack" && s.failACK.Load() {
		_ = s.Abort()
		return errors.New("ACK transport lost")
	}
	select {
	case <-s.closed:
		return io.EOF
	default:
	}
	select {
	case s.out <- relayTestPacket{op, append([]byte(nil), b...)}:
		return nil
	case <-s.closed:
		return io.EOF
	}
}
func (s *relayTestSocket) SetReadDeadline(time.Time) error { return nil }
func (s *relayTestSocket) Abort() error                    { s.once.Do(func() { close(s.closed) }); return nil }
func (s *relayTestSocket) send(t *testing.T, f relayFrame) {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	s.in <- relayTestPacket{wire.OpText, b}
}
func relayNextFrame(t *testing.T, s *relayTestSocket) relayFrame {
	t.Helper()
	select {
	case p := <-s.out:
		var f relayFrame
		if p.op != wire.OpText || json.Unmarshal(p.data, &f) != nil {
			t.Fatalf("unexpected relay output: %+v", p)
		}
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for relay frame")
		return relayFrame{}
	}
}

func relayFixture(t *testing.T) (*busDaemon, *ConnectionState, *schedulerQueue, *relayTestSocket, chan *relayTestSocket, ConnectRequest) {
	t.Helper()
	d, _, req := daemonFixture(t)
	q := &schedulerQueue{thread: req.ThreadID, entered: make(chan daemonQueueCall, 16), closed: make(chan struct{})}
	d.openQueue = func(CodexQueueConfig) (nativeQueue, error) { return q, nil }
	dialed := make(chan *relayTestSocket, 16)
	d.dialRelay = func(context.Context, *ConnectionState) (relaySocket, error) {
		s := newRelayTestSocket()
		dialed <- s
		return s, nil
	}
	req.Relay = &RelayConfig{Host: "test-relay", Base: "http://127.0.0.1:1234", CredentialDir: filepath.Join(req.Config.UserHome, "credentials")}
	c, err := d.connect(req)
	if err != nil {
		t.Fatal(err)
	}
	s := <-dialed
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := d.shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return d, c, q, s, dialed, req
}

func relayMessage(t *testing.T, id, text string) relayFrame {
	t.Helper()
	b, err := json.Marshal(core.Message{From: "dev/sender", To: "dev/worker", TS: "2026-09-18T00:00:00Z", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return relayFrame{Type: "message", SpoolID: id, Message: b}
}

func TestDaemonRelayLostACKDeduplicatesAcrossReconnect(t *testing.T) {
	d, c, q, s, dialed, _ := relayFixture(t)
	s.failACK.Store(true)
	frame := relayMessage(t, "123.1.json", "durable remote payload")
	s.send(t, frame)
	var second *relayTestSocket
	select {
	case second = <-dialed:
	case <-time.After(3 * time.Second):
		t.Fatal("transport did not reconnect")
	}
	path := filepath.Join(d.peerDir(c), "inbox.jsonl")
	before, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(before), `"relayId":"123.1.json"`) {
		t.Fatalf("lost ACK did not leave a durable record: %s %v", before, err)
	}
	second.send(t, frame)
	ack := relayNextFrame(t, second)
	if ack.Type != "ack" || ack.SpoolID != frame.SpoolID {
		t.Fatalf("bad ACK: %+v", ack)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("replayed frame appended a duplicate")
	}
	if !strings.Contains(string(after), `"from":"dev@test-relay/sender"`) || !strings.Contains(string(after), `"to":"dev@test-relay/worker"`) {
		t.Fatalf("remote routing identity lost: %s", after)
	}
	d.schedule()
	schedulerWait(t, "native remote acceptance", func() bool { return d.snapshot(c.ID).Accepted == 1 })
	schedulerWait(t, "remote delivery lane idle", func() bool { return len(d.slots) == 0 })
	second.send(t, frame)
	if f := relayNextFrame(t, second); f.Type != "ack" {
		t.Fatal(f)
	}
	d.schedule()
	schedulerWait(t, "duplicate check complete", func() bool { return len(d.slots) == 0 })
	if len(q.observedCalls()) != 1 {
		t.Fatal("relay replay duplicated native queue submission")
	}
	if _, err := os.Stat(filepath.Join(CBUSDir(), "dev", "worker")); !os.IsNotExist(err) {
		t.Fatal("remote registration collided with local channel store")
	}
	marker, err := os.ReadFile(filepath.Join(CBUSDir(), ".remote", "test-relay", "dev", c.ThreadID))
	if err != nil || !strings.Contains(string(marker), `"alias": "worker"`) {
		t.Fatalf("exact-session reply marker missing: %s %v", marker, err)
	}
}

func TestDaemonRelayDisconnectCancelsSubscriptionAndKeepsInbox(t *testing.T) {
	d, c, q, s, dialed, _ := relayFixture(t)
	s.send(t, relayMessage(t, "456.1.json", "still waiting"))
	if f := relayNextFrame(t, s); f.Type != "ack" {
		t.Fatal(f)
	}
	before, err := os.ReadFile(filepath.Join(d.peerDir(c), "inbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.disconnect(ConnectionTarget(c)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.closed:
	default:
		t.Fatal("disconnect returned before transport closed")
	}
	d.schedule()
	schedulerWait(t, "disconnected worker drained", func() bool { return len(d.slots) == 0 })
	select {
	case <-dialed:
		t.Fatal("disconnected alias reopened idle subscription")
	default:
	}
	after, err := os.ReadFile(filepath.Join(d.peerDir(c), "inbox.jsonl"))
	if err != nil || string(before) != string(after) {
		t.Fatal("disconnect changed durable remote inbox")
	}
	if len(q.observedCalls()) != 0 || d.snapshot(c.ID).State != "disconnected" {
		t.Fatal("disconnect allowed native delivery")
	}
}

func TestDaemonRelayPresenceOutboxSurvivesLostACKAndDisconnect(t *testing.T) {
	d, c, _, s, dialed, _ := relayFixture(t)
	owned, finish, err := d.beginOperation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	owned.Consumer = &consumerObservation{State: "online", PresenceOnline: true, ObservedAt: Now()}
	if err := d.preparePresence(owned, "join", "CLI connected"); err != nil {
		t.Fatal(err)
	}
	if err := d.save(owned); err != nil {
		t.Fatal(err)
	}
	finish()
	join := relayNextFrame(t, s)
	if join.Type != "presence" || join.Event != "join" || join.EventID == "" {
		t.Fatal(join)
	}
	_ = s.Abort()
	var second *relayTestSocket
	select {
	case second = <-dialed:
	case <-time.After(3 * time.Second):
		t.Fatal("presence ACK loss did not reconnect")
	}
	retry := relayNextFrame(t, second)
	if retry.EventID != join.EventID {
		t.Fatalf("presence retry changed ID: %+v %+v", join, retry)
	}
	second.send(t, relayFrame{Type: "presence-ack", EventID: join.EventID})
	schedulerWait(t, "join ACK durably removed", func() bool { return len(d.snapshot(c.ID).PresenceOutbox) == 0 })
	schedulerWait(t, "presence ACK lane released", func() bool { return len(d.slots) == 0 })
	if err := d.disconnect(ConnectionTarget(c)); err != nil {
		t.Fatal(err)
	}
	if state := d.snapshot(c.ID); len(state.PresenceOutbox) != 1 || state.PresenceOutbox[0].Event != "leave" {
		t.Fatalf("disconnect lost pending departure: %+v", state)
	}
	d.schedule() // publish-only reconnect; queued inbound messages must remain unacked.
	var departure *relayTestSocket
	select {
	case departure = <-dialed:
	case <-time.After(3 * time.Second):
		t.Fatal("departure outbox was stranded by disconnect")
	}
	leave := relayNextFrame(t, departure)
	if leave.Event != "leave" {
		t.Fatal(leave)
	}
	departure.send(t, relayMessage(t, "789.1.json", "must stay on relay"))
	departure.send(t, relayFrame{Type: "presence-ack", EventID: leave.EventID})
	schedulerWait(t, "departure committed while disconnected", func() bool { return len(d.snapshot(c.ID).PresenceOutbox) == 0 })
	select {
	case p := <-departure.out:
		t.Fatalf("disconnected consumer ACKed inbound mail: %s", p.data)
	case <-departure.closed:
	case <-time.After(time.Second):
		t.Fatal("publish-only departure socket stayed open")
	}
}

func TestDaemonRelayRejectsMalformedOrChangedEpochWithoutACK(t *testing.T) {
	for _, kind := range []string{"null", "wrong target", "replaced inbox", "partial inbox", "replaced owner"} {
		t.Run(kind, func(t *testing.T) {
			d, c, q, s, _, _ := relayFixture(t)
			frame := relayMessage(t, "bad.1.json", "do not consume")
			path := filepath.Join(d.peerDir(c), "inbox.jsonl")
			switch kind {
			case "null":
				frame.Message = json.RawMessage("null")
			case "wrong target":
				frame.Message = json.RawMessage(`{"from":"dev/sender","to":"dev/other","text":"wrong"}`)
			case "replaced inbox":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "partial inbox":
				if err := os.WriteFile(path, []byte(`{"from":`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replaced owner":
				if err := os.Remove(filepath.Join(d.peerDir(c), "meta.json")); err != nil {
					t.Fatal(err)
				}
			}
			s.send(t, frame)
			select {
			case <-s.closed:
			case p := <-s.out:
				t.Fatalf("invalid delivery was ACKed: %s", p.data)
			case <-time.After(time.Second):
				t.Fatal("invalid relay frame was not rejected")
			}
			if len(q.observedCalls()) != 0 {
				t.Fatal("invalid relay frame reached the model")
			}
		})
	}
}

func TestDaemonRelayRefusesCapabilityBeforeClaimingAlias(t *testing.T) {
	d, q, req := daemonFixture(t)
	req.Relay = &RelayConfig{Host: "test-relay", Base: "http://localhost:1234", CredentialDir: req.Config.UserHome}
	d.dialRelay = func(context.Context, *ConnectionState) (relaySocket, error) {
		return nil, errors.New("upgrade the relay")
	}
	if _, err := d.connect(req); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("old relay capability accepted: %v", err)
	}
	if len(d.statusSnapshots()) != 0 || q.closes != 1 {
		t.Fatal("failed relay handshake claimed a registration or leaked sidecar")
	}
	entries, err := os.ReadDir(filepath.Join(d.root, "connections"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed capability persisted connection: %v %+v", err, entries)
	}
}

func TestDaemonRelayEndpointHandshakeAndLegacyRefusal(t *testing.T) {
	for _, mode := range []string{"ready", "legacy", "wrong protocol"} {
		t.Run(mode, func(t *testing.T) {
			requests := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.Path
				if mode == "legacy" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Query().Get("channel") != "dev" || r.URL.Query().Get("alias") != "worker" || r.URL.Query().Get("consumer") != "stable-id" {
					http.Error(w, "bad route", 400)
					return
				}
				conn, err := wire.Upgrade(w, r, "bearer.cbus.test-token")
				if err != nil {
					return
				}
				defer conn.Abort()
				protocol := "cbus-relay-durable/v1"
				if mode == "wrong protocol" {
					protocol = "legacy"
				}
				b, _ := json.Marshal(map[string]string{"type": "ready", "protocol": protocol})
				_ = conn.WriteFrame(wire.OpText, b)
				_, _, _ = conn.ReadFrame()
			}))
			defer server.Close()
			c := &ConnectionState{ID: "stable-id", Channel: "dev", Alias: "worker", Relay: &RelayConfig{Base: server.URL, Host: "test-relay"}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dialRelayWithToken(ctx, c, "test-token")
			if mode == "ready" {
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Abort()
			} else if err == nil || !strings.Contains(err.Error(), "upgrade") {
				t.Fatalf("incompatible relay accepted: %v", err)
			}
			if path := <-requests; path != "/tail/durable-v1" {
				t.Fatalf("legacy consuming endpoint was contacted: %s", path)
			}
		})
	}
}
