package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claudebus/internal/core"
)

func writeWebhookConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "webhook")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadWebhookConfig(t *testing.T) {
	good := writeWebhookConfig(t, "# hook\nurl=https://hooks.example.com/x\nkey=k1\n", 0o600)
	c, err := LoadWebhookConfig(good)
	if err != nil || c.URL != "https://hooks.example.com/x" || c.Key != "k1" {
		t.Fatalf("good config: %+v %v", c, err)
	}
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
	}{
		"group readable": {"url=https://hooks.example.com/x\nkey=k\n", 0o640},
		"plain http":     {"url=http://hooks.example.com/x\nkey=k\n", 0o600},
		"missing key":    {"url=https://hooks.example.com/x\n", 0o600},
		"unknown field":  {"url=https://hooks.example.com/x\nkey=k\nsecret=s\n", 0o600},
	} {
		if _, err := LoadWebhookConfig(writeWebhookConfig(t, tc.body, tc.mode)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := LoadWebhookConfig(writeWebhookConfig(t, "url=http://127.0.0.1:9/x\nkey=k\n", 0o600)); err != nil {
		t.Errorf("loopback http refused: %v", err)
	}
}

type hookRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
	auth   []string
	gets   int
	status func(text string, n int) int
}

func (h *hookRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Method != http.MethodPost {
		h.gets++
		w.WriteHeader(200)
		return
	}
	var b map[string]any
	_ = json.NewDecoder(r.Body).Decode(&b)
	h.bodies = append(h.bodies, b)
	h.auth = append(h.auth, r.Header.Get("Authorization"))
	text, _ := b["text"].(string)
	n := 0
	for _, prev := range h.bodies {
		if prev["text"] == text {
			n++
		}
	}
	code := 200
	if h.status != nil {
		code = h.status(text, n)
	}
	if code == 302 {
		w.Header().Set("Location", "/elsewhere")
	}
	w.WriteHeader(code)
}

func (h *hookRecorder) texts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, b := range h.bodies {
		s, _ := b["text"].(string)
		out = append(out, s)
	}
	return out
}

func noSleep(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }

func TestForwardHandleRedirectIsNeverFollowed(t *testing.T) {
	hook := &hookRecorder{status: func(string, int) int { return 302 }}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	var logs bytes.Buffer
	f := &forwarder{ForwardOptions: ForwardOptions{
		Webhook: WebhookConfig{URL: srv.URL + "/hook", Key: "secret-key"}, PerMin: 100, PerHour: 100, MaxAttempts: 3,
		DeadLetter: filepath.Join(t.TempDir(), "dl.jsonl"), sleep: noSleep,
		client: newForwardClient(0),
		Logf:   func(f string, a ...any) { logs.WriteString(strings.TrimSpace(fmt.Sprintf(f, a...)) + "\n") },
	}}
	msg, _ := json.Marshal(core.Message{From: "demo/a", To: "demo/hook", TS: "t", Text: "hi"})
	if err := f.handle(context.Background(), relayFrame{Type: "message", SpoolID: "1.000001.aa.json", Message: msg}); err != nil {
		t.Fatal(err)
	}
	if len(hook.texts()) != 3 || hook.gets != 0 {
		t.Fatalf("posts=%d gets=%d, want 3 POSTs and no redirected GET", len(hook.texts()), hook.gets)
	}
	dl, _ := os.ReadFile(f.DeadLetter)
	if !strings.Contains(string(dl), `"reason":"HTTP 302 after 3 attempts"`) || !strings.Contains(string(dl), `"text":"hi"`) {
		t.Fatalf("dead letter: %s", dl)
	}
	if strings.Contains(logs.String(), "secret-key") || strings.Contains(logs.String(), srv.URL) {
		t.Fatalf("logs leak the key or URL:\n%s", logs.String())
	}
}

func TestForwardThrottleHoldsInsteadOfDropping(t *testing.T) {
	var waits []time.Duration
	f := &forwarder{ForwardOptions: ForwardOptions{PerMin: 2, PerHour: 3, Logf: func(string, ...any) {},
		sleep: func(_ context.Context, d time.Duration) bool { waits = append(waits, d); return false }}}
	now := time.Now()
	f.sent = []time.Time{now.Add(-30 * time.Second), now.Add(-10 * time.Second)}
	if f.throttle(context.Background()) {
		t.Fatal("third send within a minute was allowed")
	}
	if len(waits) != 1 || waits[0] < 25*time.Second || waits[0] > 31*time.Second {
		t.Fatalf("per-minute hold %v, want about 30s", waits)
	}
	waits = nil
	f.sent = []time.Time{now.Add(-50 * time.Minute), now.Add(-40 * time.Minute), now.Add(-30 * time.Minute)}
	if f.throttle(context.Background()) || len(waits) != 1 || waits[0] < 9*time.Minute || waits[0] > 11*time.Minute {
		t.Fatalf("per-hour hold %v, want about 10m", waits)
	}
}

func TestForwardPayloadTruncatesBytes(t *testing.T) {
	text := strings.Repeat("é", forwardMaxText) // 2 bytes each
	raw, _ := json.Marshal(map[string]any{"from": "demo/a", "to": "demo/hook", "ts": "t", "text": text, "relayId": "r1"})
	var m core.Message
	_ = json.Unmarshal(raw, &m)
	var out map[string]any
	_ = json.Unmarshal(forwardPayload("id1", m, raw), &out)
	got, _ := out["text"].(string)
	if len(got) > forwardMaxText || out["truncated"] != float64(2*forwardMaxText) || out["relayId"] != "r1" || out["id"] != "id1" {
		t.Fatalf("len=%d truncated=%v relayId=%v id=%v", len(got), out["truncated"], out["relayId"], out["id"])
	}
}

// startTestRelay builds and runs the real relay on loopback.
func startTestRelay(t *testing.T, token string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs isolated relay process")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain unavailable")
	}
	bin := filepath.Join(t.TempDir(), "cbus-relay")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/relay/cmd/cbus-relay").CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v %s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-listen", addr, "-spool", t.TempDir(), "-token-file", filepath.Join(t.TempDir(), "unused"))
	cmd.Env = append(os.Environ(), "CBUS_RELAY_TOKEN="+token)
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	base := "http://" + addr
	probe := &http.Client{Timeout: 200 * time.Millisecond}
	schedulerWait(t, "relay ready", func() bool {
		r, e := probe.Get(base + "/healthz")
		if e != nil {
			return false
		}
		_ = r.Body.Close()
		return r.StatusCode == 200
	})
	return base
}

func relaySend(t *testing.T, base, token, text string) {
	t.Helper()
	b, _ := json.Marshal(core.SendReq{Channel: "demo", Alias: "hook", From: "demo/laptop", Text: text})
	req, _ := http.NewRequest(http.MethodPost, base+"/send", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("send %q: HTTP %d", text, resp.StatusCode)
	}
}

func TestForwardThroughRealRelay(t *testing.T) {
	const token = "forwardintegrationtoken"
	base := startTestRelay(t, token)
	hook := &hookRecorder{status: func(text string, n int) int {
		switch {
		case text == "bad":
			return 400
		case text == "flaky" && n == 1:
			return 500
		}
		return 200
	}}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	for _, s := range []string{"hello", "bad", "flaky", "after"} {
		relaySend(t, base, token, s)
	}
	dl := filepath.Join(t.TempDir(), "dl.jsonl")
	opts := ForwardOptions{
		Channel: "demo", Host: "server", Alias: "hook", Base: base, Token: token,
		Webhook: WebhookConfig{URL: srv.URL + "/hook", Key: "k"},
		PerMin:  100, PerHour: 100, MaxAttempts: 3, DeadLetter: dl,
		Once: true, OnceIdle: time.Second, sleep: noSleep,
	}
	if err := Forward(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(hook.texts(), ","); got != "hello,bad,flaky,flaky,after" {
		t.Fatalf("webhook saw %s", got)
	}
	for _, a := range hook.auth {
		if a != "Bearer k" {
			t.Fatalf("auth header %q", a)
		}
	}
	if b, _ := os.ReadFile(dl); !strings.Contains(string(b), `"reason":"HTTP 400"`) || strings.Count(string(b), "\n") != 1 {
		t.Fatalf("dead letters: %s", b)
	}

	// everything was ACKed: a second pass delivers nothing
	if err := Forward(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if n := len(hook.texts()); n != 5 {
		t.Fatalf("second pass re-sent: %d posts", n)
	}
}

func TestForwardAbandonedDeliveryIsRedelivered(t *testing.T) {
	const token = "forwardredelivertoken"
	base := startTestRelay(t, token)
	down := true
	var mu sync.Mutex
	hook := &hookRecorder{status: func(string, int) int {
		mu.Lock()
		defer mu.Unlock()
		if down {
			return 503
		}
		return 200
	}}
	srv := httptest.NewServer(hook)
	defer srv.Close()
	relaySend(t, base, token, "keep me")
	// the backoff sleep reports a stop while the relay connection stays open, so
	// an ACK sent for the abandoned message would be committed
	opts := ForwardOptions{
		Channel: "demo", Host: "server", Alias: "hook", Base: base, Token: token,
		Webhook: WebhookConfig{URL: srv.URL + "/hook", Key: "k"},
		PerMin:  100, PerHour: 100, MaxAttempts: 1000, DeadLetter: filepath.Join(t.TempDir(), "dl.jsonl"),
		Once: true, OnceIdle: time.Second,
		sleep: func(context.Context, time.Duration) bool { return false },
	}
	if err := Forward(context.Background(), opts); err == nil {
		t.Fatal("abandoned delivery reported success")
	}
	first := hook.bodies[0]["id"]
	mu.Lock()
	down = false
	mu.Unlock()
	opts.sleep = noSleep
	if err := Forward(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if len(hook.bodies) != 2 || hook.bodies[1]["id"] != first {
		t.Fatalf("want the same message redelivered after an abandoned attempt, got %v", hook.bodies)
	}
}

func TestForwardTimeoutReachesTheRequest(t *testing.T) {
	if c := newForwardClient(0); c.Timeout != DefaultForwardTimeout {
		t.Fatalf("default timeout = %v", c.Timeout)
	}
	release := make(chan struct{})
	defer close(release)
	var got sync.Mutex
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Lock()
		posts++
		got.Unlock()
		select {
		case <-time.After(150 * time.Millisecond):
			w.WriteHeader(200)
		case <-release:
		}
	}))
	defer srv.Close()
	msg, _ := json.Marshal(core.Message{From: "demo/a", To: "demo/hook", TS: "t", Text: "slow"})
	run := func(timeout time.Duration, dl string) error {
		f := &forwarder{ForwardOptions: ForwardOptions{
			Webhook: WebhookConfig{URL: srv.URL, Key: "k"}, PerMin: 100, PerHour: 100, MaxAttempts: 1,
			DeadLetter: dl, sleep: noSleep, Logf: func(string, ...any) {}, client: newForwardClient(timeout),
		}}
		return f.handle(context.Background(), relayFrame{Type: "message", SpoolID: "1.a.json", Message: msg})
	}
	short := filepath.Join(t.TempDir(), "short.jsonl")
	if err := run(20*time.Millisecond, short); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(short); !strings.Contains(string(b), "after 1 attempts") {
		t.Fatalf("a receiver slower than the timeout was not dead-lettered: %s", b)
	}
	long := filepath.Join(t.TempDir(), "long.jsonl")
	if err := run(5*time.Second, long); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(long); !os.IsNotExist(err) {
		t.Fatal("the same receiver failed with a larger timeout")
	}
}
