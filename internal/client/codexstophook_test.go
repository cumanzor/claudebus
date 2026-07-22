package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appendInboxLine(t *testing.T, inbox, jsonLine string) {
	t.Helper()
	f, err := os.OpenFile(inbox, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(jsonLine + "\n"); err != nil {
		t.Fatal(err)
	}
}

func joinForStop(t *testing.T, ch, al, sid string) string {
	t.Helper()
	if _, _, err := Join(ch, al); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(CBUSDir(), ch, al, "inbox.jsonl")
}

// TestStopHookMidPollAppend: a message appended while the hook is long-polling yields a block
// decision carrying the framed message, and the cursor advances.
func TestStopHookMidPollAppend(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	inbox := joinForStop(t, "chA", "me", "S1")

	done := make(chan string, 1)
	go func() { done <- StopHook(strings.NewReader(`{"session_id":"S1"}`), 3*time.Second) }()
	time.Sleep(50 * time.Millisecond) // let it enter the poll loop
	appendInboxLine(t, inbox, `{"from":"chA/driver","to":"chA/me","ts":"t1","text":"hello worker"}`)

	select {
	case block := <-done:
		if !strings.Contains(block, `"decision":"block"`) {
			t.Errorf("expected a block decision, got %q", block)
		}
		if !strings.Contains(block, "hello worker") {
			t.Errorf("block reason missing the framed message: %q", block)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("StopHook did not return a block after a mid-poll append")
	}
	if _, _, _, ok := readStopCursor(filepath.Dir(inbox)); !ok {
		t.Error("cursor did not advance after delivery")
	}
}

// TestStopHookActiveEmptyImmediate: a re-entry (stop_hook_active) with nothing new allows the
// stop at once — it must not hold the turn re-blocking on ceremony.
func TestStopHookActiveEmptyImmediate(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	joinForStop(t, "chA", "me", "S1")

	start := time.Now()
	block := StopHook(strings.NewReader(`{"session_id":"S1","stop_hook_active":true}`), 10*time.Second)
	if block != "" {
		t.Errorf("stop_hook_active + empty inbox must allow the stop, got %q", block)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("must return immediately, took %s", d)
	}
}

// TestStopHookTimeoutNoBlock: no traffic within the wait yields no block (no stdout) — the
// timeout allows the stop, it is never a signal.
func TestStopHookTimeoutNoBlock(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	joinForStop(t, "chA", "me", "S1")

	if block := StopHook(strings.NewReader(`{"session_id":"S1"}`), 300*time.Millisecond); block != "" {
		t.Errorf("no traffic must yield no block, got %q", block)
	}
}

// TestStopHookMalformedStdinNoop: a non-JSON payload with no ambient session id is a no-op.
func TestStopHookMalformedStdinNoop(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t) // no id anywhere
	if block := StopHook(strings.NewReader(`not json at all`), time.Second); block != "" {
		t.Errorf("malformed stdin with no session id must no-op, got %q", block)
	}
}

// TestStopHookCursorAdvances: after delivering, a second Stop with nothing new does not
// re-deliver (the cursor moved past the delivered messages).
func TestStopHookCursorAdvances(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	inbox := joinForStop(t, "chA", "me", "S1")
	appendInboxLine(t, inbox, `{"from":"chA/d","to":"chA/me","ts":"t1","text":"msg-one"}`)
	appendInboxLine(t, inbox, `{"from":"chA/d","to":"chA/me","ts":"t2","text":"msg-two"}`)

	b1 := StopHook(strings.NewReader(`{"session_id":"S1"}`), time.Second)
	if !strings.Contains(b1, "msg-one") || !strings.Contains(b1, "msg-two") {
		t.Fatalf("first Stop must deliver both: %q", b1)
	}
	b2 := StopHook(strings.NewReader(`{"session_id":"S1","stop_hook_active":true}`), time.Second)
	if b2 != "" {
		t.Errorf("cursor did not advance; second Stop re-delivered: %q", b2)
	}
}

// TestStopHookSkipsPresence: presence/status lines are not delivered (bridge parity), chat is.
func TestStopHookSkipsPresence(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	inbox := joinForStop(t, "chA", "me", "S1")
	appendInboxLine(t, inbox, `{"from":"chA/x","to":"chA/me","ts":"t1","text":"joined chA as x","kind":"presence","event":"join"}`)
	appendInboxLine(t, inbox, `{"from":"chA/x","to":"chA/me","ts":"t2","text":"a real message"}`)

	b := StopHook(strings.NewReader(`{"session_id":"S1"}`), time.Second)
	if strings.Contains(b, "joined chA as x") {
		t.Error("presence must be skipped, not delivered as a continuation turn")
	}
	if !strings.Contains(b, "a real message") {
		t.Errorf("chat must be delivered: %q", b)
	}
}

// TestStopHookMultiInboxByTs: a worker joined to two channels hears both, ordered across
// inboxes by ts (per-inbox order preserved).
func TestStopHookMultiInboxByTs(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearAllSessionEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	inboxA := joinForStop(t, "chA", "me", "S1")
	inboxB := joinForStop(t, "chB", "me", "S1")
	appendInboxLine(t, inboxA, `{"from":"chA/d","to":"chA/me","ts":"2026-01-02","text":"BETA"}`)
	appendInboxLine(t, inboxB, `{"from":"chB/d","to":"chB/me","ts":"2026-01-01","text":"ALPHA"}`)

	b := StopHook(strings.NewReader(`{"session_id":"S1"}`), time.Second)
	ai, bi := strings.Index(b, "ALPHA"), strings.Index(b, "BETA")
	if ai < 0 || bi < 0 {
		t.Fatalf("both messages must be delivered: %q", b)
	}
	if ai > bi {
		t.Errorf("cross-inbox order must be by ts (ALPHA t1 before BETA t2): %q", b)
	}
}
