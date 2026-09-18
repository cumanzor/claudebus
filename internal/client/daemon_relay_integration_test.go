package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/core"
)

// This integration uses the real relay executable, shared WebSocket transport,
// durable local inbox, daemon restart path, and real stdio queue adapter. Its
// Codex subprocess is the protocol fake that rejects model/thread-start APIs.
func TestDaemonRelayProcessToNativeQueueAcrossRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs isolated relay process")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain unavailable")
	}
	d, _, req := daemonFixture(t)
	bin := filepath.Join(t.TempDir(), "cbus-relay")
	if output, err := exec.Command("go", "build", "-o", bin, "claudebus/relay/cmd/cbus-relay").CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v %s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	spool := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-listen", addr, "-spool", spool, "-token-file", filepath.Join(t.TempDir(), "unused"))
	cmd.Env = append(os.Environ(), "CBUS_RELAY_TOKEN=nativeintegrationtoken")
	var logs codexQueueLog
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	base := "http://" + addr
	probe := &http.Client{Timeout: 200 * time.Millisecond}
	schedulerWait(t, "isolated relay ready", func() bool {
		r, e := probe.Get(base + "/healthz")
		if e != nil {
			return false
		}
		_ = r.Body.Close()
		return r.StatusCode == 200
	})
	bind := func(daemon *busDaemon) {
		q := fakeCodexQueue(t, "normal", time.Second)
		daemon.openQueue = func(CodexQueueConfig) (nativeQueue, error) { return q, nil }
		daemon.dialRelay = func(ctx context.Context, c *ConnectionState) (relaySocket, error) {
			return dialRelayWithToken(ctx, c, "nativeintegrationtoken")
		}
	}
	bind(d)
	stopDaemon := func(daemon *busDaemon) {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := daemon.shutdown(c); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { stopDaemon(d) })
	req.Relay = &RelayConfig{Host: "isolated", Base: base, CredentialDir: t.TempDir()}
	c, err := d.connect(req)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := RemoteEndpoint{FrontDoor: FrontDoor{Base: base, Local: true}, headers: map[string]string{"Authorization": "Bearer nativeintegrationtoken"}}
	send := func(text string) {
		t.Helper()
		if err := RemoteSend(endpoint, core.SendReq{Channel: c.Channel, Alias: c.Alias, From: "dev@isolated/sender", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	deliver := func(daemon *busDaemon, want uint64) {
		t.Helper()
		schedulerWait(t, fmt.Sprintf("relay to native acceptance %d", want), func() bool { daemon.schedule(); return daemon.snapshot(c.ID).Accepted == want })
		schedulerWait(t, "delivery workers complete", func() bool { return len(daemon.slots) == 0 })
	}
	send("before disconnect")
	deliver(d, 1)
	if err := d.disconnect(ConnectionTarget(c)); err != nil {
		t.Fatal(err)
	}
	stopDaemon(d)
	send("queued while daemon stopped")
	queued := filepath.Join(spool, c.Channel, c.Alias, "new")
	if entries, err := os.ReadDir(queued); err != nil || len(entries) != 1 {
		t.Fatalf("offline remote message did not stay pending: %+v %v", entries, err)
	}
	restarted := newBusDaemon()
	restarted.start = d.start
	restarted.probeConsumer = d.probeConsumer
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	bind(restarted)
	t.Cleanup(func() { stopDaemon(restarted) })
	again, err := restarted.connect(req)
	if err != nil || again.ID != c.ID || again.Accepted != 1 {
		t.Fatalf("restart changed binding/progress: %+v %v", again, err)
	}
	deliver(restarted, 2)
	if _, err := restarted.connect(req); err != nil {
		t.Fatal(err)
	}
	deliver(restarted, 2)
	line, err := os.ReadFile(filepath.Join(restarted.peerDir(c), "inbox.jsonl"))
	if err != nil || strings.Count(string(line), "\n") != 2 || !strings.Contains(string(line), "dev@isolated/sender") {
		t.Fatalf("wrong durable inbox after restart: %s %v", line, err)
	}
	schedulerWait(t, "relay ACK committed", func() bool { entries, err := os.ReadDir(queued); return err == nil && len(entries) == 0 })
	entries, err := os.ReadDir(filepath.Join(spool, c.Channel, c.Alias, "cur"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("relay did not retain exactly two ACKed records: %+v %v", entries, err)
	}
}
