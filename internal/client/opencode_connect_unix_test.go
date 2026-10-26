//go:build darwin || linux

package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	ocS1 = "ses_ea0b7d986ffeHkMekQg7NOClOy"
	ocS2 = "ses_e9e8cfe0effePOHiD0hga9x45k"
)

func opencodeNever() (OpencodeConnectBinding, error) {
	return OpencodeConnectBinding{}, errors.New("OpenCode resolver must not run")
}

// TestOpencodePluginHelperProcess stands in for an OpenCode TUI: it serves the
// plugin's hello and status on <dir>/<name> until killed.
func TestOpencodePluginHelperProcess(t *testing.T) {
	dir := os.Getenv("CBUS_OC_HELPER_DIR")
	if dir == "" {
		return
	}
	name := os.Getenv("CBUS_OC_HELPER_NAME")
	if name == "" {
		name = strconv.Itoa(os.Getpid()) + ".sock"
	}
	protocol, _ := strconv.Atoi(os.Getenv("CBUS_OC_HELPER_PROTOCOL"))
	if protocol == 0 {
		protocol = opencodePluginProtocol
	}
	shown := os.Getenv("CBUS_OC_HELPER_SESSION")
	ctlPath, logPath := os.Getenv("CBUS_OC_HELPER_CTL"), os.Getenv("CBUS_OC_HELPER_LOG")
	_ = os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, name)
	l, err := net.Listen("unix", path)
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(2)
	}
	mode := os.FileMode(0o600)
	if m := os.Getenv("CBUS_OC_HELPER_MODE"); m != "" {
		v, _ := strconv.ParseUint(m, 8, 32)
		mode = os.FileMode(v)
	}
	_ = os.Chmod(path, mode)
	var mu sync.Mutex
	stored := map[string]string{}
	fmt.Println("ready")
	for {
		c, err := l.Accept()
		if err != nil {
			os.Exit(0)
		}
		go func(c net.Conn) {
			defer c.Close()
			line, err := bufio.NewReader(c).ReadBytes('\n')
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ctl := opencodeHelperControl{State: "idle", Shown: shown, Deliver: "ok"}
			if b, err := os.ReadFile(ctlPath); err == nil {
				_ = json.Unmarshal(b, &ctl)
			}
			if logPath != "" {
				if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
					_, _ = f.Write(line)
					f.Close()
				}
			}
			var req struct{ Op, SessionID, MessageID, Text string }
			_ = json.Unmarshal(line, &req)
			if req.Op == "watch" {
				mu.Unlock()
				opencodeHelperWatch(c, ctlPath, ctl)
				mu.Lock()
				return
			}
			var sid any
			if ctl.Shown != "" {
				sid = ctl.Shown
			}
			reply := map[string]any{"ok": true}
			switch req.Op {
			case "hello":
				reply["protocol"], reply["plugin"], reply["pid"], reply["sessionID"] = protocol, "1", os.Getpid(), sid
			case "status":
				reply["sessionID"], reply["state"] = sid, ctl.State
			case "lookup":
				// OpenCode 1.19.3 answers a message lookup in a deleted session
				// the same as a missing message
				_, ok := stored[req.MessageID]
				reply["present"] = ok && !ctl.Deleted
			case "session":
				reply["exists"] = !ctl.Deleted
			case "deliver":
				switch ctl.Deliver {
				case "ok":
					stored[req.MessageID] = req.Text
					reply["receipt"] = "persisted"
				case "unconfirmed":
					stored[req.MessageID] = req.Text
					reply["receipt"] = "submitted-unconfirmed"
				case "notsubmitted":
					reply = map[string]any{"ok": false, "code": "session-not-found", "error": "Session not found", "submitted": false}
				default:
					reply = map[string]any{"ok": false, "code": "failed", "error": "socket hang up", "submitted": "unknown"}
				}
			default:
				reply = map[string]any{"ok": false, "code": "bad-request"}
			}
			b, _ := json.Marshal(reply)
			_, _ = c.Write(append(b, '\n'))
		}(c)
	}
}

type opencodeHelperControl struct {
	State   string `json:"state"`
	Shown   string `json:"shown"`
	Deliver string `json:"deliver"`
	Deleted bool   `json:"deleted"`
	Dispose bool   `json:"dispose"`
}

// opencodeHelperWatch mirrors the plugin's watch: a snapshot, then a status or
// route event whenever the control file changes, and dispose on request.
func opencodeHelperWatch(c net.Conn, ctlPath string, ctl opencodeHelperControl) {
	write := func(v map[string]any) bool {
		b, _ := json.Marshal(v)
		_, err := c.Write(append(b, '\n'))
		return err == nil
	}
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	if !write(map[string]any{"ok": true, "event": "snapshot", "sessionID": nullable(ctl.Shown), "state": ctl.State}) {
		return
	}
	last := ctl
	for {
		time.Sleep(20 * time.Millisecond)
		next := last
		if b, err := os.ReadFile(ctlPath); err == nil {
			_ = json.Unmarshal(b, &next)
		}
		switch {
		case next.Dispose:
			write(map[string]any{"event": "dispose"})
			return
		case next.Shown != last.Shown:
			if !write(map[string]any{"event": "route", "sessionID": nullable(next.Shown), "state": next.State}) {
				return
			}
		case next.State != last.State:
			if !write(map[string]any{"event": "status", "sessionID": nullable(next.Shown), "state": next.State}) {
				return
			}
		}
		last = next
	}
}

type opencodeHelper struct {
	pid  int
	path string
	cmd  *exec.Cmd
}

func startOpencodeHelper(t *testing.T, session string, env ...string) opencodeHelper {
	t.Helper()
	dir := opencodeEndpointDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOpencodePluginHelperProcess$")
	cmd.Env = append(os.Environ(), append([]string{"CBUS_OC_HELPER_DIR=" + dir, "CBUS_OC_HELPER_SESSION=" + session}, env...)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pid := startTracked(t, cmd)
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper did not start: %q", line)
	}
	name := strconv.Itoa(pid) + ".sock"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "CBUS_OC_HELPER_NAME="); ok {
			name = v
		}
	}
	return opencodeHelper{pid: pid, path: filepath.Join(dir, name), cmd: cmd}
}

func (h opencodeHelper) kill(t *testing.T) {
	t.Helper()
	_ = h.cmd.Process.Kill()
	_, _ = h.cmd.Process.Wait()
	for i := 0; i < 100; i++ {
		if exited, _ := codexOwnerExited(h.pid, "x"); exited {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// opencodeTestStore uses a short store root: t.TempDir embeds the test name,
// and a long one would push socket paths past the 104-byte limit.
func opencodeTestStore(t *testing.T) {
	t.Helper()
	setupStore(t)
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	root, err := os.MkdirTemp(base, "cboc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("CBUS_DIR", root)
	if p := filepath.Join(root, ".opencode", "4194304.sock"); len(p) > 103 {
		t.Fatalf("store path %s leaves no room for a socket name", root)
	}
}

func TestOpencodeSocketPID(t *testing.T) {
	for name, want := range map[string]int{"/d/123.sock": 123, "/d/0.sock": 0, "/d/1.sock": 0, "/d/12a.sock": 0, "/d/.123.99.sock": 0, "/d/123.sock.tmp": 0} {
		pid, ok := opencodeSocketPID(name)
		if (want == 0) == ok || (ok && pid != want) {
			t.Errorf("opencodeSocketPID(%q) = %d %v, want %d", name, pid, ok, want)
		}
	}
}

func TestDiscoverOpencodeEndpointFindsExactlyOne(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS1)
	startOpencodeHelper(t, ocS2)
	e, err := discoverOpencodeEndpoint(ocS1)
	if err != nil {
		t.Fatal(err)
	}
	if e.PID != h.pid || e.Socket != h.path || e.StartToken == "" || e.Ino == 0 {
		t.Fatalf("endpoint = %+v, want helper %d at %s", e, h.pid, h.path)
	}
	if _, err := discoverOpencodeEndpoint("ses_" + strings.Repeat("z", 26)); err == nil || !strings.Contains(err.Error(), "no OpenCode TUI") {
		t.Fatalf("unshown session: %v", err)
	}
}

func TestDiscoverOpencodeEndpointRefusesTwoPanesOnOneSession(t *testing.T) {
	opencodeTestStore(t)
	a := startOpencodeHelper(t, ocS1)
	b := startOpencodeHelper(t, ocS1)
	_, err := discoverOpencodeEndpoint(ocS1)
	if err == nil || !strings.Contains(err.Error(), "2 OpenCode TUIs") ||
		!strings.Contains(err.Error(), strconv.Itoa(a.pid)) || !strings.Contains(err.Error(), strconv.Itoa(b.pid)) {
		t.Fatalf("two panes: %v", err)
	}
}

func TestDiscoverOpencodeEndpointReportsWhatItSkipped(t *testing.T) {
	opencodeTestStore(t)
	startOpencodeHelper(t, ocS1, "CBUS_OC_HELPER_PROTOCOL=2")
	startOpencodeHelper(t, ocS1, "CBUS_OC_HELPER_MODE=644")
	_, err := discoverOpencodeEndpoint(ocS1)
	if err == nil || !strings.Contains(err.Error(), "protocol 2") || !strings.Contains(err.Error(), "private socket") {
		t.Fatalf("skipped endpoints not reported: %v", err)
	}
}

// A socket named after another process is not that process's endpoint.
func TestDiscoverOpencodeEndpointChecksThePeerPid(t *testing.T) {
	opencodeTestStore(t)
	impostor := strconv.Itoa(os.Getpid()) + ".sock"
	startOpencodeHelper(t, ocS1, "CBUS_OC_HELPER_NAME="+impostor)
	_, err := discoverOpencodeEndpoint(ocS1)
	if err == nil || !strings.Contains(err.Error(), "socket peer does not match") {
		t.Fatalf("impostor accepted: %v", err)
	}
}

func TestDiscoverOpencodeEndpointSkipsAnExitedTUI(t *testing.T) {
	opencodeTestStore(t)
	dead := startOpencodeHelper(t, ocS1)
	dead.kill(t)
	live := startOpencodeHelper(t, ocS1)
	e, err := discoverOpencodeEndpoint(ocS1)
	if err != nil || e.PID != live.pid {
		t.Fatalf("exited TUI was not skipped silently: %+v %v", e, err)
	}
}

func TestOpencodeConnectIdentityUsesOnlyThePluginSession(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS1)
	// an inherited value shaped like an OpenCode session must still not be used
	t.Setenv("CLAUDE_CODE_SESSION_ID", ocS1)
	t.Setenv("CBUS_SESSION_ID", "")
	if _, err := OpencodeConnectIdentity(); err == nil || !strings.Contains(err.Error(), "cbus install-opencode") {
		t.Fatalf("missing plugin session: %v", err)
	}
	t.Setenv("CBUS_SESSION_ID", ocS1)
	b, err := OpencodeConnectIdentity()
	if err != nil || b.SessionID != ocS1 || b.Endpoint.PID != h.pid || !filepath.IsAbs(b.Cwd) {
		t.Fatalf("binding = %+v, %v", b, err)
	}
	req, token, err := nativeConnectIdentityForHarness("opencode", CodexConnectOptions{}, nil, nil, func() (OpencodeConnectBinding, error) { return b, nil })
	if err != nil || token != "" || req.Opencode == nil || req.ThreadID != ocS1 || req.Claude != nil {
		t.Fatalf("dispatch = %+v %q %v", req, token, err)
	}
	if _, _, err := nativeConnectIdentityForHarness("opencode", CodexConnectOptions{SQLiteHome: "/x"}, nil, nil, opencodeNever); err == nil {
		t.Fatal("OpenCode accepted a Codex store option")
	}
}

func opencodeDaemon(t *testing.T) *busDaemon {
	t.Helper()
	d := testDaemon(t)
	d.start = selfStart(t)
	if err := d.load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for id := range d.connections {
			d.closeQueue(id)
		}
	})
	return d
}

func opencodeConnectRequest(t *testing.T, alias, session string) ConnectRequest {
	t.Helper()
	e, err := discoverOpencodeEndpoint(session)
	if err != nil {
		t.Fatal(err)
	}
	return ConnectRequest{Protocol: DaemonProtocolVersion, Harness: daemonHarnessOpencode, Channel: "dev", Alias: alias, ThreadID: session,
		Opencode: &OpencodeConnectBinding{SessionID: session, Cwd: CBUSDir(), Endpoint: e}}
}

func TestDaemonOpencodeConnectBindsTheShowingTUI(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS1)
	d := opencodeDaemon(t)
	state, err := d.connect(opencodeConnectRequest(t, "oc", ocS1))
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "socket-ready" || state.Opencode == nil || state.Opencode.Binding.Endpoint.PID != h.pid {
		t.Fatalf("state = %+v", state)
	}
	c := d.connections[state.ID]
	p, err := d.consumerProbe(c)
	if err != nil || p.State != "online" || p.PID != h.pid {
		t.Fatalf("probe = %+v %v", p, err)
	}
	m, ok := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
	if !ok || m.Harness != "opencode" || m.SessionID != ocS1 || m.Cwd != CBUSDir() {
		t.Fatalf("meta = %+v", m)
	}
	reloaded := opencodeDaemon(t)
	if r := reloaded.connections[state.ID]; r == nil || r.State == "binding-required" || r.Opencode == nil {
		t.Fatalf("journal did not reload as an OpenCode connection: %+v", r)
	}
	h.kill(t)
	if p, err := d.consumerProbe(c); err != nil || p.State != "exited" {
		t.Fatalf("probe after exit = %+v %v", p, err)
	}
}

func TestDaemonOpencodeRefusesAShownSessionMismatchAndForeignDirs(t *testing.T) {
	opencodeTestStore(t)
	startOpencodeHelper(t, ocS1)
	d := opencodeDaemon(t)
	req := opencodeConnectRequest(t, "oc", ocS1)
	wrong := req
	wrong.ThreadID, wrong.Opencode = ocS2, &OpencodeConnectBinding{SessionID: ocS2, Cwd: req.Opencode.Cwd, Endpoint: req.Opencode.Endpoint}
	if _, err := d.connect(wrong); err == nil || !strings.Contains(err.Error(), "no longer shows session") {
		t.Fatalf("session the TUI does not show: %v", err)
	}
	foreign := req
	b := *req.Opencode
	b.Endpoint.Socket = filepath.Join(t.TempDir(), filepath.Base(b.Endpoint.Socket))
	foreign.Opencode = &b
	if _, err := d.connect(foreign); err == nil || !strings.Contains(err.Error(), "outside this daemon's endpoint directory") {
		t.Fatalf("foreign endpoint dir: %v", err)
	}
	if len(d.statusSnapshots()) != 0 {
		t.Fatal("a refused connect left a connection behind")
	}
}

func TestDaemonOpencodeOneLiveEndpointPerSession(t *testing.T) {
	opencodeTestStore(t)
	first := startOpencodeHelper(t, ocS1)
	d := opencodeDaemon(t)
	state, err := d.connect(opencodeConnectRequest(t, "oc", ocS1))
	if err != nil {
		t.Fatal(err)
	}
	// a second pane on the same session, entering through another channel
	second := startOpencodeHelper(t, ocS1)
	e, err := captureOpencodeEndpoint(second.path)
	if err != nil {
		t.Fatal(err)
	}
	other := ConnectRequest{Protocol: DaemonProtocolVersion, Harness: daemonHarnessOpencode, Channel: "ops", Alias: "oc", ThreadID: ocS1,
		Opencode: &OpencodeConnectBinding{SessionID: ocS1, Cwd: CBUSDir(), Endpoint: e}}
	if _, err := d.connect(other); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("already bound through TUI pid %d", first.pid)) {
		t.Fatalf("second live endpoint: %v", err)
	}
	// once the first TUI exits, the same alias rebinds to the new pane
	first.kill(t)
	again := other
	again.Channel = "dev"
	rebound, err := d.connect(again)
	if err != nil || rebound.ID != state.ID || rebound.Opencode.Binding.Endpoint.PID != second.pid {
		t.Fatalf("reconnect = %+v %v", rebound, err)
	}
}

func TestObserveOpencodeConsumerIsNotOnlineWhenThePaneMovedOn(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS2)
	e, err := captureOpencodeEndpoint(h.path)
	if err != nil {
		t.Fatal(err)
	}
	c := &ConnectionState{Harness: daemonHarnessOpencode, ThreadID: ocS1,
		Opencode: &OpencodeConnectionConfig{Binding: OpencodeConnectBinding{SessionID: ocS1, Cwd: CBUSDir(), Endpoint: e}}}
	p, err := observeOpencodeConsumer(c)
	if err != nil || p.State != "unknown" || !strings.Contains(p.Detail, "does not show session") {
		t.Fatalf("probe = %+v %v", p, err)
	}
}
