//go:build darwin || linux

package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveControlSocket serves h on the store's real control socket. The store is
// reached through a short symlink so the socket path fits the unix limit.
func serveControlSocket(t *testing.T, h http.Handler) {
	t.Helper()
	short, err := os.MkdirTemp("/tmp", "cbw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	link := filepath.Join(short, "s")
	if err := os.Symlink(CBUSDir(), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CBUS_DIR", link)
	if err := os.MkdirAll(DaemonDir(), 0700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", daemonSocket())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
}

func TestCLIWritersWakeTheDaemon(t *testing.T) {
	d, cs, _ := schedulerFixture(t, 1)
	serveControlSocket(t, d.handler(func() {}))
	runLoopWithoutTicks(t, d)
	if _, _, _, err := LocalSend("scheduler/worker-0", "scheduler/tester", false, "woken by send"); err != nil {
		t.Fatal(err)
	}
	schedulerWait(t, "send woke the daemon", func() bool { return d.snapshot(cs[0].ID).Accepted == 1 })
	BroadcastPresence("scheduler", "tester", "join", "tester joined", "")
	schedulerWait(t, "presence broadcast woke the daemon", func() bool { return d.snapshot(cs[0].ID).Accepted == 2 })
	if err := DeliverGrantNotice(Grant{ID: "g1", Channel: "scheduler", Alias: "worker-0", SessionID: cs[0].ThreadID, Action: "push"}); err != nil {
		t.Fatal(err)
	}
	schedulerWait(t, "grant notice woke the daemon", func() bool { return d.snapshot(cs[0].ID).Accepted == 3 })
}

func TestDaemonWakeEndpoint(t *testing.T) {
	d, cs, _ := schedulerFixture(t, 1)
	h := d.handler(func() {})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/wake", strings.NewReader(`{"connectionIds":["`+cs[0].ID+`","0000000000"]}`)))
	if r.Code != 200 {
		t.Fatalf("wake: %d %s", r.Code, r.Body.String())
	}
	d.mu.Lock()
	_, known := d.ready[cs[0].ID]
	_, unknown := d.ready["0000000000"]
	d.mu.Unlock()
	if !known || unknown {
		t.Fatalf("ready set after wake: known=%v unknown=%v", known, unknown)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/health", nil))
	if !strings.Contains(r.Body.String(), `"wake":true`) {
		t.Fatalf("health does not advertise wake: %s", r.Body.String())
	}
}

func TestWakeDaemonIsBoundedAndSilent(t *testing.T) {
	setupStore(t)
	start := time.Now()
	WakeDaemon("0123456789") // no daemon at all
	if took := time.Since(start); took > 250*time.Millisecond {
		t.Fatalf("wake without a daemon took %v", took)
	}
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	serveControlSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wake" {
			<-stall // a daemon that never answers
		}
		http.NotFound(w, r)
	}))
	start = time.Now()
	WakeDaemon("0123456789")
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Fatalf("wake against a stalled daemon took %v", took)
	}
	start = time.Now()
	WakeDaemon("") // an unmanaged peer
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Fatalf("a send to an unmanaged peer called the daemon (%v)", took)
	}
}
