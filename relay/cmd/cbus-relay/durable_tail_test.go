package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/relay/internal/wire"
)

func durableServer(t *testing.T) (*server, string) {
	t.Helper()
	s := presenceServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/tail", s.handleTail)
	mux.HandleFunc("/tail/durable-v1", s.handleDurableTail)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return s, strings.TrimPrefix(httpServer.URL, "http://")
}

func dialDurable(t *testing.T, addr, alias, consumer string) *wire.Conn {
	t.Helper()
	c, err := wire.Dial(addr, "/tail/durable-v1?channel=c&alias="+alias+"&consumer="+consumer, "bearer.cbus.tok", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.WriteTimeout = time.Second
	t.Cleanup(func() { c.Close() })
	frame := readDurable(t, c)
	if frame.Type != "ready" || frame.Protocol != durableTailProtocol {
		t.Fatalf("capability frame=%+v", frame)
	}
	return c
}

type durableTestFrame struct {
	Type, Protocol, SpoolID, EventID string
	Message                          json.RawMessage
}

func readDurable(t *testing.T, c *wire.Conn) durableTestFrame {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	op, payload, err := c.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if op != wire.OpText {
		t.Fatalf("unexpected opcode=%x", op)
	}
	var frame durableTestFrame
	if err = json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}
func sendDurable(t *testing.T, c *wire.Conn, frame any) {
	t.Helper()
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.WriteFrame(wire.OpText, b); err != nil {
		t.Fatal(err)
	}
}
func waitSpool(t *testing.T, s *server, alias string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		names, err := s.store.ListNew("c", alias)
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending=%v want %d", names, count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDurableTailLostAckReplaysSameIDAndExactRawJSON(t *testing.T) {
	s, addr := durableServer(t)
	raw := []byte(`{"from":"remote/author","to":"c/a","ts":"2026-09-17T12:00:00Z","text":"payload","extra":{"keep":true}}` + "\n")
	id, err := s.store.Write("c", "a", raw)
	if err != nil {
		t.Fatal(err)
	}
	first := dialDurable(t, addr, "a", "consumer-one")
	frame := readDurable(t, first)
	if frame.Type != "message" || frame.SpoolID != id {
		t.Fatalf("delivery=%+v", frame)
	}
	var got, want any
	_ = json.Unmarshal(frame.Message, &got)
	_ = json.Unmarshal(raw, &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("raw message changed: %s", frame.Message)
	}
	waitSpool(t, s, "a", 1)
	// Same-consumer reconnect supersedes the unacked old transport.
	second := dialDurable(t, addr, "a", "consumer-one")
	replay := readDurable(t, second)
	if replay.SpoolID != id || string(replay.Message) != string(frame.Message) {
		t.Fatalf("replay=%+v", replay)
	}
	sendDurable(t, second, map[string]string{"type": "ack", "spoolId": id})
	waitSpool(t, s, "a", 0)
	if _, err = os.Stat(filepath.Join(s.store.Root, "c", "a", "cur", id)); err != nil {
		t.Fatal(err)
	}
}

func TestDurableTailWrongAckAndMalformedAckPreservePending(t *testing.T) {
	for _, payload := range []string{`{"type":"ack","spoolId":"wrong"}`, `{"type":"ack","spoolId":"x","unexpected":true}`, `not json`} {
		t.Run(payload, func(t *testing.T) {
			s, addr := durableServer(t)
			id, err := s.store.Write("c", "a", []byte(`{"from":"c/b","to":"c/a","text":"keep"}`))
			if err != nil {
				t.Fatal(err)
			}
			c := dialDurable(t, addr, "a", "owner")
			_ = readDurable(t, c)
			if err = c.WriteFrame(wire.OpText, []byte(payload)); err != nil {
				t.Fatal(err)
			}
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			op, _, err := c.ReadFrame()
			if err == nil && op != wire.OpClose {
				t.Fatalf("bad ACK accepted: op=%x", op)
			}
			names, err := s.store.ListNew("c", "a")
			if err != nil || len(names) != 1 || names[0] != id {
				t.Fatalf("bad ACK lost pending: %v %v", names, err)
			}
		})
	}
}

func TestDurableTailOwnershipRejectsCompetingAndLegacyConsumers(t *testing.T) {
	s, addr := durableServer(t)
	first := dialDurable(t, addr, "a", "owner")
	for _, path := range []string{"/tail/durable-v1?channel=c&alias=a&consumer=other", "/tail?channel=c&alias=a"} {
		if c, err := wire.Dial(addr, path, "bearer.cbus.tok", time.Second); err == nil {
			c.Close()
			t.Fatal("active durable consumer was replaced")
		} else if !strings.Contains(err.Error(), "409") {
			t.Fatal(err)
		}
	}
	s.hub.mu.Lock()
	old := s.hub.tails["c/a"]
	s.hub.mu.Unlock()
	_ = dialDurable(t, addr, "a", "owner")
	if err := s.markDelivered("c/a", old, "c", "a", "unused"); err == nil || !strings.Contains(err.Error(), "displaced") {
		t.Fatalf("stale owner ACK=%v", err)
	}
	first.Close()
	legacy, err := wire.Dial(addr, "/tail?channel=c&alias=b", "bearer.cbus.tok", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if c, err := wire.Dial(addr, "/tail/durable-v1?channel=c&alias=b&consumer=owner", "bearer.cbus.tok", time.Second); err == nil {
		c.Close()
		t.Fatal("durable replaced legacy")
	} else if !strings.Contains(err.Error(), "409") {
		t.Fatal(err)
	}
}

func TestDurableTransportDoesNotEmitConsumerPresence(t *testing.T) {
	s, addr := durableServer(t)
	c := dialDurable(t, addr, "a", "owner")
	c.Close()
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if len(s.hub.pending) != 0 {
		t.Fatalf("transport generated presence: %+v", s.hub.pending)
	}
}

func TestDurablePresenceAckAfterFanoutAndRestartDedup(t *testing.T) {
	s, addr := durableServer(t)
	recipient := dialDurable(t, addr, "b", "recipient")
	source := dialDurable(t, addr, "a", "source")
	event := durableClientFrame{Type: "presence", EventID: "source-1", Event: "join", Text: "CLI resumed", TS: "2026-09-17T12:00:00Z"}
	sendDurable(t, source, event)
	ack := readDurable(t, source)
	if ack.Type != "presence-ack" || ack.EventID != event.EventID {
		t.Fatalf("ACK=%+v", ack)
	}
	item := readDurable(t, recipient)
	if item.Type != "message" {
		t.Fatalf("item=%+v", item)
	}
	var message map[string]string
	if err := json.Unmarshal(item.Message, &message); err != nil {
		t.Fatal(err)
	}
	if message["eventId"] != event.EventID || message["from"] != "c/a" || message["kind"] != "presence" || message["event"] != "join" {
		t.Fatalf("presence=%v", message)
	}
	sendDurable(t, recipient, map[string]string{"type": "ack", "spoolId": item.SpoolID})
	waitSpool(t, s, "b", 0)
	// Replay after a fresh server instance reads the same journal and cur/ state.
	restarted := &server{store: s.store, hub: newHub(), token: "tok"}
	tail, _, err := restarted.hub.attachTail("c/a", "source", true)
	if err != nil {
		t.Fatal(err)
	}
	restarted.hub.attach("c/new-recipient")
	if err = restarted.acceptDurablePresence("c/a", tail, "c", "a", event); err != nil {
		t.Fatal(err)
	}
	waitSpool(t, restarted, "b", 0)
	waitSpool(t, restarted, "new-recipient", 0)
	// Simulate publication having committed before the journal progress ACK.
	paths, err := filepath.Glob(filepath.Join(s.store.Root, ".durable-events", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("journals=%v %v", paths, err)
	}
	b, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var journal durablePresenceJournal
	if err = json.Unmarshal(b, &journal); err != nil {
		t.Fatal(err)
	}
	journal.Recipients[0].Done = false
	if err = saveDurablePresenceJournal(paths[0], journal); err != nil {
		t.Fatal(err)
	}
	if err = restarted.acceptDurablePresence("c/a", tail, "c", "a", event); err != nil {
		t.Fatal(err)
	}
	waitSpool(t, restarted, "b", 0)
	event.Text = "different payload"
	if err = restarted.acceptDurablePresence("c/a", tail, "c", "a", event); err == nil {
		t.Fatal("same event ID changed payload")
	}
}

func TestDurableTailRequiresNamedCapabilityEndpoint(t *testing.T) {
	s := presenceServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/tail", s.handleTail)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	if c, err := wire.Dial(strings.TrimPrefix(httpServer.URL, "http://"), "/tail/durable-v1?channel=c&alias=a&consumer=owner", "bearer.cbus.tok", time.Second); err == nil {
		c.Close()
		t.Fatal("legacy server silently accepted new protocol")
	} else if !strings.Contains(err.Error(), "404") {
		t.Fatal(err)
	}
}

func TestLegacyDurableHandoffSettlesOldPresence(t *testing.T) {
	h := newHub()
	old, _ := h.attach("c/a")
	h.detach("c/a", old)
	h.scheduleDepart("c/a")
	next, _, err := h.attachTail("c/a", "native", true)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	present, waiting := h.present["c/a"], h.departWait["c/a"]
	events := append([]presenceEvent(nil), h.pending...)
	h.mu.Unlock()
	if present || waiting != 0 || len(events) != 2 || events[1].event != "departed" {
		t.Fatalf("legacy state leaked: present=%v wait=%d events=%+v", present, waiting, events)
	}
	h.detach("c/a", next)
	if _, joined := h.attach("c/a"); !joined {
		t.Fatal("new legacy owner lost its join")
	}
}

func TestDurablePresencePublicationFailureRetainsPruneEvidence(t *testing.T) {
	s := presenceServer(t)
	source, _, _ := s.hub.attachTail("c/a", "source", true)
	recipient, _, _ := s.hub.attachTail("c/b", "recipient", true)
	if err := s.recordTailOwner("c/b", "recipient", true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.savePresenceJournal = func(path string, journal durablePresenceJournal) error {
		calls++
		if calls == 2 {
			return os.ErrPermission
		} // Publication committed, progress did not.
		return saveDurablePresenceJournal(path, journal)
	}
	event := durableClientFrame{Type: "presence", EventID: "source-crash", Event: "join", Text: "joined", TS: "2026-09-17T12:00:00Z"}
	if err := s.acceptDurablePresence("c/a", source, "c", "a", event); err == nil {
		t.Fatal("progress failure hidden")
	}
	names, err := s.store.ListNew("c", "b")
	if err != nil || len(names) != 1 {
		t.Fatalf("published %v %v", names, err)
	}
	if err := s.markDelivered("c/b", recipient, "c", "b", names[0]); err != nil {
		t.Fatal(err)
	}
	s.hub.detach("c/b", recipient)
	req := httptest.NewRequest(http.MethodPost, "/prune", nil)
	req.Header.Set("Authorization", "Bearer tok")
	response := httptest.NewRecorder()
	s.handlePrune(response, req)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.store.Root, "c", "b", "cur", names[0])); err != nil {
		t.Fatalf("pruned uncommitted fanout evidence: %v", err)
	}
	restarted := &server{store: s.store, hub: newHub(), token: "tok"}
	owner, _, _ := restarted.hub.attachTail("c/a", "source", true)
	if err := restarted.acceptDurablePresence("c/a", owner, "c", "a", event); err != nil {
		t.Fatal(err)
	}
	waitSpool(t, restarted, "b", 0)
	if restarted.hasUnfinishedPresenceRecipient("c", "b") {
		t.Fatal("recovery left progress unfinished")
	}
}

func TestDurablePresenceFrozenRecipientEpochSurvivesRestartAndPrune(t *testing.T) {
	s := presenceServer(t)
	source, _, _ := s.hub.attachTail("c/a", "source", true)
	s.hub.attachTail("c/b", "old-recipient", true)
	if err := s.recordTailOwner("c/b", "old-recipient", true); err != nil {
		t.Fatal(err)
	}
	// Stop before first publication while retaining the frozen recipient snapshot.
	s.savePresenceJournal = func(path string, journal durablePresenceJournal) error {
		if err := saveDurablePresenceJournal(path, journal); err != nil {
			return err
		}
		return os.ErrPermission
	}
	event := durableClientFrame{Type: "presence", EventID: "source-frozen", Event: "join", Text: "joined", TS: "2026-09-17T12:00:00Z"}
	if err := s.acceptDurablePresence("c/a", source, "c", "a", event); err == nil {
		t.Fatal("fault hidden")
	}
	restarted := &server{store: s.store, hub: newHub(), token: "tok"}
	owner, _, _ := restarted.hub.attachTail("c/a", "source", true)
	if err := restarted.recordTailOwner("c/b", "replacement", true); err != nil {
		t.Fatal(err)
	}
	if err := restarted.acceptDurablePresence("c/a", owner, "c", "a", event); err != nil {
		t.Fatal(err)
	}
	waitSpool(t, restarted, "b", 0)
	if restarted.hasUnfinishedPresenceRecipient("c", "b") {
		t.Fatal("superseded recipient did not settle")
	}
}

func TestDurablePresencePublishedEpochDoesNotReachReplacement(t *testing.T) {
	s, addr := durableServer(t)
	name := presenceRecipientSpoolID("123.presence.digest.json", "old-owner")
	if err := s.store.WriteNamed("c", "b", name, []byte(`{"from":"c/a","to":"c/b","text":"stale","kind":"presence"}`)); err != nil {
		t.Fatal(err)
	}
	ordinary, err := s.store.Write("c", "b", []byte(`{"from":"c/a","to":"c/b","text":"ordinary"}`))
	if err != nil {
		t.Fatal(err)
	}
	recipient := dialDurable(t, addr, "b", "replacement")
	frame := readDurable(t, recipient)
	if frame.SpoolID != ordinary {
		t.Fatalf("replacement received old event: %+v", frame)
	}
	if _, err := os.Stat(filepath.Join(s.store.Root, "c", "b", "cur", name)); err != nil {
		t.Fatalf("stale epoch was not retired: %v", err)
	}
	if presenceSpoolMatchesTail(name, &tail{}) {
		t.Fatal("legacy replacement can receive native epoch")
	}
}
