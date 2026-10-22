package client

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

type opencodeNotSubmittedError struct{ Err error }

func (e *opencodeNotSubmittedError) Error() string {
	return fmt.Sprintf("OpenCode message not submitted: %v", e.Err)
}

func (e *opencodeNotSubmittedError) Unwrap() error { return e.Err }

// The daemon re-verifies the caller's endpoint itself: the client's capture is
// a claim, the socket peer and the plugin's shown session are the evidence.
func prepareOpencodeAdmission(req ConnectRequest) (*OpencodeConnectionConfig, error) {
	if req.Opencode == nil || req.Config != (CodexQueueConfig{}) || req.Claude != nil {
		return nil, errors.New("OpenCode admission requires its exact caller binding, without Codex or Claude configuration")
	}
	b := *req.Opencode
	if err := validateOpencodeBinding(req.ThreadID, b); err != nil {
		return nil, err
	}
	if filepath.Dir(b.Endpoint.Socket) != opencodeEndpointDir() {
		return nil, fmt.Errorf("OpenCode endpoint %s is outside this daemon's endpoint directory %s", b.Endpoint.Socket, opencodeEndpointDir())
	}
	h, err := opencodeHelloAt(b.Endpoint)
	if err != nil {
		return nil, err
	}
	if h.shown() != req.ThreadID {
		return nil, fmt.Errorf("OpenCode TUI %d no longer shows session %s", b.Endpoint.PID, req.ThreadID)
	}
	return &OpencodeConnectionConfig{Binding: b}, nil
}

// One session is bound through one TUI: two panes on a session can each pass
// a lookup and then both submit the same message.
func (d *busDaemon) refuseSecondOpencodeEndpoint(req ConnectRequest, cfg *OpencodeConnectionConfig) error {
	for _, other := range d.statusSnapshots() {
		if daemonHarness(other.Harness) != req.Harness || other.ThreadID != req.ThreadID || other.Opencode == nil ||
			other.State == "detached" || other.State == "disconnected" {
			continue
		}
		e := other.Opencode.Binding.Endpoint
		if e == cfg.Binding.Endpoint {
			continue
		}
		if validateOpencodeEndpoint(e) == nil {
			return fmt.Errorf("OpenCode session %s is already bound through TUI pid %d as %s; connect from that pane or close it first", req.ThreadID, e.PID, ConnectionTarget(other))
		}
	}
	return nil
}

// the caller holds the connection lane
func (d *busDaemon) reconnectOpencode(c *ConnectionState, req ConnectRequest, cfg *OpencodeConnectionConfig) (*ConnectionState, error) {
	if c.Opencode == nil || cfg == nil {
		return nil, errors.New("existing OpenCode binding is unavailable; refusing implicit adoption")
	}
	if c.Opencode.Binding.SessionID != cfg.Binding.SessionID {
		return nil, errors.New("OpenCode session changed; refusing to redirect the existing connection")
	}
	if c.Relay != nil && (req.Relay == nil || c.Relay.Base != req.Relay.Base) {
		return nil, errors.New("relay endpoint changed; refusing to redirect an existing connection")
	}
	if c.Pending != nil && c.Opencode.Binding.Endpoint != cfg.Binding.Endpoint {
		return nil, errors.New("OpenCode has an unresolved pending attempt through its previous TUI; reconcile or explicitly abandon it before reconnecting from another TUI")
	}
	unlock, err := d.lockPeer(connectionLockChannel(c), c.Alias)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, ok := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
	if !ok || m.ConnectionID != c.ID || m.SessionID != c.ThreadID || m.Harness != daemonHarness(c.Harness) {
		return nil, errors.New("OpenCode alias ownership changed; refusing to update its binding")
	}
	inbox, err := openSharedRead(filepath.Join(d.peerDir(c), "inbox.jsonl"))
	if err != nil {
		return nil, inboxEpochRefusal(c, "refusing reconnect")
	}
	end := c.Offset
	if c.Pending != nil {
		end = c.Pending.End
	}
	err = d.adoptInboxEpoch(c, inbox, end, "refusing reconnect")
	inbox.Close()
	if err != nil {
		return nil, err
	}
	next := cloneConnection(c)
	next.Opencode, next.Relay = cfg, cloneRelay(req.Relay)
	d.closeQueue(c.ID)
	if _, err := d.queue(next); err != nil {
		return nil, err
	}
	if err := d.requireCLIConsumer(next); err != nil {
		d.closeQueue(c.ID)
		return nil, err
	}
	next.State, next.Error = connectionReadyState(next), ""
	if next.Pending != nil {
		next.State = pendingConnectionState(next, time.Now())
	}
	if err := d.save(next); err != nil {
		d.closeQueue(c.ID)
		return nil, err
	}
	*c = *next
	armErr := d.armLocked(c)
	unlock() // Presence fanout acquires its own ordered peer locks.
	deferred := d.deferConnect(c, armErr, d.connectPresence(c, false), d.flushPresence(c), d.connectRelay(c))
	d.setRetry(c.ID, time.Time{})
	return connectResult(c, deferred), nil
}

func observeOpencodeConsumer(c *ConnectionState) (consumerProbe, error) {
	unknown := consumerProbe{State: "unknown"}
	if err := validateConnectionBinding(c); err != nil {
		return unknown, err
	}
	e := c.Opencode.Binding.Endpoint
	exited, err := codexOwnerExited(e.PID, e.StartToken)
	if err != nil {
		return unknown, err
	}
	if exited {
		return consumerProbe{State: "exited", Detail: "bound OpenCode TUI exited; reconnect from the session"}, nil
	}
	h, err := opencodeHelloAt(e)
	if err != nil {
		return unknown, err
	}
	if h.shown() != c.ThreadID {
		return consumerProbe{State: "unknown", Detail: fmt.Sprintf("OpenCode TUI %d does not show session %s", e.PID, c.ThreadID)}, nil
	}
	return consumerProbe{State: "online", PID: e.PID, StartToken: e.StartToken,
		Detail: "exact bound OpenCode TUI and plugin socket observed showing the session"}, nil
}

type opencodeQueue struct {
	ctx    context.Context
	cancel context.CancelFunc
	cfg    OpencodeConnectionConfig
}

var _ nativeQueue = (*opencodeQueue)(nil)

// The queue opens without contacting the TUI: a connection must load while
// OpenCode is down.
func newOpencodeQueueContext(parent context.Context, cfg *OpencodeConnectionConfig) (nativeQueue, error) {
	if parent == nil || cfg == nil {
		return nil, errors.New("invalid OpenCode queue configuration")
	}
	if err := validateOpencodeBinding(cfg.Binding.SessionID, cfg.Binding); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	return &opencodeQueue{ctx: ctx, cancel: cancel, cfg: *cfg}, nil
}

func (q *opencodeQueue) Close() error { q.cancel(); return nil }

func (q *opencodeQueue) inspect(threadID string) (codexQueueThread, error) {
	if err := q.ctx.Err(); err != nil {
		return codexQueueThread{}, err
	}
	if threadID != q.cfg.Binding.SessionID {
		return codexQueueThread{}, errors.New("exact bound OpenCode session required")
	}
	return codexQueueThread{ID: threadID, Source: "cli", Cwd: q.cfg.Binding.Cwd}, nil
}

var errOpencodeDeliveryDisabled = errors.New("OpenCode delivery is not enabled in this cbus build; mail stays unread in the inbox")

func (q *opencodeQueue) enqueue(string, string, string) (string, error) {
	return "", &opencodeNotSubmittedError{Err: errOpencodeDeliveryDisabled}
}

func (q *opencodeQueue) lookupMessage(string, string) (codexMessageLookup, error) {
	return codexMessageLookup{}, errOpencodeDeliveryDisabled
}
