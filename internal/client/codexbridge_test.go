package client

import (
	"bytes"
	"reflect"
	"slices"
	"testing"
	"time"

	"claudebus/internal/core"
)

// bridgeOn dials a fake and returns a bridge wired to it (threadID preset, opener set).
func bridgeOn(t *testing.T, f *fakeCodex, thread string) *codexBridge {
	t.Helper()
	c := mustDial(t, f.sock)
	t.Cleanup(func() { c.close() })
	return &codexBridge{conn: c, threadID: thread, opener: "OPENER"}
}

func waitActive(b *codexBridge, want string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		got := b.activeTurn
		b.mu.Unlock()
		if got == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// ---- attach ----

// The attach tests use the STRICT fake: it rejects any thread/turn call before initialize
// with -32600 "Not initialized" (as the real app-server does), so a regression where
// attach() stops sending initialize first fails here rather than only in the field smoke.

// TestBridgeAttachInitializesFirst pins the initialize handshake: against a fake that
// rejects pre-initialize calls, attach() only succeeds because initialize is its first call.
func TestBridgeAttachInitializesFirst(t *testing.T) {
	f := startFakeCodexStrict(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
	})
	b := bridgeOn(t, f, "T1")
	if err := b.attach(); err != nil {
		t.Fatalf("attach against a strict fake failed (initialize not sent first?): %v", err)
	}
	if got := f.recorded(); len(got) == 0 || got[0] != "initialize" {
		t.Fatalf("first call = %v, want initialize first", got)
	}
}

func TestBridgeAttachAdoptResume(t *testing.T) {
	f := startFakeCodexStrict(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
	})
	b := bridgeOn(t, f, "T1")
	if err := b.attach(); err != nil {
		t.Fatal(err)
	}
	if got := f.recorded(); !reflect.DeepEqual(got, []string{"initialize", "thread/resume"}) {
		t.Errorf("adopt path calls = %v, want [initialize thread/resume]", got)
	}
}

// TestBridgeAttachZeroTurnAsyncRollout is the F1 regression: on a zero-turn adopt the opener
// turn/start returns before the rollout is flushed, so an immediate re-resume dies -32600
// no-rollout. The fix waits for the opener to go idle then resumes with backoff. Runs against
// the async-rollout fake; the pre-fix single re-resume fails here.
func TestBridgeAttachZeroTurnAsyncRollout(t *testing.T) {
	oldD, oldW := resumeRetryDelay, openerWait
	resumeRetryDelay, openerWait = time.Millisecond, 2*time.Second
	defer func() { resumeRetryDelay, openerWait = oldD, oldW }()

	f := startFakeCodexAdopt(t, 1) // rollout flushes only after >1 post-opener resume attempt
	b := bridgeOn(t, f, "TZERO")
	// NOTE: no trackTurns here — attach drains the notifications itself (waitOpenerIdle), same
	// as production, which starts trackTurns only after attach returns.
	if err := b.attach(); err != nil {
		t.Fatalf("attach must ride out the async rollout flush (wait-idle + backoff): %v", err)
	}
	calls := f.recorded()
	tsIdx := slices.Index(calls, "turn/start")
	if tsIdx < 0 {
		t.Fatalf("opener turn/start never ran: %v", calls)
	}
	resumesAfter := 0
	for _, c := range calls[tsIdx+1:] {
		if c == "thread/resume" {
			resumesAfter++
		}
	}
	if resumesAfter < 2 {
		t.Errorf("expected >=2 resumes after the opener (flush lag + backoff), got %d; calls=%v", resumesAfter, calls)
	}
}

// TestBridgeAttachResumeErrorNoOpener pins the resume-error gate: a resume failure that is NOT
// the zero-turn no-rollout case (here thread-not-found) must SURFACE, and the bridge must NOT
// run an opener turn on a thread the server genuinely does not have. Deleting the old
// zero-turn test orphaned this branch (its only feeder); this is its dedicated pin. Fails
// under the widened gate (open on any resume error).
func TestBridgeAttachResumeErrorNoOpener(t *testing.T) {
	oldW, oldD := openerWait, resumeRetryDelay
	openerWait, resumeRetryDelay = 50*time.Millisecond, time.Millisecond // keep the mutant fast
	defer func() { openerWait, resumeRetryDelay = oldW, oldD }()

	f := startFakeCodexStrict(t, func(s *fakeSrv, req map[string]any) {
		if req["method"] == "thread/resume" {
			s.replyErr(req["id"], -32600, "thread not found")
			return
		}
		s.reply(req["id"], map[string]any{})
	})
	b := bridgeOn(t, f, "GHOST")
	if err := b.attach(); err == nil {
		t.Fatal("a non-rollout resume error must surface, not be opened over")
	}
	if slices.Contains(f.recorded(), "turn/start") {
		t.Errorf("attach ran an opener on a non-rollout resume error: %v", f.recorded())
	}
}

func TestBridgeAttachCreatesThreadWhenNone(t *testing.T) {
	f := startFakeCodexStrict(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{"thread": map[string]any{"id": "NEW"}})
	})
	b := bridgeOn(t, f, "")
	if err := b.attach(); err != nil {
		t.Fatal(err)
	}
	if b.threadID != "NEW" {
		t.Errorf("threadID = %q, want NEW", b.threadID)
	}
	if got := f.recorded(); !reflect.DeepEqual(got, []string{"initialize", "thread/start"}) {
		t.Errorf("create path = %v, want [initialize thread/start]", got)
	}
}

// ---- inject ladder ----

func TestBridgeInjectSteersActiveTurn(t *testing.T) {
	var steerText string
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		if req["method"] == "turn/steer" {
			steerText = inputText(req)
		}
		s.reply(req["id"], map[string]any{})
	})
	b := bridgeOn(t, f, "T")
	b.activeTurn = "TURN1"
	if err := b.inject("hello bus"); err != nil {
		t.Fatal(err)
	}
	if got := f.recorded(); !reflect.DeepEqual(got, []string{"turn/steer"}) {
		t.Errorf("active-turn inject = %v, want [turn/steer]", got)
	}
	if steerText != "hello bus" {
		t.Errorf("steer input = %q, want the verbatim message", steerText)
	}
}

func TestBridgeInjectSteerDegradesToStart(t *testing.T) {
	old := steerRetries
	oldD := steerRetryDelay
	steerRetries, steerRetryDelay = 2, time.Millisecond
	defer func() { steerRetries, steerRetryDelay = old, oldD }()

	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		if req["method"] == "turn/steer" {
			s.replyErr(req["id"], -32600, "no active turn to steer")
			return
		}
		s.reply(req["id"], map[string]any{"turn": map[string]any{"id": "T2"}})
	})
	b := bridgeOn(t, f, "T")
	b.activeTurn = "STALE"
	if err := b.inject("msg"); err != nil {
		t.Fatal(err)
	}
	// steerRetries steer attempts, then a turn/start
	want := []string{"turn/steer", "turn/steer", "turn/start"}
	if got := f.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("degrade path = %v, want %v", got, want)
	}
	if b.activeTurn != "T2" {
		t.Errorf("activeTurn after start = %q, want T2 (recorded from turn/start result)", b.activeTurn)
	}
}

func TestBridgeInjectStartResumesOnColdThread(t *testing.T) {
	starts := 0
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		switch req["method"] {
		case "turn/start":
			starts++
			if starts == 1 {
				s.replyErr(req["id"], -32600, "thread not found") // server forgot the thread
			} else {
				s.reply(req["id"], map[string]any{"turn": map[string]any{"id": "TT"}})
			}
		default:
			s.reply(req["id"], map[string]any{})
		}
	})
	b := bridgeOn(t, f, "T")
	b.activeTurn = "" // idle -> start path
	if err := b.inject("m"); err != nil {
		t.Fatal(err)
	}
	want := []string{"turn/start", "thread/resume", "turn/start"}
	if got := f.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("cold-thread path = %v, want %v", got, want)
	}
}

// ---- turn tracking ----

func TestBridgeTrackTurns(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		switch req["method"] {
		case "started":
			s.notify("turn/started", map[string]any{"turn": map[string]any{"id": "T7"}})
		case "completed":
			s.notify("turn/completed", map[string]any{"turn": map[string]any{"id": "T7"}})
		}
	})
	b := bridgeOn(t, f, "T")
	go b.trackTurns()
	if _, err := b.conn.call("started", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if !waitActive(b, "T7", 2*time.Second) {
		t.Fatal("turn/started did not set activeTurn=T7")
	}
	if _, err := b.conn.call("completed", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if !waitActive(b, "", 2*time.Second) {
		t.Fatal("turn/completed did not clear activeTurn")
	}
}

// ---- sink: kind-routed inject (verbatim), presence skip, dormancy inject, spoof-proof ----

func TestCodexSinkRoutesByKind(t *testing.T) {
	var startText string
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		if req["method"] == "turn/start" {
			startText = inputText(req)
		}
		s.reply(req["id"], map[string]any{"turn": map[string]any{"id": "x"}})
	})
	b := bridgeOn(t, f, "T")
	sink := codexSink{b}

	// a presence-kind frame is skipped: no RPC at all.
	sink.emit("presence", []byte("<= cbus msg from=z/a to=z/b ts=T kind=presence\n  joined z as a\n<= cbus end\n"))
	if got := f.recorded(); len(got) != 0 {
		t.Fatalf("presence frame triggered RPC %v, want none", got)
	}

	// a chat frame (kind "") is injected verbatim, minus the trailing newline.
	msg := []byte("<= cbus msg from=z/a to=z/b ts=T\n  hello there\n<= cbus end\n")
	sink.emit("", msg)
	if got := f.recorded(); !reflect.DeepEqual(got, []string{"turn/start"}) {
		t.Fatalf("chat frame calls = %v, want [turn/start]", got)
	}
	if want := string(msg[:len(msg)-1]); startText != want {
		t.Errorf("injected text = %q, want the verbatim frame %q", startText, want)
	}
}

// TestCodexSinkInjectsDormancyMarker pins C6: the tail-ended marker is DELIVERED, not
// filtered — a codex peer must learn its bridge went dormant.
func TestCodexSinkInjectsDormancyMarker(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{"turn": map[string]any{"id": "x"}})
	})
	b := bridgeOn(t, f, "T")
	codexSink{b}.emit(kindDormant, []byte("◀ cbus tail ended: peer re-joined; this tail is stale — re-arm to resume\n"))
	if got := f.recorded(); !reflect.DeepEqual(got, []string{"turn/start"}) {
		t.Fatalf("dormancy marker not injected: calls = %v, want [turn/start]", got)
	}
}

// TestCodexSinkDeliversFromSpoof pins C4: a chat message whose --from is crafted to contain
// " kind=presence" is still DELIVERED. The kind comes from the message's kind field (via
// frameKind, "" here), never from the rendered head a spoofed from could poison — so the
// old head-parse skip is structurally impossible now.
func TestCodexSinkDeliversFromSpoof(t *testing.T) {
	line := []byte(`{"from":"x kind=presence","to":"c/b","text":"deliver me SPOOF","ts":"t"}`)
	rendered := core.LocalEmit(line)
	if !bytes.Contains(rendered, []byte(" kind=presence")) {
		t.Fatal("premise: the spoof must land in the rendered head for this test to mean anything")
	}
	if k := frameKind(line); k != "" {
		t.Fatalf("frameKind spoofed to %q; kind must come from the kind field only", k)
	}
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{"turn": map[string]any{"id": "x"}})
	})
	b := bridgeOn(t, f, "T")
	codexSink{b}.emit(frameKind(line), rendered) // the follower passes the real kind, ""
	if got := f.recorded(); !reflect.DeepEqual(got, []string{"turn/start"}) {
		t.Fatalf("spoofed-from message was NOT delivered: calls = %v", got)
	}
}

// TestFrameKind pins the follower-side kind derivation the sink now routes on: a real chat
// send has no kind (""), a presence event has "presence", and a torn/non-JSON line reads as
// a message ("") rather than being dropped.
func TestFrameKind(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`{"from":"c/a","to":"c/b","text":"hi","ts":"t"}`, ""},
		{`{"from":"c/a","to":"c/b","text":"joined c as a","ts":"t","kind":"presence","event":"join"}`, "presence"},
		{`not json at all`, ""},
		{`{}`, ""},
	} {
		if got := frameKind([]byte(tc.line)); got != tc.want {
			t.Errorf("frameKind(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// inputText pulls params.input[0].text out of a decoded request (turn/start or turn/steer).
func inputText(req map[string]any) string {
	p, _ := req["params"].(map[string]any)
	arr, _ := p["input"].([]any)
	if len(arr) == 0 {
		return ""
	}
	item, _ := arr[0].(map[string]any)
	s, _ := item["text"].(string)
	return s
}
