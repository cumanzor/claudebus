package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claudebus/internal/core"
	"claudebus/internal/wire"
)

func deadLetterLine(id, text string) string {
	msg, _ := json.Marshal(core.Message{From: "demo/a", To: "demo/hook", TS: "t", Text: text})
	b, _ := json.Marshal(map[string]any{"at": "t", "id": id, "reason": "HTTP 500", "message": json.RawMessage(msg)})
	return string(b)
}

func replayFixture(t *testing.T, status func(string, int) int, lines ...string) (*hookRecorder, ForwardOptions, string) {
	t.Helper()
	hook := &hookRecorder{status: status}
	srv := httptest.NewServer(hook)
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "dl.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return hook, ForwardOptions{Webhook: WebhookConfig{URL: srv.URL, Key: "k"}, PerMin: 100, PerHour: 100, sleep: noSleep, client: newForwardClient(0)}, path
}

func TestReplayKeepsOnlyWhatFailed(t *testing.T) {
	hook, o, path := replayFixture(t, func(text string, _ int) int {
		if text == "bad" {
			return 500
		}
		return 200
	}, deadLetterLine("1", "one"), deadLetterLine("2", "bad"), "not json", `{"id":"4","reason":"too large"}`, deadLetterLine("5", "five"))
	res, err := ReplayDeadLetters(context.Background(), o, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered != 2 || res.Failed != 3 {
		t.Errorf("result = %+v, want 2 delivered, 3 kept", res)
	}
	if got := strings.Join(hook.texts(), ","); got != "one,bad,five" {
		t.Errorf("posts = %s", got)
	}
	if id, _ := hook.bodies[0]["id"].(string); id != "1" {
		t.Errorf("replay changed the id: %v", hook.bodies[0])
	}
	b, _ := os.ReadFile(path)
	want := deadLetterLine("2", "bad") + "\nnot json\n" + `{"id":"4","reason":"too large"}` + "\n"
	if string(b) != want {
		t.Errorf("file = %q\nwant %q", b, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestReplayDryRunAndIDs(t *testing.T) {
	hook, o, path := replayFixture(t, nil, deadLetterLine("1", "one"), deadLetterLine("2", "two"))
	before, _ := os.ReadFile(path)
	dry := o
	dry.DryRun = true
	if res, err := ReplayDeadLetters(context.Background(), dry, path, nil); err != nil || res.Delivered != 0 {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) || len(hook.texts()) != 0 {
		t.Fatal("dry run posted or changed the file")
	}
	if _, err := ReplayDeadLetters(context.Background(), o, path, []string{"2", "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown id: %v", err)
	}
	if got := strings.Join(hook.texts(), ","); got != "two" {
		t.Errorf("posts = %s", got)
	}
	if b, _ := os.ReadFile(path); string(b) != deadLetterLine("1", "one")+"\n" {
		t.Errorf("file = %q", b)
	}
}

func TestReplayMissingFile(t *testing.T) {
	_, o, _ := replayFixture(t, nil, deadLetterLine("1", "one"))
	missing := filepath.Join(t.TempDir(), "none.jsonl")
	if res, err := ReplayDeadLetters(context.Background(), o, missing, nil); err != nil || res != (ReplayResult{}) {
		t.Errorf("%+v %v", res, err)
	}
	if _, err := ReplayDeadLetters(context.Background(), o, missing, []string{"1"}); err == nil {
		t.Error("a named id in a missing file reported success")
	}
}

// a running forwarder may append while a slow POST is in flight; that record must survive the rewrite
func TestReplayKeepsARecordAppendedDuringThePost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dl.jsonl")
	if err := os.WriteFile(path, []byte(deadLetterLine("1", "one")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := appendDeadLetter(path, []byte(deadLetterLine("9", "late"))); err != nil {
			t.Error(err)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	o := ForwardOptions{Webhook: WebhookConfig{URL: srv.URL, Key: "k"}, PerMin: 100, PerHour: 100, sleep: noSleep, client: newForwardClient(0)}
	if res, err := ReplayDeadLetters(context.Background(), o, path, nil); err != nil || res.Delivered != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(path); string(b) != deadLetterLine("9", "late")+"\n" {
		t.Errorf("file = %q", b)
	}
}

func TestSkipQueuedThroughRealRelay(t *testing.T) {
	const token = "forwardskiptoken"
	base := startTestRelay(t, token)
	hook := &hookRecorder{}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	relaySend(t, base, token, "old one")
	relaySend(t, base, token, "old two")
	ep := RemoteEndpoint{FrontDoor: FrontDoor{Local: true, Base: base}, headers: map[string]string{"Authorization": "Bearer " + token}}
	queued, err := RelayQueued(ep, "demo", "hook")
	if err != nil || queued != 2 {
		t.Fatalf("queued = %d, %v", queued, err)
	}
	if n, err := RelayQueued(ep, "demo", "nobody"); err != nil || n != 0 {
		t.Errorf("unknown mailbox = %d, %v", n, err)
	}
	dl := filepath.Join(t.TempDir(), "dl.jsonl")
	opts := ForwardOptions{
		Channel: "demo", Host: "server", Alias: "hook", Base: base, Token: token,
		Webhook: WebhookConfig{URL: srv.URL + "/hook", Key: "k"},
		PerMin:  100, PerHour: 100, MaxAttempts: 3, DeadLetter: dl, SkipQueued: queued, sleep: noSleep,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Forward(ctx, opts) }()
	schedulerWait(t, "the backlog is skipped", func() bool {
		b, _ := os.ReadFile(dl)
		return strings.Count(string(b), "\n") == 2
	})
	relaySend(t, base, token, "new")
	schedulerWait(t, "the new message arrives", func() bool { return len(hook.texts()) > 0 })
	cancel()
	<-done
	if got := strings.Join(hook.texts(), ","); got != "new" {
		t.Fatalf("webhook saw %s, want only the new message", got)
	}
	b, _ := os.ReadFile(dl)
	if strings.Count(string(b), `"reason":"`+forwardSkippedReason+`"`) != 2 || !strings.Contains(string(b), "old one") {
		t.Errorf("dead letters: %s", b)
	}
	if n, _ := RelayQueued(ep, "demo", "hook"); n != 0 {
		t.Errorf("queued after = %d", n)
	}

	replay := ForwardOptions{Webhook: opts.Webhook, PerMin: 100, PerHour: 100, sleep: noSleep}
	if res, err := ReplayDeadLetters(context.Background(), replay, dl, nil); err != nil || res.Delivered != 0 || res.LeftSkipped != 2 || res.Failed != 0 {
		t.Fatalf("a bare replay must leave a skipped backlog alone: %+v %v", res, err)
	}
	if got := strings.Join(hook.texts(), ","); got != "new" {
		t.Fatalf("a bare replay sent the backlog: %s", got)
	}
	replay.IncludeSkipped = true
	if res, err := ReplayDeadLetters(context.Background(), replay, dl, nil); err != nil || res.Delivered != 2 {
		t.Fatalf("replay: %+v %v", res, err)
	}
	if got := strings.Join(hook.texts(), ","); got != "new,old one,old two" {
		t.Errorf("after replay webhook saw %s", got)
	}
}

// a skip that cannot record the message must leave it on the relay
func TestSkipQueuedKeepsTheMessageWhenTheRecordFails(t *testing.T) {
	const token = "forwardskipfailtoken"
	base := startTestRelay(t, token)
	relaySend(t, base, token, "precious")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := ForwardOptions{
		Channel: "demo", Host: "server", Alias: "hook", Base: base, Token: token,
		Webhook: WebhookConfig{URL: "http://127.0.0.1:9/hook", Key: "k"},
		PerMin:  100, PerHour: 100, MaxAttempts: 1, DeadLetter: filepath.Join(blocker, "dl.jsonl"),
		SkipQueued: 1, Once: true, OnceIdle: time.Second, sleep: noSleep,
	}
	if err := Forward(context.Background(), opts); err == nil {
		t.Fatal("a failed record reported success")
	}
	ep := RemoteEndpoint{FrontDoor: FrontDoor{Local: true, Base: base}, headers: map[string]string{"Authorization": "Bearer " + token}}
	if n, _ := RelayQueued(ep, "demo", "hook"); n != 1 {
		t.Errorf("queued = %d, want the message still on the relay", n)
	}
}

func TestReplayNamedSkippedRecord(t *testing.T) {
	skipped := strings.Replace(deadLetterLine("1", "old"), "HTTP 500", forwardSkippedReason, 1)
	hook, o, path := replayFixture(t, nil, skipped, deadLetterLine("2", "real"))
	res, err := ReplayDeadLetters(context.Background(), o, path, nil)
	if err != nil || res.Delivered != 1 || res.LeftSkipped != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if res, err = ReplayDeadLetters(context.Background(), o, path, []string{"1"}); err != nil || res.Delivered != 1 {
		t.Fatalf("a named skipped record: %+v %v", res, err)
	}
	if got := strings.Join(hook.texts(), ","); got != "real,old" {
		t.Errorf("posts = %s", got)
	}
}

// the connection dies after an ACK was written and before the relay commits it
func TestSkipQueuedSurvivesALostACK(t *testing.T) {
	ids := []string{"A", "B", "C"}
	var mu sync.Mutex
	committed, lose := 0, true
	mux := http.NewServeMux()
	mux.HandleFunc("/tail/durable-v1", func(w http.ResponseWriter, r *http.Request) {
		c, err := wire.Upgrade(w, r, "bearer.cbus.tok")
		if err != nil {
			return
		}
		defer c.Abort()
		_ = c.WriteFrame(wire.OpText, []byte(`{"type":"ready","protocol":"cbus-relay-durable/v1"}`))
		for {
			mu.Lock()
			i := committed
			mu.Unlock()
			if i >= len(ids) {
				<-r.Context().Done()
				return
			}
			msg := fmt.Sprintf(`{"from":"demo/a","to":"demo/hook","ts":"t","text":%q}`, ids[i])
			fr, _ := json.Marshal(relayFrame{Type: "message", SpoolID: ids[i], Message: json.RawMessage(msg)})
			_ = c.WriteFrame(wire.OpText, fr)
			if _, _, err := c.ReadFrame(); err != nil {
				return
			}
			mu.Lock()
			dropped := lose
			lose = false
			if !dropped {
				committed++
			}
			mu.Unlock()
			if dropped {
				return
			}
		}
	})
	rel := httptest.NewServer(mux)
	defer rel.Close()
	hook := &hookRecorder{}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	dl := filepath.Join(t.TempDir(), "dl.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Forward(ctx, ForwardOptions{Channel: "demo", Host: "s", Alias: "hook", Base: rel.URL, Token: "tok",
			Webhook: WebhookConfig{URL: srv.URL, Key: "k"}, PerMin: 100, PerHour: 100, MaxAttempts: 3, DeadLetter: dl,
			SkipQueued: 3, sleep: noSleep})
	}()
	schedulerWait(t, "all three settle", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return committed == 3
	})
	cancel()
	<-done
	if got := hook.texts(); len(got) != 0 {
		t.Errorf("a backlog message was POSTed: %v", got)
	}
	b, _ := os.ReadFile(dl)
	if n := strings.Count(string(b), "\n"); n != 3 {
		t.Errorf("records = %d, want one per message:\n%s", n, b)
	}
}

func TestOversizedIsDeliveredAndKeptWhole(t *testing.T) {
	const token = "forwardbigtoken"
	base := startTestRelay(t, token)
	hook := &hookRecorder{status: func(string, int) int { return 500 }}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	big := strings.Repeat("x", 300*1024)
	relaySend(t, base, token, big)
	dl := filepath.Join(t.TempDir(), "dl.jsonl")
	// the trailing slash is how a site URL is often written
	opts := ForwardOptions{Channel: "demo", Host: "server", Alias: "hook", Base: base + "/", Token: token,
		Webhook: WebhookConfig{URL: srv.URL, Key: "k"}, PerMin: 100, PerHour: 100, MaxAttempts: 1, DeadLetter: dl,
		Once: true, OnceIdle: time.Second, sleep: noSleep}
	if err := Forward(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if len(hook.bodies) != 1 || len(hook.texts()[0]) != forwardMaxText || hook.bodies[0]["truncated"] != float64(len(big)) {
		t.Fatalf("POST: %d bodies", len(hook.bodies))
	}
	b, _ := os.ReadFile(dl)
	if !strings.Contains(string(b), big) {
		t.Fatal("the dead letter does not hold the whole message")
	}
	hook.mu.Lock()
	hook.status = nil
	hook.mu.Unlock()
	res, err := ReplayDeadLetters(context.Background(), ForwardOptions{Webhook: opts.Webhook, PerMin: 100, PerHour: 100, sleep: noSleep}, dl, nil)
	if err != nil || res.Delivered != 1 {
		t.Fatalf("replay: %+v %v", res, err)
	}
}

func TestDeadLetterRecordDoesNotGlueOntoATornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dl.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"torn","rea`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendDeadLetter(path, []byte(deadLetterLine("new", "text"))); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := `{"id":"torn","rea` + "\n" + deadLetterLine("new", "text") + "\n"
	if string(b) != want {
		t.Errorf("file = %q", b)
	}
	hook, o, _ := replayFixture(t, nil, "x")
	if res, err := ReplayDeadLetters(context.Background(), o, path, []string{"new"}); err != nil || res.Delivered != 1 {
		t.Fatalf("replay: %+v %v", res, err)
	}
	if got := hook.texts(); len(got) != 1 {
		t.Errorf("posts = %v", got)
	}
}

func TestLockForwardMailboxAllowsOneForwarder(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	release, err := LockForwardMailbox("demo", "server", "hook")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockForwardMailbox("demo", "server", "hook"); !errors.Is(err, ErrForwarderRunning) {
		t.Fatalf("second forwarder: %v", err)
	}
	other, err := LockForwardMailbox("demo", "server", "other")
	if err != nil {
		t.Fatalf("another mailbox was refused: %v", err)
	}
	other()
	release()
	again, err := LockForwardMailbox("demo", "server", "hook")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}

// a forwarder that started before the local relay moves to it once it is up
func TestForwardResolvesTheRelayBeforeEachDial(t *testing.T) {
	const token = "forwardresolvetoken"
	base := startTestRelay(t, token)
	hook := &hookRecorder{}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	relaySend(t, base, token, "waiting")
	var mu sync.Mutex
	calls := 0
	opts := ForwardOptions{Channel: "demo", Host: "server", Alias: "hook", Base: "http://127.0.0.1:1", Token: token,
		Webhook: WebhookConfig{URL: srv.URL, Key: "k"}, PerMin: 100, PerHour: 100, MaxAttempts: 1,
		DeadLetter: filepath.Join(t.TempDir(), "dl.jsonl"), sleep: noSleep,
		Resolve: func() (string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls < 3 {
				return "http://127.0.0.1:1", nil
			}
			return base + "/", nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Forward(ctx, opts) }()
	schedulerWait(t, "the message arrives through the relay that came up", func() bool { return len(hook.texts()) == 1 })
	cancel()
	<-done
}
