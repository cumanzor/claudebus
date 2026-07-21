package client

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- fake codex app-server (shared by the codec and bridge tests) ----

var sockCounter int64

// shortSock returns a unique unix-socket path short enough for SUN_LEN (~104 bytes). A
// t.TempDir() path on darwin is far too long, so this uses /tmp (a real dir on linux, a
// symlink Go's net accepts on darwin).
func shortSock(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("/tmp/cbxt-%d-%d.sock", os.Getpid(), atomic.AddInt64(&sockCounter, 1))
	_ = os.Remove(name)
	t.Cleanup(func() { _ = os.Remove(name) })
	return name
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// fakeSrv is one accepted connection to the fake app-server; the handler replies/notifies.
type fakeSrv struct {
	conn net.Conn
	wmu  sync.Mutex
}

func (s *fakeSrv) reply(id any, result any) { s.send(map[string]any{"id": id, "result": result}) }
func (s *fakeSrv) replyErr(id any, code int, msg string) {
	s.send(map[string]any{"id": id, "error": map[string]any{"code": code, "message": msg}})
}
func (s *fakeSrv) notify(method string, params any) {
	s.send(map[string]any{"method": method, "params": params})
}
func (s *fakeSrv) send(obj any) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = writeServerFrame(s.conn, opText, mustJSON(obj))
}

// fakeCodex is a scripted app-server on a unix socket. handle is invoked per client request.
// With requireInit set it rejects any thread/turn call before initialize (-32600 "Not
// initialized"), mirroring the real app-server so the bridge cannot regress its initialize
// handshake — the gap the field smoke caught.
type fakeCodex struct {
	sock        string
	handle      func(s *fakeSrv, req map[string]any)
	requireInit bool
	mu          sync.Mutex
	calls       []string
}

func (f *fakeCodex) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func startFakeCodex(t *testing.T, handle func(s *fakeSrv, req map[string]any)) *fakeCodex {
	return startFakeCodexOpt(t, handle, false)
}

// startFakeCodexStrict is startFakeCodex that enforces the initialize handshake.
func startFakeCodexStrict(t *testing.T, handle func(s *fakeSrv, req map[string]any)) *fakeCodex {
	return startFakeCodexOpt(t, handle, true)
}

// startFakeCodexAdopt models a zero-turn adopted thread with ASYNC rollout persistence,
// reproducing the F1 race the synchronous fake hid: thread/resume returns -32600 no-rollout
// until MORE than flushAfter resume attempts have followed the opener turn/start (the rollout
// is written lazily), and turn/start emits status active then idle so the bridge can observe
// opener completion without a subscription. flushAfter is the delay knob; 0 = resumable on the
// first post-opener attempt, higher = more flush lag the backoff must ride out. The handler
// runs single-goroutine per connection, so the counters need no lock.
func startFakeCodexAdopt(t *testing.T, flushAfter int) *fakeCodex {
	openerStarted := false
	resumeAfterOpener := 0
	return startFakeCodexStrict(t, func(s *fakeSrv, req map[string]any) {
		switch req["method"] {
		case "thread/resume":
			if openerStarted {
				resumeAfterOpener++
			}
			if openerStarted && resumeAfterOpener > flushAfter {
				s.reply(req["id"], map[string]any{}) // rollout finally flushed
			} else {
				s.replyErr(req["id"], -32600, "no rollout found for thread")
			}
		case "turn/start":
			openerStarted = true
			s.reply(req["id"], map[string]any{"turn": map[string]any{"id": "OPENER"}})
			s.notify("thread/status/changed", map[string]any{"status": map[string]any{"type": "active"}})
			s.notify("thread/status/changed", map[string]any{"status": map[string]any{"type": "idle"}})
		default:
			s.reply(req["id"], map[string]any{})
		}
	})
}

func startFakeCodexOpt(t *testing.T, handle func(s *fakeSrv, req map[string]any), requireInit bool) *fakeCodex {
	t.Helper()
	f := &fakeCodex{sock: shortSock(t), handle: handle, requireInit: requireInit}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatalf("fake listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeCodex) serve(conn net.Conn) {
	br := bufio.NewReader(conn)
	if err := serverHandshake(br, conn); err != nil {
		return
	}
	s := &fakeSrv{conn: conn}
	inited := false
	for {
		fin, op, payload, err := readFrame(br)
		if err != nil {
			return
		}
		if op == opClose {
			return
		}
		if op != opText || !fin {
			continue
		}
		var req map[string]any
		if json.Unmarshal(payload, &req) != nil {
			continue
		}
		method, _ := req["method"].(string)
		if method != "" {
			f.mu.Lock()
			f.calls = append(f.calls, method)
			f.mu.Unlock()
		}
		if f.requireInit && method != "initialize" && !inited {
			s.replyErr(req["id"], -32600, "Not initialized")
			continue
		}
		if method == "initialize" {
			inited = true
		}
		if f.handle != nil {
			f.handle(s, req)
		}
	}
}

// serverHandshake reads the client upgrade request and returns a minimal 101. The client
// does not validate Sec-WebSocket-Accept (a single trusted local peer), so this is enough.
func serverHandshake(br *bufio.Reader, conn net.Conn) error {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	_, err := conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
	return err
}

// writeServerFrame writes one UNmasked frame (server frames are unmasked per RFC 6455).
func writeServerFrame(w io.Writer, op byte, payload []byte) error {
	hdr := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		hdr = append(hdr, ext[:]...)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// writeServerFrameRaw writes a frame with an explicit FIN bit, for fragmentation tests
// (payloads stay < 126 bytes).
func writeServerFrameRaw(w io.Writer, fin bool, op byte, payload []byte) error {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	if _, err := w.Write([]byte{b0, byte(len(payload))}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func mustDial(t *testing.T, sock string) *codexConn {
	t.Helper()
	c, err := dialCodex(sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

// ---- codec tests ----

func TestCodexWSCallResult(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		if req["method"] == "ping" {
			s.reply(req["id"], map[string]any{"pong": true})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	res, err := c.call("ping", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"pong":true`) {
		t.Errorf("result = %s", res)
	}
}

func TestCodexWSErrorCodePropagates(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.replyErr(req["id"], -32600, "no active turn to steer")
	})
	c := mustDial(t, f.sock)
	defer c.close()
	_, err := c.call("turn/steer", map[string]any{})
	if !isRPCCode(err, -32600, "no active turn") {
		t.Fatalf("want rpcError -32600 no-active-turn, got %v", err)
	}
	if isRPCCode(err, -32600, "thread not found") {
		t.Error("substring match must be message-specific")
	}
}

func TestCodexWSNotificationsDelivered(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		s.notify("turn/started", map[string]any{"turn": map[string]any{"id": "T9"}})
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("thread/resume", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-c.notifications():
		if n.Method != "turn/started" || turnIDFromNote(n.Params) != "T9" {
			t.Errorf("note = %+v", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification delivered")
	}
}

// TestCodexWSExtLengths exercises the 16-bit and 64-bit payload-length read paths.
func TestCodexWSExtLengths(t *testing.T) {
	for _, size := range []int{200, 70000} {
		f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
			s.reply(req["id"], map[string]any{"blob": strings.Repeat("x", size)})
		})
		c := mustDial(t, f.sock)
		res, err := c.call("big", map[string]any{})
		if err != nil {
			c.close()
			t.Fatalf("size %d: %v", size, err)
		}
		if !strings.Contains(string(res), strings.Repeat("x", size)) {
			t.Errorf("size %d: payload truncated (len %d)", size, len(res))
		}
		c.close()
	}
}

// TestCodexWSFragmentedResponse: a response split across a text frame (fin=0) and a
// continuation frame (fin=1) is reassembled by the read loop.
func TestCodexWSFragmentedResponse(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		full := mustJSON(map[string]any{"id": req["id"], "result": map[string]any{"ok": 1}})
		half := len(full) / 2
		s.wmu.Lock()
		_ = writeServerFrameRaw(s.conn, false, opText, full[:half])
		_ = writeServerFrameRaw(s.conn, true, opCont, full[half:])
		s.wmu.Unlock()
	})
	c := mustDial(t, f.sock)
	defer c.close()
	res, err := c.call("frag", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `"ok":1`) {
		t.Errorf("reassembled result = %s", res)
	}
}

// TestDialCodexKeepsFirstFrameWithHandshake pins C1: a server that writes its first frame in
// the SAME segment as the 101 response must not lose it. dialCodex threads the handshake
// reader into the read loop; a fresh reader would strand the buffered frame bytes.
func TestDialCodexKeepsFirstFrameWithHandshake(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(conn)
		for {
			line, e := br.ReadString('\n')
			if e != nil {
				return
			}
			if strings.TrimSpace(line) == "" {
				break
			}
		}
		// 101 headers + one notification frame in ONE Write (a single segment).
		var buf bytes.Buffer
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = writeServerFrame(&buf, opText, mustJSON(map[string]any{"method": "hello", "params": map[string]any{}}))
		_, _ = conn.Write(buf.Bytes())
		<-time.After(2 * time.Second) // hold the connection open
	}()
	c := mustDial(t, sock)
	defer c.close()
	select {
	case n := <-c.notifications():
		if n.Method != "hello" {
			t.Errorf("first frame method = %q, want hello", n.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first frame sent with the 101 was lost (handshake reader discarded)")
	}
}

// TestReadFrameRejectsOversize pins C2: a corrupt 64-bit length must error, never drive an
// unbounded allocation. The header claims a 4 GiB frame.
func TestReadFrameRejectsOversize(t *testing.T) {
	hdr := []byte{0x81, 127, 0, 0, 0, 1, 0, 0, 0, 0} // fin+text, 127-len, then 0x100000000 = 4 GiB
	if _, _, _, err := readFrame(bufio.NewReader(bytes.NewReader(hdr))); err == nil {
		t.Fatal("oversize 64-bit frame length must return an error, not allocate")
	}
}

// TestCodexWSPingPong: a server ping does not wedge the read loop — the reply after it
// still lands (the client's pong is emitted by readLoop before it continues).
func TestCodexWSPingPong(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.wmu.Lock()
		_ = writeServerFrame(s.conn, opPing, []byte("hi"))
		s.wmu.Unlock()
		s.reply(req["id"], map[string]any{"after": "ping"})
	})
	c := mustDial(t, f.sock)
	defer c.close()
	res, err := c.call("x", map[string]any{})
	if err != nil {
		t.Fatalf("call after a server ping failed (reader wedged?): %v", err)
	}
	if !strings.Contains(string(res), "ping") {
		t.Errorf("res = %s", res)
	}
}
