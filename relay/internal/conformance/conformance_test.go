package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/core"
	"claudebus/relay/internal/wire"
)

// TestWireConformance builds and runs the real cbus-relay binary in a hermetic
// sandbox (temp spool, ephemeral loopback port, token via env — no ~/.claude-bus,
// no NUC contact) and drives POST /send, GET /peers, and GET /tail(ws) against the
// shared core wire structs. It is the Phase 0 proof that core.SendReq /
// core.PeersResponse / core.Message match the live relay contract end-to-end.
func TestWireConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("builds+runs the relay binary; skipped in -short")
	}
	// this guard must stay ABOVE trackSources: the ordering is load-bearing, not
	// stylistic. trackSources walks ..\..\.. from the package dir, and a `go test -c`
	// binary shipped to a machine with no source tree resolves that walk from wherever
	// cwd happens to be. Reversed, a clean skip becomes a filesystem walk on a stranger's
	// disk.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	trackSources(t) // make the go test cache track the relay we build at runtime

	const token = "conformancetoken00" // subprotocol-safe: no = , / space

	// build the real relay binary into a temp path
	bin := filepath.Join(t.TempDir(), "cbus-relay")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/relay/cmd/cbus-relay").CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v\n%s", err, out)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	spool := t.TempDir()

	var logs bytes.Buffer
	cmd := exec.Command(bin, "-listen", addr, "-spool", spool, "-token-file", filepath.Join(t.TempDir(), "unused"))
	cmd.Env = append(os.Environ(), "CBUS_RELAY_TOKEN="+token) // env wins over the (absent) token file
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	base := "http://" + addr
	waitHealthz(t, base, cmd, &logs)

	// --- POST /send with a core.SendReq -------------------------------------
	req := core.SendReq{
		Channel: "conf",
		Alias:   "rig",
		From:    "conf/tester",
		Text:    "hello from the rig — 你好 🎉\nsecond line",
		TS:      "2026-07-13T00:00:00Z",
	}
	body, _ := json.Marshal(req)
	hreq, _ := http.NewRequest(http.MethodPost, base+"/send", bytes.NewReader(body))
	hreq.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatalf("POST /send: %v", err)
	}
	sendBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /send status=%d body=%s (core.SendReq did not match the relay contract)", resp.StatusCode, sendBody)
	}
	var sr struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(sendBody, &sr); err != nil || !sr.OK || sr.ID == "" {
		t.Fatalf("POST /send response %q not {ok:true,id:...}: %v", sendBody, err)
	}

	// --- GET /peers decoded into core.PeersResponse -------------------------
	preq, _ := http.NewRequest(http.MethodGet, base+"/peers", nil)
	preq.Header.Set("Authorization", "Bearer "+token)
	presp, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatalf("GET /peers: %v", err)
	}
	var peers core.PeersResponse
	if err := json.NewDecoder(presp.Body).Decode(&peers); err != nil {
		t.Fatalf("decode /peers into core.PeersResponse: %v", err)
	}
	presp.Body.Close()
	entry, ok := peers["conf/rig"]
	if !ok {
		t.Fatalf("conf/rig absent from /peers: %+v", peers)
	}
	if entry.Queued < 1 {
		t.Errorf("conf/rig queued=%d, want >=1 (message spooled, not yet drained)", entry.Queued)
	}

	// --- GET /tail(ws): receive the framed delivery -------------------------
	conn, err := wire.Dial(addr, "/tail?channel=conf&alias=rig", "bearer.cbus."+token, 5*time.Second)
	if err != nil {
		t.Fatalf("ws dial /tail: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame []byte
	for i := 0; i < 5 && frame == nil; i++ {
		op, payload, err := conn.ReadFrame()
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		switch op {
		case wire.OpText:
			frame = payload
		case wire.OpPing:
			_ = conn.WriteFrame(wire.OpPong, payload)
		case wire.OpClose:
			t.Fatal("relay closed before delivering")
		}
	}
	if frame == nil {
		t.Fatal("no OpText frame delivered over /tail")
	}

	// the relay stores {from,to,ts,text} and delivers reframe(stored). Reconstruct
	// the stored line exactly as the relay does (main.go:181-187) and assert the
	// delivered frame equals core.Reframe of it — the full send->spool->ws path.
	stored, _ := json.Marshal(map[string]string{
		"from": req.From, "to": req.Channel + "/" + req.Alias, "ts": req.TS, "text": req.Text,
	})
	if want := core.Reframe(stored); !bytes.Equal(frame, want) {
		t.Fatalf("delivered frame != core.Reframe(stored line)\n got:  %q\n want: %q", frame, want)
	}

	// and core.Message round-trips the relay's stored line shape.
	m, err := core.DecodeMessage(stored)
	if err != nil {
		t.Fatalf("core.DecodeMessage(stored): %v", err)
	}
	if m.From != req.From || m.To != "conf/rig" || m.TS != req.TS || m.Text != req.Text {
		t.Fatalf("core.Message did not round-trip the stored line: %+v", m)
	}

	// --- negative assertions: the relay's auth + name gates -------------------
	// wrong bearer -> 401
	if code := postStatus(t, base, "Bearer wrongtoken", body); code != http.StatusUnauthorized {
		t.Errorf("POST /send with a bad bearer: status=%d, want 401", code)
	}
	// good bearer, invalid channel name -> 400
	invalid, _ := json.Marshal(core.SendReq{Channel: "..", Alias: "rig", From: "x", Text: "hi"})
	if code := postStatus(t, base, "Bearer "+token, invalid); code != http.StatusBadRequest {
		t.Errorf("POST /send with an invalid channel name: status=%d, want 400", code)
	}
}

// postStatus POSTs body to /send with the given Authorization header and returns
// the status code.
func postStatus(t *testing.T, base, auth string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/send", bytes.NewReader(body))
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /send: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// trackSources reads every .go file under the relay and shared-core trees so the
// go test cache invalidates this package's result whenever the relay we build and
// run at RUNTIME changes. Without it, editing relay/cmd/cbus-relay/main.go leaves
// this test binary unchanged and `go test` serves a stale PASS (the relay is
// exec'd here, not imported) — the caching gotcha documented in doc.go. The file
// opens are what the test cache records.
func trackSources(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "..", "..") // repo root, from relay/internal/conformance
	for _, tree := range []string{
		filepath.Join(root, "relay"),
		filepath.Join(root, "internal", "core"),
	} {
		err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if strings.HasSuffix(path, ".go") {
				if _, err := os.ReadFile(path); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("track sources under %s: %v", tree, err)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitHealthz(t *testing.T, base string, cmd *exec.Cmd, logs *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("relay exited early:\n%s", logs.String())
		}
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) == "ok\n" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("relay /healthz not ready within deadline:\n%s", logs.String())
}
