// cbus-relay — networked leg of claudebus. Accepts messages over HTTP and
// pushes them to listening sessions over WebSocket (armed via Claude Code's
// Monitor {ws:} source, which supports no custom headers — hence subprotocol
// token auth on /tail, the k8s-apiserver pattern).
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"claudebus/internal/core"
	"claudebus/relay/internal/spool"
	"claudebus/relay/internal/wire"
)

const (
	subprotoPrefix = "bearer.cbus."
	pingEvery      = 30 * time.Second
	pongGrace      = 90 * time.Second
	// presenceGrace debounces ws flap (the Monitor drops 1006 on sleep and re-arms)
	// before a detach becomes a `departed` presence event. Intentionally == pongGrace
	// TODAY but a DISTINCT constant: pongGrace is a transport keepalive measurement,
	// this is a UX debounce — coupling them would let a keepalive retune silently move
	// presence semantics. Note the departed floor is pingEvery+pongGrace (detach) PLUS
	// this (~3.5 min for silent death); do not shrink pongGrace to speed it up.
	presenceGrace = 90 * time.Second
)

// hub tracks the single active tail per peer plus last-seen times, and drives the
// presence state machine (cbus-ijx.5). `present` is whether a join has been emitted for
// a key without a matching departed. A detach schedules a departed after `grace` unless
// the peer reattaches; `departWait[key]` holds the seq (a global-monotonic `departSeq`)
// of the currently-valid timer, so a reconnect just deletes it and any already-fired
// timer sees the seq mismatch and no-ops — no lingering-Timer bookkeeping, no map leak.
// CRUCIALLY presence events are DECIDED and ENQUEUED onto `pending` under `mu` (in
// attach and in the departed timer), and a single drainer emits them in that order — so
// a join deciding exactly as a departed is mid-emit can never reorder into a stuck
// departed for a live peer: decide-order == emit-order closes that race (BUG the Fable
// review caught in the first cut, which committed under lock but fanned out off it).
type hub struct {
	mu         sync.Mutex
	tails      map[string]*tail // key channel/alias
	seen       map[string]time.Time
	present    map[string]bool
	departWait map[string]uint64 // key -> seq of the valid pending departed timer
	departSeq  uint64            // global-monotonic timer generation
	grace      time.Duration
	pending    []presenceEvent // ordered emit queue, drained by (*server).presenceDrainer
	wake       chan struct{}   // cap-1 drainer nudge (lost-wakeup-proof, like poke)
}

type presenceEvent struct{ channel, actor, event string }

type tail struct {
	notify chan struct{}
	done   chan struct{}
}

func newHub() *hub {
	return &hub{
		tails: map[string]*tail{}, seen: map[string]time.Time{},
		present: map[string]bool{}, departWait: map[string]uint64{},
		grace: presenceGrace, wake: make(chan struct{}, 1),
	}
}

// enqueue appends a presence event and nudges the drainer. Caller MUST hold mu so the
// append order equals the decision order (the drainer emits in that order).
func (h *hub) enqueue(channel, actor, event string) {
	h.pending = append(h.pending, presenceEvent{channel, actor, event})
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// attach registers a new tail for a peer, displacing any existing one, and enqueues a
// `join` iff this is a genuinely NEW presence. A reconnect within grace invalidates the
// pending departed (delete departWait) and is not a new join; a displacement keeps
// present=true so it is not a join either. Returns joined for the caller's logging/tests.
func (h *hub) attach(key string) (t *tail, joined bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.tails[key]; ok {
		close(old.done)
	}
	t = &tail{notify: make(chan struct{}, 1), done: make(chan struct{})}
	h.tails[key] = t
	h.seen[key] = time.Now()
	delete(h.departWait, key) // reconnect: invalidate any pending departed
	joined = !h.present[key]
	h.present[key] = true
	if joined {
		c, a, _ := strings.Cut(key, "/")
		h.enqueue(c, a, "join")
	}
	return t, joined
}

// detach drops a peer's tail unless it was already displaced by a newer tail, and
// reports displaced so the caller only schedules a departed for a real disconnect
// (a displaced tail leaving is a takeover, not a departure).
func (h *hub) detach(key string, t *tail) (displaced bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	displaced = h.tails[key] != t
	if !displaced {
		delete(h.tails, key)
	}
	h.seen[key] = time.Now()
	return displaced
}

// scheduleDepart arms a grace timer stamped with a fresh seq; when it fires it enqueues
// a departed iff the key is still gone, still present, and still the valid seq (a
// reconnect deleted departWait[key]; a re-schedule replaced the seq). Decision + enqueue
// are under mu, so they order correctly against a concurrent attach's join.
func (h *hub) scheduleDepart(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.departSeq++
	seq := h.departSeq
	h.departWait[key] = seq
	time.AfterFunc(h.grace, func() {
		h.mu.Lock()
		_, connected := h.tails[key]
		if !connected && h.present[key] && h.departWait[key] == seq {
			delete(h.present, key)
			delete(h.departWait, key)
			c, a, _ := strings.Cut(key, "/")
			h.enqueue(c, a, "departed")
		}
		h.mu.Unlock()
	})
}

func (h *hub) poke(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.tails[key]; ok {
		select {
		case t.notify <- struct{}{}:
		default:
		}
	}
	// sender activity is not receiver presence; only tails update seen
}

func (h *hub) touch(key string) {
	h.mu.Lock()
	h.seen[key] = time.Now()
	h.mu.Unlock()
}

func (h *hub) snapshot() (connected map[string]bool, seen map[string]time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connected = map[string]bool{}
	for k := range h.tails {
		connected[k] = true
	}
	seen = map[string]time.Time{}
	for k, v := range h.seen {
		seen[k] = v
	}
	return
}

type server struct {
	store spool.Store
	hub   *hub
	token string
}

func (s *server) bearerOK(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	want := "Bearer " + s.token
	return subtle.ConstantTimeCompare([]byte(auth), []byte(want)) == 1
}

// subprotoOK scans offered subprotocols for bearer.cbus.<token>; returns the
// matched protocol string (to echo in the 101) or "".
func (s *server) subprotoOK(r *http.Request) string {
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(header, ",") {
			p = strings.TrimSpace(p)
			if !strings.HasPrefix(p, subprotoPrefix) {
				continue
			}
			offered := strings.TrimPrefix(p, subprotoPrefix)
			if subtle.ConstantTimeCompare([]byte(offered), []byte(s.token)) == 1 {
				return p
			}
		}
	}
	return ""
}

func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.bearerOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req core.SendReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, core.MaxMessageBytes)).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !core.ValidName(req.Channel) || !core.ValidName(req.Alias) {
		http.Error(w, "bad channel/alias", http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	if req.From == "" {
		req.From = "unknown"
	}
	if req.TS == "" {
		req.TS = time.Now().UTC().Format(time.RFC3339)
	}
	// same event shape as the local bus inbox lines
	line, err := json.Marshal(map[string]string{
		"from": req.From,
		"to":   req.Channel + "/" + req.Alias,
		"ts":   req.TS,
		"text": req.Text,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name, err := s.store.Write(req.Channel, req.Alias, append(line, '\n'))
	if err != nil {
		http.Error(w, "spool: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.hub.poke(req.Channel + "/" + req.Alias)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"id":%q}`+"\n", name)
}

// reframe delegates to the shared framer in internal/core. Kept as a thin
// package-local wrapper so the delivery loop and reframe_test.go are unchanged
// by the extraction (pure refactor).
func reframe(payload []byte) []byte { return core.Reframe(payload) }

// fanoutPresence writes a presence event (cbus-ijx.5) into the spool of every
// CURRENTLY-CONNECTED peer on the channel except the actor, then pokes them — the
// identical path /send uses. Spool-mediated (not a direct ws push) so presence keeps
// FIFO order with messages, needs no per-tail plumbing, holds no lock across the
// writes (recipients come from a snapshot), and survives a drop between enqueue and
// drain. Offline peers are not queued to: roster catch-up stays `cbus list @host`.
// The stored line carries kind/event; only Reframe's rendered output crosses the wire.
func (s *server) fanoutPresence(channel, actor, event, text string) {
	connected, _ := s.hub.snapshot()
	ts := time.Now().UTC().Format(time.RFC3339)
	for key := range connected {
		c, a, ok := strings.Cut(key, "/")
		if !ok || c != channel || a == actor {
			continue
		}
		line, err := json.Marshal(map[string]string{
			"from": channel + "/" + actor, "to": channel + "/" + a,
			"ts": ts, "text": text, "kind": "presence", "event": event,
		})
		if err != nil {
			continue
		}
		if _, err := s.store.Write(channel, a, append(line, '\n')); err == nil {
			s.hub.poke(key)
		}
	}
}

// presenceDrainer emits queued presence events in decision order, one at a time and OFF
// the hub lock. It grabs the whole pending batch under a brief lock then resets it (keeps
// enqueue order == emit order, and drops the consumed backing array). Started once in main.
func (s *server) presenceDrainer() {
	for range s.hub.wake {
		s.hub.mu.Lock()
		batch := s.hub.pending
		s.hub.pending = nil
		s.hub.mu.Unlock()
		for _, ev := range batch {
			s.fanoutPresence(ev.channel, ev.actor, ev.event, presenceText(ev.event, ev.actor))
		}
	}
}

// presenceText renders honest connection-lifecycle wording (the relay observes ws
// attach/detach, not a client join/leave command).
func presenceText(event, actor string) string {
	if event == "departed" {
		return "departed (connection lost)"
	}
	return "connected as " + actor
}

func (s *server) handleTail(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	alias := r.URL.Query().Get("alias")
	if !core.ValidName(channel) || !core.ValidName(alias) {
		http.Error(w, "bad channel/alias", http.StatusBadRequest)
		return
	}
	proto := s.subprotoOK(r)
	if proto == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := wire.Upgrade(w, r, proto)
	if err != nil {
		log.Printf("tail %s/%s: upgrade: %v", channel, alias, err)
		return
	}
	conn.WriteTimeout = 10 * time.Second // a wedged client can't stall the loop
	key := channel + "/" + alias
	t, _ := s.hub.attach(key) // attach enqueues the join; the drainer emits it in order
	defer func() {
		if !s.hub.detach(key, t) { // a real disconnect (not a takeover) may become departed
			s.hub.scheduleDepart(key)
		}
	}()
	log.Printf("tail %s: connected", key)

	lastPong := time.Now()
	var pongMu sync.Mutex
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(pongGrace + pingEvery))
			op, payload, err := conn.ReadFrame()
			if err != nil {
				return
			}
			switch op {
			case wire.OpPing:
				_ = conn.WriteFrame(wire.OpPong, payload) // pong echoes ping data (RFC 6455)
			case wire.OpPong, wire.OpText:
				pongMu.Lock()
				lastPong = time.Now()
				pongMu.Unlock()
				s.hub.touch(key)
			case wire.OpClose:
				return
			}
		}
	}()

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	defer conn.Close()
	for {
		// drain the queue in order; each delivered message moves to cur/
		names, err := s.store.ListNew(channel, alias)
		if err != nil {
			log.Printf("tail %s: list: %v", key, err)
			return
		}
		for _, name := range names {
			// displacement check per message: a newer tail may own this queue
			// now — bail before delivering so two loops never interleave long
			select {
			case <-t.done:
				log.Printf("tail %s: displaced mid-drain", key)
				return
			default:
			}
			payload, err := s.store.Read(channel, alias, name)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue // a displacing tail already delivered it
				}
				log.Printf("tail %s: read %s: %v", key, name, err)
				return
			}
			payload = []byte(strings.TrimRight(string(payload), "\n"))
			if err := conn.WriteFrame(wire.OpText, reframe(payload)); err != nil {
				log.Printf("tail %s: write: %v (message stays queued)", key, err)
				return
			}
			if err := s.store.MarkDelivered(channel, alias, name); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue // lost the mark race to a displacing tail; benign
				}
				log.Printf("tail %s: mark %s: %v", key, name, err)
				return
			}
			s.hub.touch(key)
		}
		select {
		case <-t.notify:
		case <-t.done: // displaced by a newer tail
			log.Printf("tail %s: displaced", key)
			return
		case <-readerDone: // client went away
			log.Printf("tail %s: closed", key)
			return
		case <-ping.C:
			pongMu.Lock()
			stale := time.Since(lastPong) > pongGrace
			pongMu.Unlock()
			if stale {
				log.Printf("tail %s: pong timeout", key)
				return
			}
			if err := conn.WriteFrame(wire.OpPing, nil); err != nil {
				return
			}
		}
	}
}

func (s *server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if !s.bearerOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	queued, err := s.store.Peers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	connected, seen := s.hub.snapshot()
	out := map[string]core.PeersEntry{}
	for k, n := range queued {
		out[k] = core.PeersEntry{Connected: connected[k], LastSeen: seen[k], Queued: n}
	}
	for k := range connected {
		if _, ok := out[k]; !ok {
			out[k] = core.PeersEntry{Connected: true, LastSeen: seen[k]}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handlePrune drops spool peers with no queued mail and no live tail — the relay
// counterpart to the client's local `cbus prune`. The spool tree is append-only
// (a peer dir is born on its first queued write and never GC'd), so off peers
// accumulate in /peers forever; this reaps them. An optional ?channel= scopes it
// to one channel. A live tail (in the hub) or any queued message in new/ keeps a
// peer. Returns the removed "<channel>/<alias>" keys, sorted.
func (s *server) handlePrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.bearerOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel != "" && !core.ValidName(channel) {
		http.Error(w, "bad channel", http.StatusBadRequest)
		return
	}
	queued, err := s.store.Peers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	connected, _ := s.hub.snapshot()
	pruned := []string{}
	for key, n := range queued {
		c, a, _ := strings.Cut(key, "/")
		if channel != "" && c != channel {
			continue
		}
		if n > 0 || connected[key] {
			continue // pending mail or a live listener — keep it
		}
		if ok, _ := s.store.Remove(c, a); ok {
			pruned = append(pruned, key)
		}
	}
	sort.Strings(pruned)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(core.PruneResponse{Pruned: pruned})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "listen address (loopback; CF tunnel fronts it)")
	spoolDir := flag.String("spool", "spool", "maildir spool root")
	tokenFile := flag.String("token-file", "token", "file holding the app token")
	graceFlag := flag.Duration("presence-grace", presenceGrace, "grace before a dropped tail becomes a `departed` presence event")
	flag.Parse()

	token := strings.TrimSpace(os.Getenv("CBUS_RELAY_TOKEN"))
	if token == "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			log.Fatalf("no token: %v (set CBUS_RELAY_TOKEN or provide -token-file)", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		log.Fatal("empty token")
	}
	if strings.ContainsAny(token, "=,/ ") {
		log.Fatal("token must be subprotocol-safe (unpadded base64url or hex; no '=', ',', '/', spaces)")
	}

	s := &server{store: spool.Store{Root: *spoolDir}, hub: newHub(), token: token}
	s.hub.grace = *graceFlag
	go s.presenceDrainer() // emits queued join/departed events in decision order
	mux := http.NewServeMux()
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/tail", s.handleTail)
	mux.HandleFunc("/peers", s.handlePeers)
	mux.HandleFunc("/prune", s.handlePrune)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })

	log.Printf("cbus-relay listening on %s (spool %s)", *listen, *spoolDir)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
