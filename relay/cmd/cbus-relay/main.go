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
)

// hub tracks the single active tail per peer plus last-seen times.
type hub struct {
	mu    sync.Mutex
	tails map[string]*tail // key channel/alias
	seen  map[string]time.Time
}

type tail struct {
	notify chan struct{}
	done   chan struct{}
}

func newHub() *hub {
	return &hub{tails: map[string]*tail{}, seen: map[string]time.Time{}}
}

// attach registers a new tail for a peer, displacing any existing one.
func (h *hub) attach(key string) *tail {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.tails[key]; ok {
		close(old.done)
	}
	t := &tail{notify: make(chan struct{}, 1), done: make(chan struct{})}
	h.tails[key] = t
	h.seen[key] = time.Now()
	return t
}

func (h *hub) detach(key string, t *tail) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tails[key] == t {
		delete(h.tails, key)
	}
	h.seen[key] = time.Now()
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
	t := s.hub.attach(key)
	defer s.hub.detach(key, t)
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

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "listen address (loopback; CF tunnel fronts it)")
	spoolDir := flag.String("spool", "spool", "maildir spool root")
	tokenFile := flag.String("token-file", "token", "file holding the app token")
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
	mux := http.NewServeMux()
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/tail", s.handleTail)
	mux.HandleFunc("/peers", s.handlePeers)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })

	log.Printf("cbus-relay listening on %s (spool %s)", *listen, *spoolDir)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
