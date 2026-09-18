package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"claudebus/internal/core"
	"claudebus/internal/wire"
)

// RelayConfig pins routing and credential references from the connecting shell.
// Secrets are read when dialing and never enter the connection journal/status.
type RelayConfig struct {
	Host          string `json:"host"`
	Base          string `json:"base"`
	CredentialDir string `json:"credentialDir"`
}

func cloneRelay(c *RelayConfig) *RelayConfig {
	if c == nil {
		return nil
	}
	next := *c
	return &next
}

type relayObservation struct {
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	LastDurableID string `json:"lastDurableId,omitempty"`
}

func (d *busDaemon) relayState(id, state string, err error, durableID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.relayViews == nil {
		d.relayViews = map[string]relayObservation{}
	}
	v := d.relayViews[id]
	v.State, v.Error = state, ""
	if err != nil {
		v.Error = err.Error()
	}
	if durableID != "" {
		v.LastDurableID = durableID
	}
	d.relayViews[id] = v
}

func (d *busDaemon) decorateRelay(c *ConnectionState) {
	if c.Relay != nil {
		v, ok := d.relayViews[c.ID]
		if !ok {
			v.State = "not-started"
		}
		c.RelayStatus = &v
	}
}

func (d *busDaemon) connectRelay(c *ConnectionState, ready ...relaySocket) error {
	if c.Relay == nil {
		return nil
	}
	d.mu.Lock()
	running := d.relays[c.ID] != nil
	d.mu.Unlock()
	var socket relaySocket
	if len(ready) > 0 {
		socket = ready[0]
	}
	if !running && socket == nil {
		var err error
		socket, err = d.openRelay(c)
		if err != nil {
			d.relayState(c.ID, "unavailable", err, "")
			return err
		}
	}
	if err := d.writeRemoteIdentity(c); err != nil {
		if socket != nil {
			_ = socket.Abort()
		}
		return err
	}
	return d.startRelay(c, socket)
}

func (d *busDaemon) writeRemoteIdentity(c *ConnectionState) error {
	dir := filepath.Join(CBUSDir(), ".remote", c.Relay.Host, c.Channel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	owner := c.Config.RuntimePID
	if c.Consumer != nil && c.Consumer.PID > 0 {
		owner = c.Consumer.PID
	}
	type marker struct {
		Alias        string `json:"alias"`
		OwnerPID     int    `json:"ownerPid"`
		TS           string `json:"ts"`
		ConnectionID string `json:"connectionId"`
	}
	path := filepath.Join(dir, c.ThreadID)
	if prior, err := os.ReadFile(path); err == nil {
		var m marker
		if json.Unmarshal(prior, &m) == nil && m.Alias == c.Alias && m.OwnerPID == owner && m.ConnectionID == c.ID {
			return nil
		}
	}
	b, err := json.MarshalIndent(marker{c.Alias, owner, Now(), c.ID}, "", "  ")
	if err != nil {
		return err
	}
	if err := durableJSON(path, b); err != nil {
		return err
	}
	return syncRelayDirectories(dir)
}

func CodexRelayConfig(host string) (*RelayConfig, error) {
	if !core.ValidStoreName(host) {
		return nil, errors.New("invalid relay host")
	}
	fd, err := ResolveFrontDoor(host)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(credStoreDir(host))
	if err != nil {
		return nil, err
	}
	cfg := &RelayConfig{Host: host, Base: strings.TrimRight(fd.Base, "/"), CredentialDir: dir}
	return cfg, validateRelayConfig(cfg)
}

func validateRelayConfig(c *RelayConfig) error {
	if c == nil {
		return nil
	}
	u, err := url.Parse(c.Base)
	if !core.ValidStoreName(c.Host) || !filepath.IsAbs(c.CredentialDir) || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("relay requires a valid host, absolute credential directory and http(s) base URL without credentials/query/fragment")
	}
	return nil
}

func ConnectionTarget(c *ConnectionState) string {
	channel := c.Channel
	if c.Relay != nil {
		channel += "@" + c.Relay.Host
	}
	return channel + "/" + c.Alias
}

func connectionLockChannel(c *ConnectionState) string {
	if c.Relay != nil {
		return c.Channel + "@" + c.Relay.Host
	}
	return c.Channel
}

func sameRelay(a, b *RelayConfig) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Host == b.Host
}

type relaySocket interface {
	ReadFrame() (byte, []byte, error)
	WriteFrame(byte, []byte) error
	SetReadDeadline(time.Time) error
	Abort() error
}

type relaySubscription struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	socket relaySocket
}

func (s *relaySubscription) setSocket(socket relaySocket) {
	s.mu.Lock()
	s.socket = socket
	s.mu.Unlock()
}

func (s *relaySubscription) stop() {
	s.cancel()
	s.mu.Lock()
	if s.socket != nil {
		_ = s.socket.Abort()
	}
	s.mu.Unlock()
}

func relayToken(ctx context.Context, cfg *RelayConfig) (string, error) {
	var token string
	if runtime.GOOS == "darwin" {
		cmd := boundedCmd(ctx, "security", "find-generic-password", "-s", credServicePrefix+cfg.Host, "-a", "token", "-w")
		b, err := cmd.Output()
		if err == nil {
			token = strings.TrimRight(string(b), "\n")
		}
	} else {
		f, err := os.Open(filepath.Join(cfg.CredentialDir, "token"))
		if err == nil {
			b, readErr := io.ReadAll(io.LimitReader(f, 4097))
			_ = f.Close()
			if readErr == nil && len(b) <= 4096 {
				token = string(b)
			}
		}
	}
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n ,\t") {
		return "", fmt.Errorf("relay token unavailable for %s; run cbus auth set %s --token -", cfg.Host, cfg.Host)
	}
	return token, nil
}

// The distinct endpoint cannot accidentally consume legacy /tail frames before
// discovering that an older relay lacks durable ACK support.
func dialDurableRelay(ctx context.Context, c *ConnectionState) (relaySocket, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	token, err := relayToken(ctx, c.Relay)
	if err != nil {
		return nil, err
	}
	return dialRelayWithToken(ctx, c, token)
}

func dialRelayWithToken(ctx context.Context, c *ConnectionState, token string) (relaySocket, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u := WSURL(c.Relay.Base) + "/tail/durable-v1?" + url.Values{"channel": {c.Channel}, "alias": {c.Alias}, "consumer": {c.ID}}.Encode()
	conn, err := wire.DialContext(ctx, u, "bearer.cbus."+token, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("durable relay unavailable; server must support /tail/durable-v1 (upgrade the relay): %w", err)
	}
	conn.WriteTimeout = 5 * time.Second
	stop := context.AfterFunc(ctx, func() { _ = conn.Abort() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	op, b, err := conn.ReadFrame()
	var ready struct{ Type, Protocol string }
	if err != nil || op != wire.OpText || json.Unmarshal(b, &ready) != nil || ready.Type != "ready" || ready.Protocol != "cbus-relay-durable/v1" {
		_ = conn.Abort()
		return nil, errors.New("relay lacks durable-v1 ready handshake; upgrade the relay before connecting")
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

func (d *busDaemon) openRelay(c *ConnectionState) (relaySocket, error) {
	if d.dialRelay != nil {
		return d.dialRelay(d.ctx, c)
	}
	return dialDurableRelay(d.ctx, c)
}

// A subscription owns only the WebSocket and durable inbox appends. Native model
// work stays in the bounded delivery scheduler; idle reconnects do no model work.
func (d *busDaemon) startRelay(c *ConnectionState, ready relaySocket) error {
	if c.Relay == nil {
		return nil
	}
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		if ready != nil {
			_ = ready.Abort()
		}
		return errDaemonStopping
	}
	if d.relays == nil {
		d.relays = map[string]*relaySubscription{}
	}
	if d.relays[c.ID] != nil {
		d.mu.Unlock()
		if ready != nil {
			_ = ready.Abort()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(d.ctx)
	s := &relaySubscription{cancel: cancel, done: make(chan struct{}), socket: ready}
	d.relays[c.ID] = s
	d.relayWorkers.Add(1)
	d.mu.Unlock()
	go d.runRelay(ctx, c.ID, s, ready)
	return nil
}

func (d *busDaemon) stopRelay(id string) {
	d.mu.Lock()
	s := d.relays[id]
	delete(d.relays, id)
	d.mu.Unlock()
	if s != nil {
		s.stop()
		<-s.done
	}
	d.relayState(id, "stopped", nil, "")
}

func (d *busDaemon) runRelay(ctx context.Context, id string, sub *relaySubscription, conn relaySocket) {
	defer d.relayWorkers.Done()
	defer close(sub.done)
	defer func() {
		sub.stop()
		d.mu.Lock()
		if d.relays[id] == sub {
			delete(d.relays, id)
			v := d.relayViews[id]
			v.State, v.Error = "stopped", ""
			d.relayViews[id] = v
		}
		d.mu.Unlock()
	}()
	for ctx.Err() == nil {
		c := d.snapshot(id)
		if c == nil || c.State == "detached" || c.State == "disconnected" && len(c.PresenceOutbox) == 0 {
			return
		}
		if conn == nil {
			var err error
			if d.dialRelay != nil {
				conn, err = d.dialRelay(ctx, c)
			} else {
				conn, err = dialDurableRelay(ctx, c)
			}
			if err != nil {
				d.relayState(id, "reconnecting", err, "")
				if !relayWait(ctx, time.Second) {
					return
				}
				continue
			}
			sub.setSocket(conn)
		}
		d.relayState(id, "connected", nil, "")
		err := d.consumeRelay(ctx, id, conn)
		if ctx.Err() == nil {
			d.relayState(id, "reconnecting", err, "")
		}
		_ = conn.Abort()
		sub.setSocket(nil)
		conn = nil
		if !relayWait(ctx, time.Second) {
			return
		}
	}
}

func relayWait(ctx context.Context, duration time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(duration):
		return true
	}
}

type relayFrame struct {
	Type    string          `json:"type"`
	SpoolID string          `json:"spoolId,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
	EventID string          `json:"eventId,omitempty"`
	Event   string          `json:"event,omitempty"`
	Text    string          `json:"text,omitempty"`
	TS      string          `json:"ts,omitempty"`
}

func writeRelayFrame(conn relaySocket, frame relayFrame) error {
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.WriteFrame(wire.OpText, b)
}

// A second goroutine publishes persisted presence intent. Reads remain single
// threaded, and the wire writer serializes ACKs, pongs and presence frames.
func (d *busDaemon) consumeRelay(ctx context.Context, id string, conn relaySocket) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Abort() })
	defer stop()
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		sent := ""
		for ctx.Err() == nil {
			c := d.snapshot(id)
			if c == nil || c.State == "detached" {
				_ = conn.Abort()
				return
			}
			if len(c.PresenceOutbox) > 0 {
				p := c.PresenceOutbox[0]
				if p.ID != sent {
					if err := d.publishRelayPresence(ctx, c, conn, p); err != nil {
						_ = conn.Abort()
						return
					}
					sent = p.ID
				}
			} else if c.State == "disconnected" {
				_ = conn.Abort()
				return
			}
			if !relayWait(ctx, 100*time.Millisecond) {
				return
			}
		}
	}()
	defer func() { cancel(); _ = conn.Abort(); <-publisherDone }()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		op, b, err := conn.ReadFrame()
		if err != nil {
			return err
		}
		switch op {
		case wire.OpPing:
			if err := conn.WriteFrame(wire.OpPong, b); err != nil {
				return err
			}
		case wire.OpPong:
		case wire.OpClose:
			return io.EOF
		case wire.OpText:
			var frame relayFrame
			if json.Unmarshal(b, &frame) != nil {
				return errors.New("invalid durable relay frame")
			}
			switch frame.Type {
			case "message":
				c := d.snapshot(id)
				if c == nil {
					return errDaemonStopping
				}
				if c.State == "disconnected" {
					continue
				} // no ACK: stays on the relay.
				if err := d.appendRelay(ctx, c, frame); err != nil {
					return err
				}
				d.relayState(id, "connected", nil, frame.SpoolID)
				if err := writeRelayFrame(conn, relayFrame{Type: "ack", SpoolID: frame.SpoolID}); err != nil {
					return err
				}
			case "presence-ack":
				if err := d.ackRelayPresence(ctx, id, frame.EventID); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported durable relay frame %q", frame.Type)
			}
		default:
			return errors.New("unsupported relay WebSocket frame")
		}
	}
}

func (d *busDaemon) publishRelayPresence(ctx context.Context, c *ConnectionState, conn relaySocket, p presenceTransition) error {
	unlock, err := lockPeerForContext(ctx, connectionLockChannel(c), c.Alias, 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if owns, err := d.ownership(c); err != nil || !owns {
		return errors.New("presence source registration changed")
	}
	return writeRelayFrame(conn, relayFrame{Type: "presence", EventID: p.ID, Event: p.Event, Text: p.Text, TS: p.TS})
}

func (d *busDaemon) ackRelayPresence(ctx context.Context, id, eventID string) error {
	if eventID == "" {
		return errors.New("empty presence acknowledgement")
	}
	for ctx.Err() == nil {
		c, finish, err := d.beginOperation(id)
		if errors.Is(err, errDaemonBusy) {
			if relayWait(ctx, 10*time.Millisecond) {
				continue
			}
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		defer finish()
		if c == nil || len(c.PresenceOutbox) == 0 || c.PresenceOutbox[0].ID != eventID {
			return errors.New("unexpected presence acknowledgement")
		}
		next := cloneConnection(c)
		next.PresenceOutbox = next.PresenceOutbox[1:]
		if err := d.save(next); err != nil {
			return err
		}
		*c = *next
		return nil
	}
	return ctx.Err()
}

// Relay IDs are scoped to this connection's immutable inbox epoch. Dedup is in
// the same append-only file as the message, so no two-file commit can lose a frame.
func (d *busDaemon) appendRelay(ctx context.Context, c *ConnectionState, frame relayFrame) (err error) {
	if frame.SpoolID == "" || len(frame.SpoolID) > 256 || strings.ContainsAny(frame.SpoolID, "/\\\r\n") {
		return errors.New("invalid relay spool ID")
	}
	var message core.Message
	if len(frame.Message) == 0 || string(frame.Message) == "null" || json.Unmarshal(frame.Message, &message) != nil || message.To != c.Channel+"/"+c.Alias || message.From == "" || message.Text == "" {
		return errors.New("invalid relay message or recipient")
	}
	if message.Kind != "" && message.Kind != "presence" {
		return errors.New("unsupported relay message kind")
	}
	// Relay presence stores local channel/alias; qualify it for exact remote replies.
	if !IsRemote(message.From) {
		if ch, alias, e := ParseLocal(message.From); e == nil && ch != "" {
			message.From = ch + "@" + c.Relay.Host + "/" + alias
		}
	}
	message.To = ConnectionTarget(c)
	line, err := json.Marshal(struct {
		core.Message
		RelayID string `json:"relayId"`
	}{message, frame.SpoolID})
	if err != nil {
		return err
	}
	if len(line) > daemonMaxInboxLine {
		return errors.New("relay message exceeds durable inbox line limit")
	}
	unlock, err := lockPeerForContext(ctx, connectionLockChannel(c), c.Alias, 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	current := d.snapshot(c.ID)
	if current == nil || current.State == "disconnected" || current.State == "detached" {
		return errors.New("relay subscription stopped")
	}
	owned, err := d.ownership(c)
	if err != nil || !owned {
		return errors.New("relay registration removed or replaced")
	}
	f, err := os.OpenFile(filepath.Join(d.peerDir(c), "inbox.jsonl"), os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok || dev != c.Dev || ino != c.Ino || size < current.Offset {
		return errors.New("relay inbox epoch changed")
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	for {
		prior, e := readDaemonLine(reader)
		if errors.Is(e, io.EOF) {
			if len(prior) > 0 {
				return errors.New("relay inbox has a partial trailing record; refusing to acknowledge")
			}
			break
		}
		if e != nil {
			return e
		}
		var identity struct {
			RelayID string `json:"relayId"`
		}
		if json.Unmarshal(prior, &identity) != nil {
			return errors.New("relay inbox contains invalid JSON")
		}
		if identity.RelayID == frame.SpoolID {
			if strings.TrimSpace(string(prior)) != string(line) {
				return errors.New("relay ID repeated with different message bytes")
			}
			if err := f.Sync(); err != nil {
				return err
			}
			return d.syncRelayParents(c) // prior append may have crashed before fsync/ACK.
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return d.syncRelayParents(c)
}

func (d *busDaemon) syncRelayParents(c *ConnectionState) error {
	// Persist newly-created directory entries as well as message bytes before
	// acknowledging. This also covers a crash during the initial registration.
	return syncRelayDirectories(d.peerDir(c))
}

func syncRelayDirectories(start string) error {
	stop := filepath.Dir(filepath.Clean(CBUSDir()))
	for dir := start; ; dir = filepath.Dir(dir) {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
		if dir == stop || filepath.Dir(dir) == dir {
			return nil
		}
	}
}
