package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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

// Forward delivers one relay mailbox to an outbound HTTPS webhook over the
// durable stream: POST each message, then ACK it. The relay owns the cursor and
// redelivers anything unacknowledged, so delivery is at-least-once and
// receivers dedupe on the payload id (the relay spool id).

const (
	forwardMaxText   = 16 * 1024
	forwardReadGrace = 2 * time.Minute
)

// WebhookConfig is the outbound endpoint; Key is sent as a bearer token.
type WebhookConfig struct {
	URL string
	Key string
}

// LoadWebhookConfig reads `url=` and `key=` lines from a file that must not be
// readable by group or others (the key is a credential).
func LoadWebhookConfig(path string) (WebhookConfig, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return WebhookConfig{}, err
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return WebhookConfig{}, fmt.Errorf("%s: mode %v is readable by others; chmod 600 it", path, fi.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		return WebhookConfig{}, err
	}
	defer f.Close()
	var c WebhookConfig
	sc := bufio.NewScanner(io.LimitReader(f, 64*1024))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return WebhookConfig{}, fmt.Errorf("%s: expected key=value lines", path)
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "url":
			c.URL = strings.TrimSpace(v)
		case "key":
			c.Key = strings.TrimSpace(v)
		default:
			return WebhookConfig{}, fmt.Errorf("%s: unknown key %q (want url, key)", path, strings.TrimSpace(k))
		}
	}
	if err := sc.Err(); err != nil {
		return WebhookConfig{}, err
	}
	if err := checkWebhookURL(c.URL); err != nil {
		return WebhookConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if c.Key == "" || strings.ContainsAny(c.Key, "\r\n") {
		return WebhookConfig{}, fmt.Errorf("%s: missing or invalid key", path)
	}
	return c, nil
}

// plain http is allowed only to loopback, for local receivers and tests.
func checkWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("missing or invalid url")
	}
	if u.Scheme == "https" {
		return nil
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("url must be https (plain http only to loopback)")
}

// ForwardOptions configures one forwarder. Base and Token address the relay.
type ForwardOptions struct {
	Channel, Host, Alias string
	Base, Token          string
	Resolve              func() (string, error) // re-picks the relay base before each dial
	Webhook              WebhookConfig
	PerMin, PerHour      int
	Timeout              time.Duration // one webhook request; 0 means the default
	MaxAttempts          int
	DeadLetter           string
	DryRun               bool
	SkipQueued           int  // first this many frames are the backlog: settle them without a POST
	IncludeSkipped       bool // replay also sends records skipped by SkipQueued
	Once                 bool // exit once the mailbox has been idle for OnceIdle
	OnceIdle             time.Duration
	Logf                 func(format string, a ...any)

	client *http.Client
	sleep  func(context.Context, time.Duration) bool
}

// DefaultDeadLetter is where messages that could not be delivered are kept.
func DefaultDeadLetter(channel, host, alias string) string {
	return filepath.Join(CBUSDir(), "forward", channel+"@"+host, alias+".deadletter.jsonl")
}

// DefaultForwardTimeout bounds one webhook request.
const DefaultForwardTimeout = 30 * time.Second

func newForwardClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultForwardTimeout
	}
	return &http.Client{
		Timeout: timeout,
		// a redirected POST would be replayed as a bodiless GET and look delivered
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ForwardConsumer is the durable consumer id; a stable id lets a restarted
// forwarder resume its own subscription while a different consumer is refused.
func ForwardConsumer(alias string) string { return "forward-" + alias }

type forwarder struct {
	ForwardOptions
	sent    []time.Time
	skipped map[string]bool // spool ids settled as backlog; a redelivered one only needs its ACK again
}

// Forward runs until ctx ends (or, with Once, until the mailbox is idle),
// reconnecting with backoff when the relay connection drops.
func Forward(ctx context.Context, o ForwardOptions) error {
	if o.PerMin <= 0 || o.PerHour <= 0 || o.MaxAttempts <= 0 {
		return errors.New("per-min, per-hour and max-attempts must be positive")
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.client == nil {
		o.client = newForwardClient(o.Timeout)
	}
	if o.sleep == nil {
		o.sleep = relayWait
	}
	if o.OnceIdle <= 0 {
		o.OnceIdle = 3 * time.Second
	}
	o.Base = strings.TrimRight(o.Base, "/")
	f := &forwarder{ForwardOptions: o, skipped: map[string]bool{}}
	delay := time.Second
	for {
		// a relay that was down at startup may be up now, and local beats the public door
		if f.Resolve != nil {
			if base, err := f.Resolve(); err == nil {
				f.Base = strings.TrimRight(base, "/")
			}
		}
		conn, err := f.dial(ctx)
		if err == nil {
			delay = time.Second
			err = f.session(ctx, conn)
			_ = conn.Abort()
			if err == nil {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if f.Once {
			return err
		}
		f.Logf("relay connection: %v; reconnecting in %s", err, delay)
		if !f.sleep(ctx, delay) {
			return ctx.Err()
		}
		delay = min(delay*2, time.Minute)
	}
}

func (f *forwarder) dial(ctx context.Context) (*wire.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	q := url.Values{"channel": {f.Channel}, "alias": {f.Alias}, "consumer": {ForwardConsumer(f.Alias)}}
	conn, err := wire.DialContext(ctx, WSURL(f.Base)+"/tail/durable-v1?"+q.Encode(), "bearer.cbus."+f.Token, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("durable relay unavailable (the relay must serve /tail/durable-v1): %w", err)
	}
	conn.WriteTimeout = 5 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	op, b, err := conn.ReadFrame()
	var ready struct{ Type, Protocol string }
	if err != nil || op != wire.OpText || json.Unmarshal(b, &ready) != nil || ready.Type != "ready" || ready.Protocol != "cbus-relay-durable/v1" {
		_ = conn.Abort()
		return nil, errors.New("relay lacks the durable-v1 ready handshake")
	}
	return conn, nil
}

// session answers pings on a reader goroutine so a slow webhook or a retry
// backoff never looks like a dead consumer to the relay.
func (f *forwarder) session(ctx context.Context, conn *wire.Conn) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wmu sync.Mutex
	write := func(op byte, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return conn.WriteFrame(op, b)
	}
	messages := make(chan relayFrame)
	go func() {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(forwardReadGrace))
			op, b, err := conn.ReadFrame()
			if err != nil {
				cancel(err)
				return
			}
			switch op {
			case wire.OpPing:
				if err := write(wire.OpPong, b); err != nil {
					cancel(err)
					return
				}
			case wire.OpClose:
				cancel(io.EOF)
				return
			case wire.OpText:
				var fr relayFrame
				if json.Unmarshal(b, &fr) != nil || fr.Type != "message" || fr.SpoolID == "" {
					cancel(errors.New("unexpected durable relay frame"))
					return
				}
				select {
				case messages <- fr:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	for {
		var idle <-chan time.Time
		if f.Once {
			idle = time.After(f.OnceIdle)
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-idle:
			return nil
		case fr := <-messages:
			var err error
			switch {
			case f.skipped[fr.SpoolID]:
			case f.SkipQueued > 0:
				if err = f.skip(fr); err == nil {
					f.skipped[fr.SpoolID] = true
					f.SkipQueued--
				}
			default:
				err = f.handle(ctx, fr)
			}
			if err != nil {
				return err // abandoned mid-delivery: no ACK, the relay redelivers
			}
			if f.DryRun {
				return nil // never ACK in a dry run; the relay holds the message for the real forwarder
			}
			ack, _ := json.Marshal(relayFrame{Type: "ack", SpoolID: fr.SpoolID})
			if err := write(wire.OpText, ack); err != nil {
				return err
			}
		}
	}
}

// handle returns nil once the message is settled (delivered, skipped or dead-lettered).
func (f *forwarder) handle(ctx context.Context, fr relayFrame) error {
	abandoned := func() error {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		return errors.New("delivery abandoned")
	}
	var m core.Message
	if json.Unmarshal(fr.Message, &m) != nil {
		return f.deadLetter(fr, "unreadable message")
	}
	if m.Kind == "presence" {
		f.Logf("skip presence %s", fr.SpoolID)
		return nil
	}
	body := forwardPayload(fr.SpoolID, m, fr.Message)
	if f.DryRun {
		f.Logf("dry-run %s from=%s %d bytes", fr.SpoolID, m.From, len(body))
		return nil
	}
	delay := 2 * time.Second
	for attempt := 1; ; attempt++ {
		if !f.throttle(ctx) {
			return abandoned()
		}
		status, err := f.post(ctx, body)
		switch {
		case err == nil && status/100 == 2:
			f.Logf("sent %s from=%s HTTP %d", fr.SpoolID, m.From, status)
			return nil
		case err == nil && status/100 == 4 && !retryableStatus(status):
			return f.deadLetter(fr, fmt.Sprintf("HTTP %d", status))
		}
		reason := fmt.Sprint(err)
		if err == nil {
			reason = fmt.Sprintf("HTTP %d", status)
		}
		if ctx.Err() != nil {
			return abandoned()
		}
		if attempt >= f.MaxAttempts {
			return f.deadLetter(fr, fmt.Sprintf("%s after %d attempts", reason, attempt))
		}
		f.Logf("retry %s in %s (attempt %d/%d): %s", fr.SpoolID, delay, attempt, f.MaxAttempts, reason)
		if !f.sleep(ctx, delay) {
			return abandoned()
		}
		delay = min(delay*2, 5*time.Minute)
	}
}

// auth, timeout and rate-limit answers can recover; other 4xx never will.
func retryableStatus(code int) bool {
	return code == 401 || code == 403 || code == 408 || code == 429
}

func forwardPayload(id string, m core.Message, raw json.RawMessage) []byte {
	text := m.Text
	body := map[string]any{"id": id, "from": m.From, "to": m.To, "ts": m.TS}
	if len(text) > forwardMaxText {
		body["truncated"] = len(text)
		text = strings.ToValidUTF8(text[:forwardMaxText], "")
	}
	body["text"] = text
	var extra struct {
		RelayID string `json:"relayId"`
	}
	_ = json.Unmarshal(raw, &extra)
	if extra.RelayID != "" {
		body["relayId"] = extra.RelayID
	}
	if m.EventID != "" {
		body["eventId"] = m.EventID
	}
	b, _ := json.Marshal(body)
	return b
}

func (f *forwarder) post(ctx context.Context, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.Webhook.Key)
	req.Header.Set("User-Agent", wire.UserAgentValue())
	resp, err := f.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the url.Error text repeats the webhook URL
		}
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// throttle holds (never drops) while over the rate cap, so two automated peers
// replying to each other cannot trigger unbounded webhook runs.
func (f *forwarder) throttle(ctx context.Context) bool {
	warned := false
	for {
		now := time.Now()
		for len(f.sent) > 0 && now.Sub(f.sent[0]) >= time.Hour {
			f.sent = f.sent[1:]
		}
		recent := 0
		for _, t := range f.sent {
			if now.Sub(t) < time.Minute {
				recent++
			}
		}
		if recent < f.PerMin && len(f.sent) < f.PerHour {
			f.sent = append(f.sent, now)
			return true
		}
		var wait time.Duration
		if recent >= f.PerMin {
			wait = time.Minute - now.Sub(f.sent[len(f.sent)-f.PerMin])
		}
		if len(f.sent) >= f.PerHour {
			wait = max(wait, time.Hour-now.Sub(f.sent[0]))
		}
		if !warned {
			f.Logf("rate cap hit (%d/min, %d/hour); holding %s", f.PerMin, f.PerHour, wait.Round(time.Second))
			warned = true
		}
		if !f.sleep(ctx, max(wait, time.Second)) {
			return false
		}
	}
}

// deadLetter keeps the message locally before it is acknowledged; if the
// record cannot be written the message is left on the relay instead.
func (f *forwarder) deadLetter(fr relayFrame, reason string) error {
	f.Logf("dead-letter %s: %s", fr.SpoolID, reason)
	if f.DryRun {
		return nil
	}
	rec, _ := json.Marshal(struct {
		At      string          `json:"at"`
		ID      string          `json:"id"`
		Reason  string          `json:"reason"`
		Message json.RawMessage `json:"message,omitempty"`
	}{time.Now().UTC().Format(time.RFC3339), fr.SpoolID, reason, validJSONOrNil(fr.Message)})
	if err := appendDeadLetter(f.DeadLetter, rec); err != nil {
		return fmt.Errorf("cannot write dead letter %s: %w; leaving the message on the relay", f.DeadLetter, err)
	}
	return nil
}

func validJSONOrNil(b json.RawMessage) json.RawMessage {
	if !json.Valid(b) {
		return nil
	}
	return b
}

func appendDeadLetter(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return withDeadLetterLock(path, func() error {
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		// a torn write leaves no newline; the next record must not glue onto it
		rec := append(line[:len(line):len(line)], '\n')
		if fi, err := fh.Stat(); err == nil && fi.Size() > 0 {
			last := make([]byte, 1)
			if _, err := fh.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
				rec = append([]byte{'\n'}, rec...)
			}
		}
		if _, err = fh.Write(rec); err == nil {
			err = fh.Sync()
		}
		if cerr := fh.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

// ErrForwarderRunning means another forwarder holds the mailbox lock.
var ErrForwarderRunning = errors.New("another forwarder is running")

// LockForwardMailbox holds one forwarder per mailbox under this CBUS_DIR. Two would share
// a consumer id, displace each other on the relay and never settle a slow message.
func LockForwardMailbox(channel, host, alias string) (release func(), err error) {
	path := filepath.Join(CBUSDir(), "forward", channel+"@"+host, alias+".forwarder.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLockExclusive(fh); err != nil {
		_ = fh.Close()
		if errors.Is(err, errLockContended) {
			return nil, fmt.Errorf("%w for %s@%s/%s under %s", ErrForwarderRunning, channel, host, alias, CBUSDir())
		}
		return nil, err
	}
	return func() { _ = unlockFile(fh); _ = fh.Close() }, nil
}
