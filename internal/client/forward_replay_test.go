package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/core"
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
