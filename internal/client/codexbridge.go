package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// codexbridge attaches a codex app-server thread to a cbus inbox. The bridge arms as the
// alias's local listener (structural liveness covers its pid) and tails the inbox with the
// shared follower loop; each framed bus message becomes one codex injection. Delivery
// follows the ruling ladder: steer an in-flight turn when one is active, else open a new
// turn, resuming the thread if the server forgot it. One frame = one injection; presence
// frames are skipped (a codex injection forces a full model turn, too costly for join/leave
// ceremony), but the cursor still advances over them.

// steerRetries/steerRetryDelay bound the init-gap retry (a just-started turn is briefly
// inProgress but not yet steerable). Vars, not consts, so tests can shrink the delay.
var (
	steerRetries    = 4
	steerRetryDelay = 250 * time.Millisecond
)

// defaultOpener seeds a zero-turn adopted thread so it gains a rollout (thread/resume errors
// without one). A3's wrapper passes the real bus bootstrap; this is the A2 standalone default.
const defaultOpener = "Connected to a cbus channel as a codex peer. Bus messages will arrive as turns; reply on the bus."

type codexBridge struct {
	conn     *codexConn
	threadID string
	opener   string

	mu         sync.Mutex
	activeTurn string // the currently-active turnId, "" when the thread is idle
}

// RunCodexBridge is the `cbus codex-bridge CH/AL --sock PATH [--thread ID]` entry point. It
// dials the app-server, attaches (adopt-or-open), tracks turn state, and blocks in the
// follower loop until the listener goes dormant. thread "" makes the bridge create and own
// a fresh thread.
func RunCodexBridge(target, sock, thread string) error {
	conn, err := dialCodex(sock)
	if err != nil {
		return fmt.Errorf("dial codex app-server %q: %w", sock, err)
	}
	defer conn.close()
	b := &codexBridge{conn: conn, threadID: thread, opener: defaultOpener}
	if err := b.attach(); err != nil {
		return fmt.Errorf("attach codex thread: %w", err)
	}
	go b.trackTurns()
	return armLocalTailTo(target, false, codexSink{b})
}

// attach subscribes the bridge to its thread. An empty threadID makes the bridge create one
// (thread/start subscribes the creating connection, no rollout needed). Adopting an existing
// thread goes through thread/resume, which both loads the rollout (surviving a server
// restart/unload) and subscribes to the turn/notification stream. A zero-turn thread has no
// rollout, so resume errors; there the bridge opens it with the bootstrap turn first, then
// resumes.
func (b *codexBridge) attach() error {
	// the app-server rejects every thread/turn call with -32600 "Not initialized" until
	// the connection has been initialized.
	if _, err := b.conn.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "cbus-codex-bridge", "version": "1"},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if b.threadID == "" {
		r, err := b.conn.call("thread/start", map[string]any{"approvalPolicy": "never", "sandbox": "read-only"})
		if err != nil {
			return err
		}
		if b.threadID = threadIDFromResult(r); b.threadID == "" {
			return errors.New("thread/start returned no thread id")
		}
		return nil
	}
	if _, err := b.conn.call("thread/resume", map[string]any{"threadId": b.threadID}); err != nil {
		// zero-turn thread (no rollout): open it, then resume to subscribe.
		if _, oerr := b.conn.call("turn/start", turnParams(b.threadID, b.opener)); oerr != nil {
			return fmt.Errorf("resume failed (%v) and opener turn failed: %w", err, oerr)
		}
		if _, rerr := b.conn.call("thread/resume", map[string]any{"threadId": b.threadID}); rerr != nil {
			return rerr
		}
	}
	return nil
}

// trackTurns keeps activeTurn current from the subscribed stream: a turn/started makes the
// thread busy (and names the steerable turn), a turn/completed for that turn makes it idle.
func (b *codexBridge) trackTurns() {
	for note := range b.conn.notifications() {
		switch note.Method {
		case "turn/started":
			if id := turnIDFromNote(note.Params); id != "" {
				b.mu.Lock()
				b.activeTurn = id
				b.mu.Unlock()
			}
		case "turn/completed":
			id := turnIDFromNote(note.Params)
			b.mu.Lock()
			if id == "" || b.activeTurn == id {
				b.activeTurn = ""
			}
			b.mu.Unlock()
		}
	}
}

// inject delivers one bus message. It steers the active turn when there is one, falling back
// to opening a fresh turn. The ladder is: steer -> (no active turn, after a bounded init-gap
// retry) turn/start -> (thread not found) resume + retry once.
func (b *codexBridge) inject(text string) error {
	b.mu.Lock()
	turn := b.activeTurn
	b.mu.Unlock()
	if turn != "" {
		if err := b.steerWithRetry(turn, text); err == nil {
			return nil
		} else if !isRPCCode(err, -32600, "no active turn") {
			return err // a real steer failure, not the init-gap/turn-ended case
		}
		// steer degraded to "no active turn": the turn ended or never became steerable,
		// so open a new one (this is how queue-until-idle emerges naturally).
	}
	return b.startTurn(text)
}

// steerWithRetry injects into the active turn, absorbing the brief post-turn/start init gap
// where a genuinely-inProgress turn is not yet steerable (-32600 "no active turn to steer").
// It never busy-waits unbounded: after steerRetries it returns the last error to degrade.
func (b *codexBridge) steerWithRetry(turn, text string) error {
	var err error
	for i := 0; i < steerRetries; i++ {
		if _, err = b.conn.call("turn/steer", steerParams(b.threadID, turn, text)); err == nil {
			return nil
		}
		if !isRPCCode(err, -32600, "no active turn") {
			return err
		}
		time.Sleep(steerRetryDelay)
	}
	return err
}

// startTurn opens a fresh turn, recovering from a cold/unloaded thread (server restart) with
// one resume + retry. It records the new turnId so the next message steers it without waiting
// for the turn/started notification.
func (b *codexBridge) startTurn(text string) error {
	r, err := b.conn.call("turn/start", turnParams(b.threadID, text))
	if err != nil && isRPCCode(err, -32600, "thread not found") {
		if _, rerr := b.conn.call("thread/resume", map[string]any{"threadId": b.threadID}); rerr != nil {
			return rerr
		}
		r, err = b.conn.call("turn/start", turnParams(b.threadID, text))
	}
	if err != nil {
		return err
	}
	if id := turnIDFromResult(r); id != "" {
		b.mu.Lock()
		b.activeTurn = id
		b.mu.Unlock()
	}
	return nil
}

// codexSink is the follower's frameSink: one emit per LocalEmit-rendered frame, tagged with
// the kind the follower read off the raw line. It injects a chat message (kind "") and the
// dormancy marker (kindDormant, C6 — a peer must learn its delivery stopped) verbatim, so a
// codex peer reads its bus mail in the same format a Claude peer sees; it skips presence
// events (kind "presence"), since a codex injection is a full model turn. The kind comes from
// the follower, NOT from parsing the rendered head — a hostile --from cannot spoof it. An
// injection failure is logged, never fatal; the follower keeps tailing.
type codexSink struct{ b *codexBridge }

func (s codexSink) emit(kind string, rendered []byte) {
	if kind != "" && kind != kindDormant {
		return // presence/status event: not a model turn
	}
	if err := s.b.inject(string(bytes.TrimRight(rendered, "\n"))); err != nil {
		fmt.Fprintf(os.Stderr, "cbus: codex-bridge injection failed: %v\n", err)
	}
}

// ---- small helpers ----

func textInput(s string) []map[string]any {
	return []map[string]any{{"type": "text", "text": s}}
}

func turnParams(threadID, text string) map[string]any {
	return map[string]any{"threadId": threadID, "input": textInput(text)}
}

func steerParams(threadID, turnID, text string) map[string]any {
	return map[string]any{"threadId": threadID, "expectedTurnId": turnID, "input": textInput(text)}
}

func isRPCCode(err error, code int, substr string) bool {
	var re *rpcError
	return errors.As(err, &re) && re.Code == code && strings.Contains(re.Message, substr)
}

func threadIDFromResult(r json.RawMessage) string {
	var p struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(r, &p)
	return p.Thread.ID
}

func turnIDFromResult(r json.RawMessage) string {
	var p struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(r, &p)
	return p.Turn.ID
}

func turnIDFromNote(params json.RawMessage) string { return turnIDFromResult(params) }

var _ frameSink = codexSink{}
