package client

// The local supervisor owns durable bus connections, not terminals or model turns.
// Codex is the first adapter. Its sidecar only reads and queues; the original CLI
// remains the thread owner and decides when to consume queued input.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"claudebus/internal/core"
	"claudebus/internal/dirsync"
)

type ConnectRequest struct {
	Protocol int                   `json:"protocol,omitempty"`
	Harness  string                `json:"harness,omitempty"`
	Channel  string                `json:"channel"`
	Alias    string                `json:"alias,omitempty"`
	ThreadID string                `json:"threadId"`
	Config   CodexQueueConfig      `json:"config"`
	Claude   *ClaudeConnectBinding `json:"claude,omitempty"`
	Relay    *RelayConfig          `json:"relay,omitempty"`
	Host     string                `json:"host,omitempty"`
}

// ConnectionState reports queue acceptance separately from recipient receipt.
// Pending survives ambiguous RPC outcomes. Absence in a subsequent queue/history
// read is NOT proof of rejection, so an uncertain submission is never replayed.
type ConnectionState struct {
	ID               string                  `json:"id"`
	Harness          string                  `json:"harness,omitempty"`
	Channel          string                  `json:"channel"`
	Alias            string                  `json:"alias"`
	ThreadID         string                  `json:"threadId"`
	Config           CodexQueueConfig        `json:"config"`
	Claude           *ClaudeConnectionConfig `json:"claude,omitempty"`
	RecordedVersion  string                  `json:"recordedVersion"`
	State            string                  `json:"state"`
	Error            string                  `json:"error,omitempty"`
	ListenerError    string                  `json:"listenerError,omitempty"`
	Accepted         uint64                  `json:"accepted"`
	LastQueueID      string                  `json:"lastQueueId,omitempty"`
	Dev              uint64                  `json:"dev"`
	Ino              uint64                  `json:"ino"`
	Offset           int64                   `json:"offset"`
	Pending          *queueAttempt           `json:"pending,omitempty"`
	LastAccepted     *deliveryObservation    `json:"lastAccepted,omitempty"`
	Abandoned        uint64                  `json:"abandoned"`
	Resolutions      []abandonedAttempt      `json:"resolutions,omitempty"`
	RolloutPath      string                  `json:"rolloutPath,omitempty"`
	Consumer         *consumerObservation    `json:"consumer,omitempty"`
	PresenceSequence uint64                  `json:"presenceSequence,omitempty"`
	PresenceOutbox   []presenceTransition    `json:"presenceOutbox,omitempty"`
	Relay            *RelayConfig            `json:"relay,omitempty"`
	RelayStatus      *relayObservation       `json:"relayStatus,omitempty"`
	Compaction       *compactionObservation  `json:"compaction,omitempty"`
	Deferred         string                  `json:"deferred,omitempty"`            // set only on the copy a connect returns, never stored
	Predecessor      *ArchivedConnection     `json:"archivedPredecessor,omitempty"` // likewise
}

type queueAttempt struct {
	ClientID string              `json:"clientId"`
	End      int64               `json:"end"`
	Hash     string              `json:"hash"`
	QueueID  string              `json:"queueId,omitempty"`
	Evidence *codexMessageLookup `json:"evidence,omitempty"`
	// Set only after a clean Claude socket write; it anchors the receipt deadline.
	SubmittedAt time.Time `json:"submittedAt,omitzero"`
}

type nativeQueue interface {
	inspect(string) (codexQueueThread, error)
	enqueue(string, string, string) (string, error)
	lookupMessage(string, string) (codexMessageLookup, error)
	Close() error
}

type busDaemon struct {
	mu            sync.Mutex
	root          string
	start         string
	version       string
	connections   map[string]*ConnectionState
	queues        map[string]nativeQueue
	openQueue     func(CodexQueueConfig) (nativeQueue, error)
	nextTry       map[string]time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	connectGate   chan struct{} // one connect at a time; waiters queue rather than fail
	controlWait   time.Duration
	skipped       []string // connection records load could not read or validate
	lanes         map[string]chan struct{}
	snapshots     map[string]*ConnectionState
	slots         chan struct{}
	workers       sync.WaitGroup
	closing       bool
	rotation      int           // next connection a pass visits, guarded by mu
	passLeft      int           // connections the current pass has yet to visit, guarded by mu
	kick          chan struct{} // wakes the scheduler loop: a wake, a freed slot or a released lane
	ready         map[string]struct{}
	due           map[string]time.Time // receipt follow-ups, guarded by mu
	followups     map[string]int
	probeConsumer func(context.Context, *ConnectionState) (consumerProbe, error)
	relays        map[string]*relaySubscription
	relayViews    map[string]relayObservation
	tickErrors    map[string]string // last logged tick error per connection, guarded by mu
	lastGC        *GCStatus         // guarded by mu
	gcFirst       time.Duration
	gcEvery       time.Duration
	relayWorkers  sync.WaitGroup
	dialRelay     func(context.Context, *ConnectionState) (relaySocket, error)
}

const DaemonProtocolVersion = 3

func DaemonDir() string    { return filepath.Join(CBUSDir(), ".daemon") }
func daemonSocket() string { return filepath.Join(DaemonDir(), "control.sock") }

func newBusDaemon() *busDaemon {
	ctx, cancel := context.WithCancel(context.Background())
	d := &busDaemon{root: DaemonDir(), version: "dev", connections: map[string]*ConnectionState{}, queues: map[string]nativeQueue{}, nextTry: map[string]time.Time{},
		ctx: ctx, cancel: cancel, lanes: map[string]chan struct{}{}, connectGate: make(chan struct{}, 1), controlWait: daemonControlWait, snapshots: map[string]*ConnectionState{}, slots: make(chan struct{}, daemonMaxOperations),
		kick: make(chan struct{}, 1), ready: map[string]struct{}{}, due: map[string]time.Time{}, followups: map[string]int{}, relays: map[string]*relaySubscription{}, relayViews: map[string]relayObservation{}, tickErrors: map[string]string{}, gcFirst: gcFirstPassDelay, gcEvery: gcPassInterval}
	d.openQueue = func(c CodexQueueConfig) (nativeQueue, error) { return newCodexQueueContext(d.ctx, c) }
	return d
}

// WakeDaemon tells the local daemon that these managed connections have new
// inbox lines. Best effort and bounded: no daemon, an older daemon without the
// endpoint, or a slow one leaves delivery to its sweep. It never starts a daemon.
func WakeDaemon(connectionIDs ...string) {
	connectionIDs = slices.DeleteFunc(connectionIDs, func(id string) bool { return id == "" })
	if len(connectionIDs) == 0 {
		return // unmanaged peers have no daemon to wake
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = DaemonCallContext(ctx, "POST", "/wake", wakeRequest{ConnectionIDs: connectionIDs}, nil)
}

type wakeRequest struct {
	ConnectionIDs []string `json:"connectionIds"`
}

// managedConnectionID names the daemon connection behind a peer, or "".
func managedConnectionID(metaPath string) string {
	m, _ := ReadPeerMeta(metaPath)
	return m.ConnectionID
}

// DaemonCall uses a private local socket. A caller's identity/config is explicit
// in the request, never inherited from the session that happened to start cbusd.
func DaemonCall(method, path string, in, out any) error {
	return DaemonCallContext(context.Background(), method, path, in, out)
}

func DaemonCallContext(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", daemonSocket())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, "http://cbus"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := client.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		return fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(out)
	}
	return nil
}

// RunDaemon runs in the foreground; the CLI's start verb detaches it. flock
// prevents a second daemon from stealing a live socket, including during startup.
func RunDaemon(ctx context.Context, versions ...string) error {
	d := newBusDaemon()
	defer d.cancel()
	if len(versions) > 0 && versions[0] != "" {
		d.version = versions[0]
	}
	if err := os.MkdirAll(d.root, 0700); err != nil {
		return err
	}
	if err := os.Chmod(d.root, 0700); err != nil {
		return err
	}
	if err := restrictToOwner(d.root); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(d.root, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = tryLockExclusive(lock); err != nil {
		return fmt.Errorf("daemon already running or lock unavailable: %w", err)
	}
	defer unlockFile(lock)
	d.start, err = procStartTime(os.Getpid())
	if err != nil {
		return err
	}
	if err = d.load(); err != nil {
		return err
	}
	// Restore local acceptance before health can report this instance running.
	// Native queue startup remains lazy and never delays listener readiness.
	d.rearmLoaded(ctx)
	_ = os.Remove(daemonSocket())
	l, err := net.Listen("unix", daemonSocket())
	if err != nil {
		return err
	}
	defer l.Close()
	defer os.Remove(daemonSocket())
	if err = os.Chmod(daemonSocket(), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{Handler: d.handler(cancel), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	tick := time.NewTicker(daemonSweepInterval)
	defer tick.Stop()
	loopCtx, stopLoop := context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		d.scheduleLoop(loopCtx, tick.C)
	}()
	gcSettings, err := LoadGCSettings()
	if err != nil {
		daemonLogf("gc: %v; automatic collection is off", err)
		gcSettings.Auto = false
	}
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		d.runGC(loopCtx, gcSettings)
	}()
	shutdown := func() error {
		stopLoop()
		<-loopDone
		<-gcDone
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err := d.shutdown(c)
		if e := srv.Shutdown(c); err == nil {
			err = e
		}
		if err != nil {
			_ = srv.Close()
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return shutdown()
		case err := <-done:
			stopErr := shutdown()
			if errors.Is(err, http.ErrServerClosed) {
				return stopErr
			}
			return err
		}
	}
}

func (d *busDaemon) handler(stop context.CancelFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No browser-origin requests: this endpoint is local process IPC only.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser requests are not supported", 403)
			return
		}
		if r.URL.Path == "/health" && r.Method == "GET" {
			health := map[string]any{"running": true, "pid": os.Getpid(), "start": d.start, "protocol": DaemonProtocolVersion, "version": d.version, "fencedDisconnect": true, "wake": true, "gc": true}
			if len(d.skipped) > 0 {
				health["skippedRecords"] = d.skipped
			}
			d.mu.Lock()
			if d.lastGC != nil {
				health["lastGC"] = *d.lastGC
			}
			d.mu.Unlock()
			writeDaemonJSON(w, health)
			return
		}
		if r.URL.Path == "/gc" && r.Method == "POST" {
			var req gcRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			pass, err := d.collectConnections(r.Context(), GCLimits{Grace: time.Duration(req.GraceSeconds) * time.Second, Inactive: time.Duration(req.InactiveSeconds) * time.Second}, liveGCProbe())
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			for _, r := range pass.Records {
				if r.Collected {
					daemonLogf("%s: collected connection %s into %s", r.Target, r.ID, r.Archive)
				}
			}
			if pass.OrphanTokens > 0 {
				daemonLogf("gc: removed %d session tokens no record references", pass.OrphanTokens)
			}
			writeDaemonJSON(w, pass)
			return
		}
		if r.URL.Path == "/wake" && r.Method == "POST" {
			var req wakeRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, id := range req.ConnectionIDs {
				d.wake(id) // unknown ids are ignored
			}
			writeDaemonJSON(w, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/stop" && r.Method == "POST" {
			var expected struct {
				PID   int    `json:"pid"`
				Start string `json:"start"`
			}
			err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&expected)
			if err != nil && !errors.Is(err, io.EOF) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if (expected.PID != 0 || expected.Start != "") && (expected.PID != os.Getpid() || expected.Start != d.start) {
				http.Error(w, "daemon instance changed; nothing stopped", http.StatusConflict)
				return
			}
			writeDaemonJSON(w, map[string]bool{"stopping": true})
			stop()
			return
		}
		switch {
		case r.URL.Path == "/connect" && r.Method == "POST":
			var req connectWireRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			c, err := d.connectWithCredential(req.ConnectRequest, req.ClaudeToken)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			s := d.snapshot(c.ID)
			if s == nil {
				s = c
			}
			s.Deferred = c.Deferred
			s.Predecessor = d.archivedPredecessor(s)
			writeDaemonJSON(w, s)
		case r.URL.Path == "/connections" && r.Method == "GET":
			writeDaemonJSON(w, d.statusSnapshots())
		case r.URL.Path == "/disconnect" && r.Method == "POST":
			var req struct {
				Target string `json:"target"`
				disconnectFence
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			id, err := d.disconnectExact(req.Target, req.disconnectFence)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			writeDaemonJSON(w, map[string]any{"disconnected": true, "connectionId": id})
		case r.URL.Path == "/reconcile" && r.Method == "POST":
			var req struct {
				Target string `json:"target"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			c, err := d.reconcile(req.Target)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			writeDaemonJSON(w, d.snapshot(c.ID))
		case r.URL.Path == "/abandon" && r.Method == "POST":
			var req AbandonRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			c, err := d.abandon(req)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			writeDaemonJSON(w, d.snapshot(c.ID))
		default:
			http.NotFound(w, r)
		}
	})
}

// daemonLogf writes one timestamped daemon.log line.
func daemonLogf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s cbus daemon: %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"), fmt.Sprintf(format, args...))
}

func writeDaemonJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (d *busDaemon) load() error {
	dir := filepath.Join(d.root, "connections")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range es {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		c, err := readConnectionRecord(dir, e.Name())
		if err != nil {
			// one bad record must not stop delivery to every other connection
			d.skipped = append(d.skipped, e.Name())
			daemonLogf("skipped %v", err)
			continue
		}
		d.register(c)
	}
	return nil
}

func readConnectionRecord(dir, name string) (*ConnectionState, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	var c ConnectionState
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("connection %s: %w", name, err)
	}
	if c.ID == "" || name != c.ID+".json" || !core.ValidStoreName(c.Channel) || !core.ValidStoreName(c.Alias) || !uuidLike(c.ThreadID) {
		return nil, fmt.Errorf("invalid connection %s", name)
	}
	if err := validateConnectionAdapter(&c); err != nil {
		return nil, fmt.Errorf("connection %s: %w", name, err)
	}
	c.Harness = daemonHarness(c.Harness)
	if err := validateRelayConfig(c.Relay); err != nil {
		return nil, fmt.Errorf("connection %s: %w", name, err)
	}
	if err := validateConnectionBinding(&c); err != nil && c.State != "disconnected" && c.State != "detached" {
		c.State, c.Error = "binding-required", err.Error()
	}
	return &c, nil
}

func (d *busDaemon) save(c *ConnectionState) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := durableJSON(filepath.Join(d.root, "connections", c.ID+".json"), b); err != nil {
		return err
	}
	d.publish(c)
	return nil
}

// An attempt must reach durable storage before enqueue; acceptance and the cursor
// move together in the same atomic record. Unlike legacy follower cursors, errors
// here are fatal to this attempt and never justify advancing past unread mail.
func durableJSON(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return dirsync.Sync(filepath.Dir(path))
}

func writeDaemonMeta(dir string, m peerMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return durableJSON(filepath.Join(dir, "meta.json"), b)
}

func (d *busDaemon) queue(c *ConnectionState) (nativeQueue, error) {
	if err := validateConnectionAdapter(c); err != nil {
		return nil, err
	}
	if err := validateConnectionBinding(c); err != nil {
		return nil, err
	}
	// Config is keyed per connection: one peer's cwd/config must not overwrite another's.
	if q := d.cachedQueue(c.ID); q != nil {
		return q, nil
	}
	var q nativeQueue
	var err error
	if daemonHarness(c.Harness) == daemonHarnessClaude {
		q, err = newClaudeQueueContext(d.ctx, d.root, *c.Claude)
	} else {
		q, err = d.openQueue(c.Config)
	}
	if err != nil {
		return nil, err
	}
	// Track the process before inspection so stop can cancel a slow handshake.
	if err := d.cacheQueue(c.ID, q); err != nil {
		return nil, err
	}
	t, err := q.inspect(c.ThreadID)
	if err == nil && t.ID != c.ThreadID {
		err = fmt.Errorf("exact thread required; backend returned id=%q", t.ID)
	}
	if err != nil {
		d.closeQueue(c.ID)
		return nil, err
	}
	c.RecordedVersion = t.CliVersion
	if t.Path != "" {
		c.RolloutPath = t.Path
	}
	return q, nil
}

// Source is historical thread provenance, retained across frontend changes.
// Admit only a currently observed CLI; queue() must also work while it is down.
func (d *busDaemon) requireCLIConsumer(c *ConnectionState) error {
	if err := validateConnectionAdapter(c); err != nil {
		return err
	}
	probe := *c
	probe.Consumer = nil // A saved owner must not shadow this caller's runtime.
	p, err := d.consumerProbe(&probe)
	if err != nil {
		return fmt.Errorf("cannot verify current CLI consumer: %w", err)
	}
	if p.State != "online" || p.PID <= 0 || p.StartToken == "" {
		return fmt.Errorf("current %s CLI consumer not verified (%s); run connect from the exact CLI with normal process-inspection permission", daemonHarness(c.Harness), p.State)
	}
	if daemonHarness(c.Harness) == daemonHarnessClaude {
		if c.Claude == nil || p.PID != c.Claude.Binding.Endpoint.PID || p.StartToken != c.Claude.Binding.Endpoint.StartToken {
			return errors.New("Claude consumer does not match the caller's exact process identity")
		}
		return nil
	}
	if c.Config.RuntimePID != 0 || c.Config.RuntimeStartToken != "" || c.Config.BindingSource == "runtime-open-queue" {
		if p.PID != c.Config.RuntimePID || p.StartToken != c.Config.RuntimeStartToken {
			return errors.New("current CLI consumer does not match the caller's process identity; reconnect from the exact resumed CLI")
		}
	}
	return nil
}

func validateCodexQueueBinding(c CodexQueueConfig) error {
	if !filepath.IsAbs(c.Binary) || !filepath.IsAbs(c.Home) || !filepath.IsAbs(c.Cwd) || !filepath.IsAbs(c.SQLiteHome) || !filepath.IsAbs(c.UserHome) {
		return errors.New("Codex binary, home, cwd, SQLite store and user home must be bound to absolute paths; reconnect from the original current CLI to capture its storage binding")
	}
	return nil
}

func sameCodexQueueProcess(a, b CodexQueueConfig) bool {
	return a.Binary == b.Binary && a.Home == b.Home && a.Cwd == b.Cwd && a.SQLiteHome == b.SQLiteHome && a.UserHome == b.UserHome
}

func sameCodexConnectionHome(existing CodexQueueConfig, requested string) bool {
	if existing.Home == requested {
		return true
	}
	// Old journals predate canonical store capture. Resolve their CODEX_HOME
	// only for explicit migration; bound stores keep the exact comparison.
	if existing.SQLiteHome == "" {
		canonical, err := filepath.EvalSymlinks(existing.Home)
		return err == nil && canonical == requested
	}
	return false
}

func (d *busDaemon) connect(req ConnectRequest) (*ConnectionState, error) {
	return d.connectWithCredential(req, "")
}

func (d *busDaemon) connectWithCredential(req ConnectRequest, token string) (*ConnectionState, error) {
	if err := validateDaemonHarness(req.Harness); err != nil {
		return nil, err
	}
	if err := validateConnectEnvelope(req, token); err != nil {
		return nil, err
	}
	host, err := connectHostLabel(req.Host)
	if err != nil {
		return nil, err
	}
	req.Harness = daemonHarness(req.Harness)
	select {
	case d.connectGate <- struct{}{}:
		defer func() { <-d.connectGate }()
	case <-time.After(daemonConnectWait):
		return nil, errDaemonBusy
	case <-d.ctx.Done():
		return nil, errDaemonStopping
	}
	if err := checkStoreName("channel", req.Channel); err != nil {
		return nil, err
	}
	if req.Alias != "" {
		if err := checkStoreName("alias", req.Alias); err != nil {
			return nil, err
		}
	}
	if !uuidLike(req.ThreadID) {
		return nil, errors.New("connect requires the exact CLI session UUID")
	}
	claude, err := prepareConnectBinding(req, token)
	if err != nil {
		return nil, err
	}
	if err := validateRelayConfig(req.Relay); err != nil {
		return nil, err
	}
	if req.Relay != nil && req.Alias == "" {
		return nil, errors.New("remote connect requires an explicit alias")
	}
	for _, selected := range d.statusSnapshots() {
		sameHarness := daemonHarness(selected.Harness) == req.Harness
		if req.Relay != nil && sameRelay(selected.Relay, req.Relay) && selected.Channel == req.Channel && selected.Alias == req.Alias && selected.State != "detached" && (!sameHarness || selected.ThreadID != req.ThreadID || !sameConnectHome(selected, req)) {
			return nil, errors.New("remote alias is already managed by another exact thread/store; choose another alias")
		}
		if !sameHarness {
			continue
		}
		if selected.Channel == req.Channel && selected.ThreadID == req.ThreadID && sameRelay(selected.Relay, req.Relay) && sameConnectHome(selected, req) {
			c, finish, err := d.beginControl(selected.ID)
			if err != nil {
				return nil, err
			}
			if c == nil || c.State == "detached" || !d.owns(c) { // nil: collected since the snapshot
				finish()
				continue
			}
			defer finish()
			if req.Alias != "" && req.Alias != c.Alias {
				return nil, fmt.Errorf("this thread is already connected as %s/%s", c.Channel, c.Alias)
			}
			if req.Harness == daemonHarnessClaude {
				state, err := d.reconnectClaude(c, req, claude, token)
				if err == nil {
					d.wake(c.ID)
				}
				return state, err
			}
			if c.Config.SQLiteHome != "" && c.Config.SQLiteHome != req.Config.SQLiteHome {
				return nil, errors.New("Codex queue store changed; refusing to redirect an existing connection or its pending deliveries")
			}
			refresh := !sameCodexQueueProcess(c.Config, req.Config)
			if refresh {
				d.closeQueue(c.ID)
			}
			next := *c
			next.Harness = req.Harness
			next.Config = req.Config
			if c.Relay != nil && c.Relay.Base != req.Relay.Base {
				return nil, errors.New("relay endpoint changed; refusing to redirect an existing connection")
			}
			next.Relay = cloneRelay(req.Relay)
			if _, err := d.queue(&next); err != nil {
				return nil, err
			}
			if err := d.requireCLIConsumer(&next); err != nil {
				d.closeQueue(c.ID) // Never cache a new binding against the old journal.
				return nil, err
			}
			if next.State == "disconnected" || next.State == "binding-required" {
				if next.State == "disconnected" {
					next.Compaction = nil
				}
				next.State = "queue-ready"
				next.Error = ""
				if next.Pending != nil {
					next.State = "uncertain"
				}
			}
			if err := d.save(&next); err != nil {
				if refresh {
					d.closeQueue(c.ID)
				}
				return nil, err
			}
			*c = next
			deferred := d.deferConnect(c, d.arm(c), d.connectPresence(c, false), d.observeCompaction(c), d.flushPresence(c), d.connectRelay(c))
			d.wake(c.ID)
			return connectResult(c, deferred), nil
		}
	}
	id, err := randNonce()
	if err != nil {
		return nil, err
	}
	c := &ConnectionState{ID: id, Harness: req.Harness, Channel: req.Channel, Alias: req.Alias, ThreadID: req.ThreadID, Config: req.Config, Claude: claude, Relay: cloneRelay(req.Relay), Consumer: &consumerObservation{State: "unknown"}}
	c.State = connectionReadyState(c)
	_, finish, err := d.beginControl(c.ID)
	if err != nil {
		return nil, err
	}
	defer finish()
	if _, err = d.queue(c); err != nil {
		return nil, fmt.Errorf("native queue capability check: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			d.closeQueue(c.ID)
		}
	}()
	if err := d.requireCLIConsumer(c); err != nil {
		return nil, err
	}
	var ready relaySocket
	if c.Relay != nil {
		ready, err = d.openRelay(c)
		if err != nil {
			return nil, err
		}
		defer func() {
			if ready != nil {
				_ = ready.Abort()
			}
		}()
	}
	var dir string
	var unlock func()
	var reservation *peerMeta
	// Reservation inspection can replace ErrExist with a more specific refusal.
	var aliasExists bool
	if req.Alias == "" {
		c.Alias, dir, unlock, err = claimAliasLockedContext(d.ctx, req.Channel)
	} else {
		dir = d.peerDir(c)
		unlock, err = d.lockPeer(connectionLockChannel(c), c.Alias)
		if err == nil {
			if err = os.MkdirAll(filepath.Dir(dir), 0755); err == nil {
				err = os.Mkdir(dir, 0755)
				aliasExists = errors.Is(err, os.ErrExist)
				if c.Relay == nil && aliasExists {
					reservation, err = daemonReservation(dir, c.Channel, c.Alias)
				}
			}
		}
	}
	if unlock != nil {
		defer unlock()
	}
	if err != nil {
		if c.Claude != nil && aliasExists {
			m, ok := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
			if ok && m.ConnectionID == "" && m.SessionID == c.ThreadID {
				return nil, fmt.Errorf("cannot claim alias %s: this session has an unmanaged registration; stop any Monitor for this exact alias and explicitly leave it before native connect, or choose a fresh alias", ConnectionTarget(c))
			}
		}
		return nil, fmt.Errorf("claim alias (choose another alias or explicitly unregister the existing peer): %w", err)
	}
	f, err := createDaemonInbox(dir, reservation != nil)
	if err != nil {
		return nil, err
	}
	var ok bool
	c.Dev, c.Ino, _, ok = fileIdentityOf(f)
	if err = f.Close(); err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("cannot identify new inbox")
	}
	if c.Claude != nil {
		if err := d.persistClaudeCapability(c.Claude, token); err != nil {
			return nil, errors.Join(err, cleanupClaudeUnpublishedClaim(dir, c.Dev, c.Ino))
		}
	}
	if err = d.save(c); err != nil {
		return nil, err
	}
	now := Now()
	m := peerMeta{Alias: c.Alias, Channel: c.Channel, SessionID: c.ThreadID, Cwd: connectionCwd(c), ListenerPid: jsonNull, OwnerPid: jsonNull, Host: host, TS: now, LastActivity: now, Origin: OriginJoined, Harness: c.Harness, ConnectionID: c.ID}
	if reservation != nil {
		m.Origin, m.Model = reservation.Origin, reservation.Model
	}
	if err = writeDaemonMeta(dir, m); err != nil {
		return nil, err
	}
	if err = d.armLocked(c); err != nil {
		return nil, err
	}
	d.register(c)
	keep = true
	unlock() // Presence fanout acquires source and recipient locks in canonical order.
	deferred := d.deferConnect(c, d.connectPresence(c, true), d.observeCompaction(c), d.flushPresence(c), d.connectRelay(c, ready))
	ready = nil // connectRelay starts or aborts it on every path
	d.wake(c.ID)
	return connectResult(c, deferred), nil
}

const connectDeferredPrefix = "connected; retrying after connect: "

// deferConnect reports post-registration steps that failed. The connection is
// registered and the scheduler retries each of them, so connect still succeeds.
func (d *busDaemon) deferConnect(c *ConnectionState, errs ...error) string {
	err := errors.Join(errs...)
	if err == nil {
		return ""
	}
	deferred := strings.ReplaceAll(err.Error(), "\n", "; ")
	c.Error = connectDeferredPrefix + deferred
	daemonLogf("%s: %s", ConnectionTarget(c), c.Error)
	if saveErr := d.save(c); saveErr != nil {
		daemonLogf("persist %s: %v", ConnectionTarget(c), saveErr)
	}
	return deferred
}

func connectResult(c *ConnectionState, deferred string) *ConnectionState {
	out := cloneConnection(c)
	out.Deferred = deferred
	return out
}

func (d *busDaemon) peerDir(c *ConnectionState) string {
	if c.Relay != nil {
		return filepath.Join(d.root, "remote", c.ID)
	}
	return filepath.Join(CBUSDir(), c.Channel, c.Alias)
}
func (d *busDaemon) owns(c *ConnectionState) bool {
	ok, _ := d.ownership(c)
	return ok
}
func (d *busDaemon) ownership(c *ConnectionState) (bool, error) {
	b, err := os.ReadFile(filepath.Join(d.peerDir(c), "meta.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var m peerMeta
	if err = json.Unmarshal(b, &m); err != nil {
		return false, fmt.Errorf("read registration: %w", err)
	}
	return m.ConnectionID == c.ID && m.SessionID == c.ThreadID, nil
}
func (d *busDaemon) arm(c *ConnectionState) error {
	unlock, err := d.lockPeer(connectionLockChannel(c), c.Alias)
	if err != nil {
		return err
	}
	defer unlock()
	return d.armLocked(c)
}
func (d *busDaemon) armLocked(c *ConnectionState) error {
	path := filepath.Join(d.peerDir(c), "meta.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m peerMeta
	if err = json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.ConnectionID != c.ID || m.SessionID != c.ThreadID {
		return errors.New("connection epoch changed")
	}
	m.ListenerPid = json.RawMessage(fmt.Sprint(os.Getpid()))
	m.ListenerStart = d.start
	m.OwnerPid = jsonNull
	if c.Claude != nil && daemonHarness(c.Harness) == daemonHarnessClaude {
		m.Cwd = c.Claude.Binding.Cwd
	}
	if err := writeDaemonMeta(d.peerDir(c), m); err != nil {
		return err
	}
	c.ListenerError = ""
	return nil
}

func (d *busDaemon) disconnect(target string) error {
	_, err := d.disconnectExact(target, disconnectFence{})
	return err
}

// disconnectFence pins the registration and consumer incarnation a caller
// validated. Empty fields leave the operator verb unfenced.
type disconnectFence struct {
	ConnectionID  string `json:"connectionId,omitempty"`
	ThreadID      string `json:"threadId,omitempty"`
	ConsumerPID   int    `json:"consumerPid,omitempty"`
	ConsumerStart string `json:"consumerStart,omitempty"`
}

// The fence is checked under the connection lane and peer lock, so an
// unregister and reconnect of the same alias cannot slip in between.
func (d *busDaemon) disconnectExact(target string, f disconnectFence) (string, error) {
	c, unlock, finish, err := d.lockConnectionWithRelease(target)
	if err != nil {
		return "", err
	}
	defer finish()
	defer unlock()
	if (f.ConnectionID != "" && c.ID != f.ConnectionID) || (f.ThreadID != "" && c.ThreadID != f.ThreadID) {
		return "", fmt.Errorf("%s is now a different registration; refusing to disconnect it", target)
	}
	if f.ConsumerPID != 0 || f.ConsumerStart != "" {
		pid, start := pinnedConsumer(c)
		if pid != f.ConsumerPID || start != f.ConsumerStart {
			return "", fmt.Errorf("%s is bound to a different consumer process; refusing to disconnect it", target)
		}
	}
	// Retain the durable inbox/journal and commit departure with the disconnect.
	next := *c
	next.State, next.Error = "disconnected", ""
	if err := d.disconnectPresence(&next); err != nil {
		return "", err
	}
	if err := d.save(&next); err != nil {
		return "", err
	}
	*c = next
	d.closeQueue(c.ID)
	d.stopRelay(c.ID)
	b, err := os.ReadFile(filepath.Join(d.peerDir(c), "meta.json"))
	if err != nil {
		return "", err
	}
	var m peerMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	m.ListenerPid, m.ListenerStart, m.OwnerPid = json.RawMessage("-1"), "", jsonNull
	if err := writeDaemonMeta(d.peerDir(c), m); err != nil {
		return "", err
	}
	unlock()
	return c.ID, d.flushPresence(c)
}

func (d *busDaemon) deliver(c *ConnectionState) error {
	if err := validateConnectionAdapter(c); err != nil {
		return err
	}
	unlock, err := d.lockPeer(connectionLockChannel(c), c.Alias)
	if err != nil {
		return err
	}
	defer unlock()
	owns, err := d.ownership(c)
	if err != nil {
		return err
	}
	if !owns {
		d.stopRelay(c.ID)
		d.closeQueue(c.ID)
		c.State = "detached"
		c.Error = "registration removed or replaced; delivery stopped"
		return d.save(c)
	}
	inbox := filepath.Join(d.peerDir(c), "inbox.jsonl")
	dev, ino, size, ok := fileIdentity(inbox)
	if !ok || ino != c.Ino || size < c.Offset {
		return inboxEpochRefusal(c, "refusing to replay an unknown epoch")
	}
	// Do not start a sidecar or query the harness merely because it is idle.
	// A changed device is decided below, on the handle delivery reads.
	if size == c.Offset && c.Pending == nil && dev == c.Dev {
		if m, _ := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json")); m.ListenerStart != d.start {
			return d.armLocked(c)
		}
		return nil
	}
	f, err := os.Open(inbox)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := d.adoptInboxEpoch(c, f, c.Offset, "refusing to replay an unknown epoch"); err != nil {
		return err
	}
	if _, err = f.Seek(c.Offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 64<<10)
	line, err := readDaemonLine(r)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	end := c.Offset + int64(len(line))
	hash := fmt.Sprintf("%x", sha256.Sum256(line))
	var msg core.Message
	if err = json.Unmarshal(line, &msg); err != nil {
		return fmt.Errorf("invalid inbox line at %d: %w", c.Offset, err)
	}
	if c.Pending != nil && (c.Pending.End != end || c.Pending.Hash != hash) {
		return errors.New("pending message bytes changed; refusing another enqueue")
	}
	managed := c.Pending == nil && managedCodexConnection(c)
	if managed {
		if ready, err := d.managedCodexDeliveryReady(c); err != nil || !ready {
			return err
		}
	}
	q, err := d.queue(c)
	if err != nil {
		return err
	}
	if err = d.armLocked(c); err != nil {
		return err
	}
	if c.Pending != nil {
		evidence := c.Pending.Evidence
		if c.Pending.QueueID == "" && evidence == nil {
			found, err := d.lookup(c, q, c.Pending.ClientID)
			if err != nil {
				return err
			}
			if found.State == codexMessageNotFound && daemonHarness(c.Harness) == daemonHarnessClaude {
				return d.awaitClaudeReceipt(c)
			}
			if found.State == codexMessageNotFound {
				return errors.New("enqueue outcome uncertain; retained message needs reconciliation, not a blind retry")
			}
			evidence = &found
		}
		if daemonHarness(c.Harness) != daemonHarnessClaude {
			return d.accept(c, evidence)
		}
		attempt := *c.Pending
		if err := d.accept(c, evidence); err != nil {
			return err
		}
		logClaudeAttempt(c, attempt, "received")
		d.setRetry(c.ID, time.Time{})
		return nil
	}
	if managed {
		// queue initialization may have waited while the frontend exited.
		if ready, err := d.managedCodexDeliveryReady(c); err != nil || !ready {
			return err
		}
	}
	c.Pending = &queueAttempt{ClientID: fmt.Sprintf("cbus-%s-%d-%s", c.ID, c.Offset, hash[:16]), End: end, Hash: hash}
	c.State = "submitting"
	c.Error = ""
	if err = d.save(c); err != nil {
		c.Pending = nil
		return err
	}
	if !d.owns(c) {
		return errors.New("connection detached before enqueue")
	}
	queueID, err := q.enqueue(c.ThreadID, c.Pending.ClientID, nativeBusPayload(line, msg))
	var awaiting *claudeAwaitingReceiptError
	if daemonHarness(c.Harness) == daemonHarnessClaude && errors.As(err, &awaiting) && awaiting.State == claudeSubmitted && awaiting.Err == nil {
		// A clean write has no ACK. Wait for its receipt on the scheduler tick
		// with the sidecar kept, so lookups scan on from where they stopped.
		next := *c
		attempt := *c.Pending
		attempt.SubmittedAt = time.Now()
		next.Pending = &attempt
		next.State, next.Error = claudeAwaitingReceiptState, ""
		if e := d.save(&next); e != nil {
			d.closeQueue(c.ID)
			return errors.Join(err, e)
		}
		*c = next
		logClaudeAttempt(c, attempt, "submitted")
		return nil
	}
	if err != nil {
		var rejection *rpcError
		var claudeRejected *claudeNotSubmittedError
		if (daemonHarness(c.Harness) == daemonHarnessCodex && errors.As(err, &rejection) && rejection.Code == -32600) ||
			(daemonHarness(c.Harness) == daemonHarnessClaude && errors.As(err, &claudeRejected)) {
			// Only adapter-specific proof that no message was submitted can
			// release this attempt. Unconfirmed socket writes remain pending.
			next := *c
			next.Pending = nil
			next.State = "error"
			next.Error = err.Error()
			if e := d.save(&next); e != nil {
				return e
			}
			*c = next
		}
		// A broken sidecar is recreated next time; the pending record fences retries.
		d.closeQueue(c.ID)
		return err
	}
	c.Pending.QueueID = queueID
	return d.accept(c, nil)
}

// Absence before the deadline is still waiting. After it, the attempt is
// reported uncertain on the error backoff; lookups continue and nothing is resent.
func (d *busDaemon) awaitClaudeReceipt(c *ConnectionState) error {
	if !claudeReceiptWaiting(c, time.Now()) {
		return errors.New("Claude receipt not observed before the deadline; retained message needs reconciliation, not a blind retry")
	}
	if c.State == claudeAwaitingReceiptState && c.Error == "" {
		return nil
	}
	next := *c
	next.State, next.Error = claudeAwaitingReceiptState, ""
	if err := d.save(&next); err != nil {
		return err
	}
	*c = next
	return nil
}

func (d *busDaemon) accept(c *ConnectionState, evidence *codexMessageLookup) error {
	if daemonHarness(c.Harness) == daemonHarnessClaude && (c.Claude == nil || evidence == nil || evidence.State != codexMessageReceived || evidence.ReceiptOffset == nil) {
		return errors.New("Claude acceptance requires exact persisted user receipt; socket writes are unconfirmed")
	}
	// Keep the in-memory attempt intact on persistence failure, too.
	if evidence != nil {
		observed := *evidence
		c.Pending.Evidence = &observed
	}
	next := *c
	if c.Claude != nil {
		cfg := *c.Claude
		next.Claude = &cfg
	}
	next.Offset = c.Pending.End
	next.LastQueueID = c.Pending.QueueID
	next.LastAccepted = &deliveryObservation{Attempt: *c.Pending, State: "accepted", ObservedAt: Now()}
	if evidence != nil {
		next.LastAccepted.State = evidence.State
		next.LastAccepted.ItemID = evidence.ItemID
		next.LastAccepted.Attempt.QueueID = evidence.QueueID
		next.LastQueueID = evidence.QueueID
		if daemonHarness(c.Harness) == daemonHarnessClaude {
			if *evidence.ReceiptOffset < c.Claude.ReceiptOffset {
				return errors.New("Claude receipt cursor moved backwards")
			}
			next.Claude.ReceiptOffset = *evidence.ReceiptOffset
		}
	}
	next.LastAccepted.Attempt.Evidence = nil
	next.Pending = nil
	next.Accepted++
	if next.State != "disconnected" {
		next.State = connectionReadyState(c)
	}
	next.Error = ""
	if err := d.save(&next); err != nil {
		return err
	}
	*c = next
	if daemonHarness(c.Harness) == daemonHarnessClaude {
		d.closeQueue(c.ID) // Reopen with the newly committed receipt cursor.
	}
	return nil
}

// Relay delivery adds an ID and qualified addressing to a size-limited message.
const daemonMaxInboxLine = core.MaxMessageBytes + 4096

func readDaemonLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > daemonMaxInboxLine+1 {
			return nil, errors.New("inbox message exceeds limit")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err // partial trailing records are left unread until completed
	}
}
