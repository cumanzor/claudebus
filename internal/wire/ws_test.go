package wire

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// relaySide serves one upgrade, hands the request to capture, then echoes the
// first text frame back (or just returns on close).
func relaySide(t *testing.T, capture func(*http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(r)
		c, err := Upgrade(w, r, "bearer.cbus.test")
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer c.Close()
		op, payload, err := c.ReadFrame()
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if op == OpText {
			if err := c.WriteFrame(OpText, payload); err != nil {
				t.Errorf("server write: %v", err)
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestDialContextSendsUserAgent(t *testing.T) {
	var gotUA string
	srv := relaySide(t, func(r *http.Request) { gotUA = r.Header.Get("User-Agent") })
	conn, err := DialContext(t.Context(), wsURL(srv), "bearer.cbus.test", 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteFrame(OpText, []byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	op, payload, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if op != OpText || string(payload) != "ping" {
		t.Fatalf("client read %#x %q, want text %q", op, payload, "ping")
	}
	if !strings.HasPrefix(gotUA, "cbus/") {
		t.Fatalf("handshake User-Agent = %q, want a cbus/<version> identity", gotUA)
	}
}

func TestUserAgentCannotInjectHeaders(t *testing.T) {
	old := UserAgent
	t.Cleanup(func() { UserAgent = old })
	UserAgent = "bad\r\nInjected: 1"
	var gotUA, injected string
	srv := relaySide(t, func(r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		injected = r.Header.Get("Injected")
	})
	conn, err := DialContext(t.Context(), wsURL(srv), "bearer.cbus.test", 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if injected != "" {
		t.Fatalf("Injected header leaked through: %q", injected)
	}
	if gotUA != "badInjected: 1" {
		t.Fatalf("User-Agent = %q, want the CRLF-stripped value", gotUA)
	}
}
