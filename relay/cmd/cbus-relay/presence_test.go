package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"claudebus/internal/core"
	"claudebus/relay/internal/spool"
)

func presenceServer(t *testing.T) *server {
	t.Helper()
	return &server{store: spool.Store{Root: t.TempDir()}, hub: newHub(), token: "tok"}
}

// queued reads and parses every spool line waiting for a peer.
func queued(t *testing.T, st spool.Store, ch, al string) []map[string]string {
	t.Helper()
	names, _ := st.ListNew(ch, al)
	var out []map[string]string
	for _, n := range names {
		b, err := st.Read(ch, al, n)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("bad spool line %q: %v", b, err)
		}
		out = append(out, m)
	}
	return out
}

// waitFor polls a peer's queued lines until pred is satisfied or it times out.
func waitFor(t *testing.T, st spool.Store, ch, al string, pred func([]map[string]string) bool) []map[string]string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		lines := queued(t, st, ch, al)
		if pred(lines) {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out; c/%s had %v", al, lines)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func countEvent(lines []map[string]string, event string) int {
	n := 0
	for _, m := range lines {
		if m["event"] == event {
			n++
		}
	}
	return n
}

// First attach is a new join; a displacing attach on the same key is not.
func TestAttachJoinDecision(t *testing.T) {
	h := newHub()
	if _, joined := h.attach("c/a"); !joined {
		t.Fatal("first attach must be a new join")
	}
	if _, joined := h.attach("c/a"); joined {
		t.Fatal("displacement (same key) must NOT be a new join")
	}
}

// Fan-out reaches every connected peer on the channel except the actor, carries
// kind=presence + per-recipient to=, and never crosses to another channel.
func TestFanoutPresence(t *testing.T) {
	s := presenceServer(t)
	s.hub.attach("c/listener")
	s.hub.attach("c/observer")
	s.hub.attach("other/x")

	s.fanoutPresence("c", "joiner", "join", "connected as joiner")

	for _, al := range []string{"listener", "observer"} {
		lines := queued(t, s.store, "c", al)
		if len(lines) != 1 {
			t.Fatalf("c/%s: got %d presence lines, want 1", al, len(lines))
		}
		m := lines[0]
		if m["kind"] != "presence" || m["event"] != "join" || m["from"] != "c/joiner" || m["to"] != "c/"+al {
			t.Fatalf("c/%s: bad presence line %v", al, m)
		}
	}
	if l := queued(t, s.store, "c", "joiner"); len(l) != 0 {
		t.Fatalf("actor must not notify itself: %v", l)
	}
	if l := queued(t, s.store, "other", "x"); len(l) != 0 {
		t.Fatalf("other-channel peer must not receive: %v", l)
	}
}

// End-to-end: a join by a second peer is delivered through the drainer to a listener.
func TestJoinDeliveredViaDrainer(t *testing.T) {
	s := presenceServer(t)
	go s.presenceDrainer()

	s.hub.attach("c/listener") // no other peers yet -> its own join fans to nobody
	s.hub.attach("c/joiner")   // fans a join to listener

	lines := waitFor(t, s.store, "c", "listener", func(l []map[string]string) bool {
		return countEvent(l, "join") == 1
	})
	m := lines[0]
	if m["kind"] != "presence" || m["from"] != "c/joiner" || m["to"] != "c/listener" || m["text"] != "connected as joiner" {
		t.Fatalf("bad join line: %v", m)
	}
}

// A real disconnect becomes a departed after grace, fanned to connected peers.
func TestDepartedAfterGrace(t *testing.T) {
	s := presenceServer(t)
	s.hub.grace = 30 * time.Millisecond
	go s.presenceDrainer()

	s.hub.attach("c/listener")
	tw, _ := s.hub.attach("c/worker")
	if s.hub.detach("c/worker", tw) {
		t.Fatal("clean detach wrongly reported displaced")
	}
	s.hub.scheduleDepart("c/worker")

	lines := waitFor(t, s.store, "c", "listener", func(l []map[string]string) bool {
		return countEvent(l, "departed") == 1
	})
	var dep map[string]string
	for _, m := range lines {
		if m["event"] == "departed" {
			dep = m
		}
	}
	if dep["from"] != "c/worker" || dep["kind"] != "presence" || dep["text"] != "departed (connection lost)" {
		t.Fatalf("bad departed line: %v", dep)
	}
}

// A reconnect within grace cancels the pending departed and is not a fresh join.
func TestDepartedCancelledByReconnect(t *testing.T) {
	s := presenceServer(t)
	s.hub.grace = 80 * time.Millisecond
	go s.presenceDrainer()

	s.hub.attach("c/listener")
	tw, _ := s.hub.attach("c/worker")
	s.hub.detach("c/worker", tw)
	s.hub.scheduleDepart("c/worker")
	time.Sleep(15 * time.Millisecond) // well before grace
	if _, joined := s.hub.attach("c/worker"); joined {
		t.Fatal("reconnect within grace must NOT be a new join")
	}
	time.Sleep(200 * time.Millisecond) // past the original grace
	if n := countEvent(queued(t, s.store, "c", "listener"), "departed"); n != 0 {
		t.Fatalf("reconnect must cancel the pending departed, but %d were delivered", n)
	}
}

// Displacement (takeover) is neither a join nor a departure.
func TestDisplacementNoDepart(t *testing.T) {
	s := presenceServer(t)
	s.hub.grace = 20 * time.Millisecond
	go s.presenceDrainer()

	s.hub.attach("c/listener")
	t1, _ := s.hub.attach("c/worker")
	if _, joined := s.hub.attach("c/worker"); joined { // t2 displaces t1
		t.Fatal("displacing attach must not be a new join")
	}
	if !s.hub.detach("c/worker", t1) { // the displaced tail leaving is a takeover
		t.Fatal("detach of a displaced tail must report displaced=true")
	}
	// caller skips scheduleDepart when displaced, so nothing departed should fire
	time.Sleep(80 * time.Millisecond)
	if n := countEvent(queued(t, s.store, "c", "listener"), "departed"); n != 0 {
		t.Fatalf("displacement must not emit departed, delivered %d", n)
	}
}

// After a departed lands, a genuine return is a fresh join again.
func TestJoinAfterDeparted(t *testing.T) {
	s := presenceServer(t)
	s.hub.grace = 20 * time.Millisecond
	go s.presenceDrainer()

	t1, j1 := s.hub.attach("c/worker")
	if !j1 {
		t.Fatal("first attach should be a join")
	}
	s.hub.detach("c/worker", t1)
	s.hub.scheduleDepart("c/worker")
	time.Sleep(80 * time.Millisecond) // let the departed fire (present deleted)
	if _, j2 := s.hub.attach("c/worker"); !j2 {
		t.Fatal("a return after departed must be a new join")
	}
}

// BUG #1 regression (Fable review): a departed decided just before a join must be
// EMITTED before that join, so the last event a peer sees for a returning worker is
// the join — never a stuck departed. The fix is decide-order == emit-order: both are
// enqueued under mu, drained in order. Enqueue [departed, join] under one lock and
// assert the delivered order matches (last == join).
func TestPresenceOrderingDepartedBeforeJoin(t *testing.T) {
	s := presenceServer(t)
	go s.presenceDrainer()
	s.hub.attach("c/listener") // recipient

	s.hub.mu.Lock()
	s.hub.enqueue("c", "worker", "departed")
	s.hub.enqueue("c", "worker", "join")
	s.hub.mu.Unlock()

	lines := waitFor(t, s.store, "c", "listener", func(l []map[string]string) bool {
		return len(l) == 2
	})
	if lines[0]["event"] != "departed" || lines[1]["event"] != "join" {
		t.Fatalf("emit order must equal decision order [departed, join], got %v", lines)
	}
}

// A fanned-out presence line, once Reframed as the ws frame, renders kind=presence.
func TestPresenceReframeEndToEnd(t *testing.T) {
	s := presenceServer(t)
	s.hub.attach("c/listener")
	s.fanoutPresence("c", "joiner", "join", "connected as joiner")

	names, _ := s.store.ListNew("c", "listener")
	if len(names) != 1 {
		t.Fatalf("want 1 spool line, got %d", len(names))
	}
	raw, _ := s.store.Read("c", "listener", names[0])
	frame := string(core.Reframe([]byte(strings.TrimRight(string(raw), "\n"))))
	head := strings.SplitN(frame, "\n", 2)[0]
	if !strings.Contains(head, "kind=presence") {
		t.Fatalf("reframed presence must render kind=presence in the header: %q", head)
	}
	if !strings.Contains(frame, "connected as joiner") {
		t.Fatalf("reframed presence missing body text: %q", frame)
	}
}

// Concurrent attach/detach/scheduleDepart/poke storm on shared keys; -race must stay
// clean and it must not deadlock. Exercises the attach-vs-timer mutex discipline.
func TestConcurrentHubStress(t *testing.T) {
	s := presenceServer(t)
	s.hub.grace = time.Millisecond
	go s.presenceDrainer()

	const workers, iters = 8, 400
	keys := []string{"c/w0", "c/w1", "c/w2", "c/w3"}
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				key := keys[(g+i)%len(keys)]
				tl, _ := s.hub.attach(key)
				s.hub.poke(key)
				if !s.hub.detach(key, tl) {
					s.hub.scheduleDepart(key)
				}
			}
		}(g)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("hub stress deadlocked")
	}
	time.Sleep(10 * time.Millisecond) // let straggler grace timers fire harmlessly
}
