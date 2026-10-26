package client

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
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

type opencodeDeliverReply struct {
	OK        bool     `json:"ok"`
	Receipt   string   `json:"receipt"`
	Texts     []string `json:"texts"`
	Present   bool     `json:"present"`
	Code      string   `json:"code"`
	Error     string   `json:"error"`
	Submitted any      `json:"submitted"`
}

// enqueue asks the plugin to submit and returns the message ID only when the
// plugin read the stored message back. A failure counts as not submitted only
// when the plugin says nothing could have been stored.
func (q *opencodeQueue) enqueue(threadID, messageID, payload string) (string, error) {
	if threadID != q.cfg.Binding.SessionID || !validOpencodeMessageID(messageID) || payload == "" {
		return "", &opencodeNotSubmittedError{Err: errors.New("exact session, OpenCode message ID and payload required")}
	}
	var r opencodeDeliverReply
	err := opencodeRequest(q.cfg.Binding.Endpoint, map[string]string{"op": "deliver", "sessionID": threadID, "messageID": messageID, "text": payload}, &r)
	if err != nil {
		if errors.Is(err, errOpencodeEndpointGone) || errors.Is(err, errOpencodeNotDialed) {
			return "", &opencodeNotSubmittedError{Err: err}
		}
		return "", fmt.Errorf("OpenCode submit outcome unknown: %w", err)
	}
	switch {
	case r.OK && (r.Receipt == "persisted" || r.Receipt == "present"):
		return messageID, nil
	case r.OK:
		return "", fmt.Errorf("OpenCode accepted the submit but the message was not read back (%s)", r.Receipt)
	case r.Submitted == false:
		return "", &opencodeNotSubmittedError{Err: fmt.Errorf("%s: %s", r.Code, r.Error)}
	default:
		return "", fmt.Errorf("OpenCode submit outcome unknown: %s: %s", r.Code, r.Error)
	}
}

func (q *opencodeQueue) lookupMessage(threadID, messageID string) (codexMessageLookup, error) {
	if threadID != q.cfg.Binding.SessionID || !validOpencodeMessageID(messageID) {
		return codexMessageLookup{}, errors.New("exact session and OpenCode message ID required")
	}
	var r opencodeDeliverReply
	if err := opencodeRequest(q.cfg.Binding.Endpoint, map[string]string{"op": "lookup", "sessionID": threadID, "messageID": messageID}, &r); err != nil {
		return codexMessageLookup{}, err
	}
	if !r.OK {
		return codexMessageLookup{}, fmt.Errorf("OpenCode lookup failed: %s: %s", r.Code, r.Error)
	}
	if !r.Present {
		return codexMessageLookup{State: codexMessageNotFound}, nil
	}
	return codexMessageLookup{State: codexMessageReceived, ItemID: messageID}, nil
}

// nativeAttemptKey is the ID an adapter knows an attempt by.
func nativeAttemptKey(c *ConnectionState, a queueAttempt) string {
	switch daemonHarness(c.Harness) {
	case daemonHarnessOpencode:
		return a.MessageID
	default:
		return a.ClientID
	}
}

// OpenCode's enqueue already read the stored message back, so it is the receipt.
func enqueueEvidence(c *ConnectionState, queueID string) *codexMessageLookup {
	switch daemonHarness(c.Harness) {
	case daemonHarnessOpencode:
		return &codexMessageLookup{State: codexMessageReceived, ItemID: queueID}
	default:
		return nil
	}
}

const opencodeIdleRetry = 2 * time.Second

// Mail waits in the bus inbox until the pane shows the bound session and that
// session is idle; a message submitted mid-turn can override the user's request.
func (d *busDaemon) opencodeDeliveryReady(c *ConnectionState) (bool, error) {
	if c.Opencode == nil {
		return false, errors.New("OpenCode connection binding is missing")
	}
	var r struct {
		OK        bool    `json:"ok"`
		SessionID *string `json:"sessionID"`
		State     *string `json:"state"`
	}
	err := opencodeRequest(c.Opencode.Binding.Endpoint, map[string]string{"op": "status"}, &r)
	ready := err == nil && r.OK && r.SessionID != nil && *r.SessionID == c.ThreadID && r.State != nil && *r.State == "idle"
	if !ready {
		d.setRetry(c.ID, time.Now().Add(opencodeIdleRetry))
	}
	return ready, nil
}

var opencodeIDCounter atomic.Uint32

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// newOpencodeMessageID follows OpenCode's ascending ID form, "msg_" plus 12 hex
// of milliseconds*4096+counter and 14 random base62, so bus messages sort where
// OpenCode's own messages of the same moment would.
func newOpencodeMessageID(now time.Time) string {
	n := uint64(now.UnixMilli())*0x1000 + uint64(opencodeIDCounter.Add(1)&0xfff)
	var tail [14]byte
	var r [14]byte
	_, _ = rand.Read(r[:])
	for i := range tail {
		tail[i] = base62[int(r[i])%len(base62)]
	}
	return fmt.Sprintf("msg_%012x%s", n&0xffffffffffff, tail[:])
}

func validOpencodeMessageID(id string) bool {
	tail, ok := strings.CutPrefix(id, "msg_")
	if !ok || len(tail) < 20 || len(tail) > 64 {
		return false
	}
	for _, c := range tail {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
