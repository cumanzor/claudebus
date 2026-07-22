package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"claudebus/internal/core"
)

// codexwrap is `cbus codex`: it stands up a per-peer codex app-server, launches a codex
// --remote TUI attached to it, learns the TUI's thread id from its own passive server
// connection, joins the bus as that thread, then bridges bus messages into that thread.
//
// The rendezvous is discovery, NOT a hook (cbus-6ij.4 F1): SessionStart hooks fire for exec
// and interactive-local sessions but NOT in the app-server/--remote topology, and the
// app-server has no trust-bypass flag — a live probe confirmed a real turn completing with the
// hook silently absent. Instead a passive connection (initialize only, opened before the TUI)
// receives the TUI thread's thread/started notification, which carries the thread id and cwd.
// The wrapper takes that id and joins directly with it as the session id (the --session-id
// mechanism from cbus-6ij.1). thread/list discovery is deliberately NOT used: it returns the
// user's whole codex history, so "the one live thread" is not knowable from it (A3.0).

const (
	// sunPathMax is a conservative bound on a unix socket path: sockaddr_un.sun_path is 104
	// bytes incl NUL on darwin (108 on linux), so a path under 103 fits everywhere.
	sunPathMax    = 103
	codexServerUp = 10 * time.Second
	discoverWait  = 45 * time.Second // the TUI attaches and starts its thread well within this
)

// allocCodexSocket picks a per-launch socket path short enough for SUN_LEN. It prefers
// $CBUS_DIR/.sock — dot-prefixed so channel walkers skip it, 0700, and it survives tmp
// cleaners — and falls back to os.TempDir()/cbus-codex when the CBUS_DIR path is too deep,
// erroring loudly when neither fits. nonce makes the path unique per launch.
func allocCodexSocket(nonce string) (string, error) {
	for _, base := range []string{filepath.Join(CBUSDir(), ".sock"), filepath.Join(os.TempDir(), "cbus-codex")} {
		s := filepath.Join(base, nonce+".sock")
		if len(s) <= sunPathMax {
			if err := os.MkdirAll(base, 0o700); err != nil {
				return "", err
			}
			return s, nil
		}
	}
	return "", fmt.Errorf("no unix socket path fits under %d bytes: $CBUS_DIR and TempDir are both too deep (set CBUS_DIR to a shorter path)", sunPathMax)
}

func randNonce() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// codexRemoteArgs builds the codex --remote launch args (after "codex"): attach the TUI to the
// app-server socket, then the user's passthrough. No hook and no trust bypass — the F1 probe
// showed SessionStart hooks do not fire in the app-server/--remote topology, so the wrapper
// discovers the thread itself.
func codexRemoteArgs(sock string, passthrough []string) []string {
	return append([]string{"--remote", "unix://" + sock}, passthrough...)
}

// codexRemoteEnv is the environment for the codex --remote TUI: the launcher's env with the
// session-id vars SCRUBBED and CBUS_ALIAS/CBUS_CHANNEL SET. Scrubbing is the identity fix
// (cbus-6ij.4, found live): the codex model runs shell commands that inherit this env, and a
// leaked launcher CLAUDE_CODE_SESSION_ID / CBUS_SESSION_ID / GROK_SESSION_ID would make its
// `cbus send` resolve the LAUNCHER's registration and speak with spoofed provenance. With the
// whole SessionID() chain gone, cbus falls to the CBUS_ALIAS path and the codex peer
// self-identifies as itself. The thread id is unknown at spawn (discovery completes later), so
// alias-based identity is the mechanism, not a session-id env.
func codexRemoteEnv(channel, alias string) []string {
	drop := map[string]bool{
		"CLAUDE_CODE_SESSION_ID": true,
		"CBUS_SESSION_ID":        true,
		"GROK_SESSION_ID":        true,
		"CBUS_ALIAS":             true, // set below; drop any inherited value first
		"CBUS_CHANNEL":           true,
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			env = append(env, kv)
		}
	}
	return append(env, "CBUS_ALIAS="+alias, "CBUS_CHANNEL="+channel)
}

// codexCommands constructs the two child processes the wrapper launches — the per-peer
// app-server and the codex --remote TUI — both with the scrubbed CBUS_ALIAS env. This is the
// identity fix: codex's tool shells execute in the APP-SERVER process tree in the remote
// topology (not the TUI), so scrubbing the TUI alone left the launcher session-id reachable and
// the leak live; scrubbing the app-server (the execution locus) closes it, and scrubbing the
// TUI too is defense in depth. The caller sets SysProcAttr/streams and starts each in order.
func codexCommands(channel, alias, sock string, passthrough []string) (srv, tui *exec.Cmd) {
	env := codexRemoteEnv(channel, alias)
	srv = exec.Command("codex", "app-server", "--listen", "unix://"+sock)
	srv.Env = env
	tui = exec.Command("codex", codexRemoteArgs(sock, passthrough)...)
	tui.Env = env
	return srv, tui
}

// threadStartedInfo pulls the thread id and cwd out of a thread/started notification.
func threadStartedInfo(params json.RawMessage) (id, cwd string) {
	var p struct {
		Thread struct {
			ID  string `json:"id"`
			Cwd string `json:"cwd"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(params, &p)
	return p.Thread.ID, p.Thread.Cwd
}

// discoverThread blocks until the app-server pushes a thread/started for the TUI's thread (a
// passive connection receives it, F1 probe), returning that thread id. The cwd is a HARD
// check: a per-peer app-server must serve exactly this wrapper's TUI, so a thread/started whose
// cwd disagrees with the wrapper's is refused loudly (both paths named) rather than adopted —
// same loudness as the rejected exactly-one-thread contract. On expiry it names the likely
// cause rather than hanging: the --remote session never reached the app-server.
func discoverThread(conn *codexConn, wantCwd string, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case note := <-conn.notifications():
			if note.Method != "thread/started" {
				continue
			}
			id, cwd := threadStartedInfo(note.Params)
			if id == "" {
				continue // malformed notification; the timer still bounds the wait
			}
			if wantCwd != "" && cwd != "" && cwd != wantCwd {
				return "", fmt.Errorf("codex started a thread in %q but this wrapper runs in %q — refusing (a per-peer app-server must serve exactly this TUI)", cwd, wantCwd)
			}
			return id, nil
		case <-timer.C:
			return "", fmt.Errorf("codex --remote never started a thread within %s: the TUI did not attach to the app-server", timeout)
		}
	}
}

// waitForSocket polls until the app-server's unix socket exists.
func waitForSocket(sock string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %s", sock, timeout)
}

// joinAs joins channel/alias with sid as this session's id (the --session-id mechanism): the
// codex peer registers under its own thread id, so the bridge can arm that alias.
func joinAs(sid, channel, alias string) error {
	defer OverrideSessionID(sid)()
	_, _, err := Join(channel, alias)
	return err
}

// claimListenerAtJoin makes the codex peer read as a LIVE listener the instant it joins,
// closing the tens-of-seconds window before the bridge arms during which `cbus list` shows
// `off pid=?` and operators (or a sweep) misread the peer as dead. The bridge arms in THIS
// process (RunCodexBridge runs in a goroutine of the wrapper), so the wrapper's own pid and
// start-time are an honest structural witness immediately; the durable inbox queues any
// pre-arm message and the bridge's first arm delivers it.
//
// The replay cursor seed is load-bearing: committing listenerPid makes resolveResume read a
// cursor-absent peer as an ever-armed migration and seek the real arm to EOF, silently
// dropping everything queued in the window. A seeded cursor moves the arm onto the
// cursor-valid path (resume at offset 0 = full replay). ORDER MATTERS: armMeta and writeCursor
// are both void best-effort, so committing listenerPid before a seed that independently fails
// (ENOSPC/EIO/a rename race) would leave listenerPid-set + cursor-absent, the exact seek-END
// mail-loss state the seed exists to prevent. So seed FIRST, VERIFY it round-trips through
// readCursor, and only then armMeta — an unverified seed skips the claim entirely, which
// leaves listenerPid null and keeps resolveResume on the never-armed byte-0 full-replay path.
// The listenerPid-set + cursor-absent state is unreachable by construction.
//
// Best-effort throughout: a missing witness or a seed that will not round-trip degrades to the
// pre-fix display bug (`off pid=?`), never to mail loss and never to a phantom listener. A
// claim-then-attach-failure leaves the wrapper's real pid recorded; when the wrapper exits,
// that pid is dead and MetaListenerAlive reads it dead, so liveness handles it structurally.
func claimListenerAtJoin(channel, alias string) {
	peerDir := filepath.Join(CBUSDir(), channel, alias)
	start, err := procStartTime(os.Getpid())
	if err != nil {
		return // no witness: a claim would read dead anyway
	}
	st, serr := os.Stat(InboxPath(channel, alias))
	if serr != nil {
		return
	}
	dev, ino, ok := statDevIno(st)
	if !ok {
		return
	}
	writeCursor(peerDir, dev, ino, 0)
	if cd, ci, co, state := readCursor(peerDir); state != cursorValid || cd != dev || ci != ino || co != 0 {
		return // seed did not round-trip: skip the claim rather than risk seek-END mail loss
	}
	armMeta(filepath.Join(peerDir, "meta.json"), start)
}

// RunCodexWrap is `cbus codex`. It blocks until the TUI exits, then tears down the app-server
// (and its native child — the npm codex runs one under a node shim, A2) via a group kill.
func RunCodexWrap(channel, alias string, passthrough []string) error {
	if channel == "" {
		channel = branchChannelFromGit()
	}
	if !core.ValidStoreName(channel) {
		return fmt.Errorf("bad channel %q", channel)
	}
	if !core.ValidStoreName(alias) {
		return fmt.Errorf("bad alias %q", alias)
	}
	nonce, err := randNonce()
	if err != nil {
		return err
	}
	sock, err := allocCodexSocket(nonce)
	if err != nil {
		return err
	}
	_ = os.Remove(sock)

	// 1. per-peer app-server in its own process group, so teardown takes the native child. Both
	//    the app-server and the TUI get the scrubbed CBUS_ALIAS env (codexCommands); the
	//    app-server is the execution locus for codex's tool shells, so its scrub is the fix.
	srv, tui := codexCommands(channel, alias, sock, passthrough)
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start codex app-server: %w", err)
	}
	// killServer takes down the app-server GROUP (SIGTERM, escalating to SIGKILL) and REAPS
	// it, so the app-server's dying stderr — the "WebSocket protocol error: Connection reset"
	// line — has flushed to the terminal by the time it returns. The teardown path calls it
	// BEFORE printing the bridge cause so the cause is the last thing on screen; the defer is
	// the backstop for every early-return and the normal-quit path. sync.Once makes the two
	// callers idempotent (srv.Wait must run once).
	var serverDown sync.Once
	killServer := func() {
		serverDown.Do(func() {
			_ = syscall.Kill(-srv.Process.Pid, syscall.SIGTERM)
			reaped := make(chan struct{})
			go func() { _, _ = srv.Process.Wait(); close(reaped) }()
			if !reapWithin(reaped, serverReapWait) {
				_ = syscall.Kill(-srv.Process.Pid, syscall.SIGKILL)
				// BOTH waits are bounded: an app-server wedged in D-state does not reap even
				// on SIGKILL, and a bare receive here would suppress the teardown cause forever.
				// Printing the cause outranks reaping (the ruled never-suppress property), so on
				// expiry we leave the orphan for process-exit to reap and press on.
				reapWithin(reaped, serverReapWait)
			}
			_ = os.Remove(sock)
		})
	}
	defer killServer()
	if err := waitForSocket(sock, codexServerUp); err != nil {
		return err
	}

	// 2. a passive discovery connection, opened BEFORE the TUI so it catches the TUI thread's
	//    thread/started (a late connection would miss the live push).
	disc, err := dialCodex(sock)
	if err != nil {
		return fmt.Errorf("connect to app-server for thread discovery: %w", err)
	}
	defer disc.close()
	if _, err := disc.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "cbus-codex", "version": "1"},
	}); err != nil {
		return fmt.Errorf("initialize discovery connection: %w", err)
	}

	// 3. the TUI, attached to the app-server; it takes over the terminal. Its env is already the
	//    scrubbed one (codexCommands); it takes the terminal streams here.
	tui.Stdin, tui.Stdout, tui.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := tui.Start(); err != nil {
		return fmt.Errorf("start codex --remote: %w", err)
	}

	// 4. learn the TUI thread id from the discovery connection, then join the bus as it.
	threadID, err := discoverThread(disc, cwd(), discoverWait)
	if err != nil {
		_ = tui.Process.Kill()
		return err
	}
	disc.close()
	if err := joinAs(threadID, channel, alias); err != nil {
		_ = tui.Process.Kill()
		return fmt.Errorf("join %s/%s as the codex thread: %w", channel, alias, err)
	}
	claimListenerAtJoin(channel, alias) // read as a live listener before the bridge arms

	// 5. bridge the alias inbox into the TUI thread. RunCodexBridge blocks in the follower for
	//    the wrapper's lifetime; if it ever returns (startup failure OR the listener going
	//    dormant) while the TUI still lives, the peer is deaf. Fail the WHOLE unit: kill the TUI
	//    so the human sees the session end instead of a healthy-looking window with no delivery
	//    (the session is recoverable via codex resume). The bridge queues its cause BEFORE the
	//    kill, so a bridge-driven teardown has it ready the instant tui.Wait returns.
	bridgeExit := make(chan error, 1)
	go func() {
		berr := RunCodexBridge(channel+"/"+alias, sock, threadID)
		bridgeExit <- berr
		_ = tui.Process.Kill()
	}()

	// 6. the human drives the TUI; when it exits, resolve the cause and print it LAST.
	werr := tui.Wait()
	return teardownOutcome(werr, bridgeExit, killServer, os.Stderr, bridgeCauseGrace)
}

var (
	// bridgeCauseGrace bounds how long the teardown waits for a self-exited TUI's bridge to
	// name a cause (the same app-server reset can down both, bridge a beat behind). It only
	// bounds the WAIT, never suppresses a cause. serverReapWait bounds the SIGTERM reap before
	// escalating to SIGKILL. Vars, not consts, so tests shrink them.
	bridgeCauseGrace = 2 * time.Second
	serverReapWait   = 2 * time.Second
)

// reapWithin waits for reaped to close, up to d, returning whether the reap completed. On
// expiry it returns false rather than blocking, so a wedged (D-state) app-server that will not
// reap even on SIGKILL can never suppress the teardown cause forever — printing outranks
// reaping.
func reapWithin(reaped <-chan struct{}, d time.Duration) bool {
	select {
	case <-reaped:
		return true
	case <-time.After(d):
		return false
	}
}

// teardownOutcome decides the wrapper's exit and prints the cause LAST — after killServer has
// killed+reaped the app-server, so the app-server's dying "WebSocket protocol error" stderr
// has flushed and the bridge cause is the final line the operator sees. killServer and out are
// seams: production passes the real closure and os.Stderr, a test passes stubs to pin ordering.
//
//   - bridge-driven teardown: the goroutine queues berr before it kills the TUI, so berr is
//     ready the instant tui.Wait returns. A non-blocking read catches it; flush, then cause.
//   - clean self-exit (werr == nil): the human quit, the bridge stays healthy and silent;
//     nothing to surface, just flush and return.
//   - abnormal self-exit (werr != nil): the reset that killed the TUI will down the bridge a
//     beat later — wait the bounded grace for its cause; on expiry, still flush and surface
//     what is known (the TUI's own error), last.
//
// A berr read AFTER killServer would be a WE-killed-it artifact, not the incident, so only a
// berr observed BEFORE killServer is ever treated as the cause.
func teardownOutcome(werr error, bridgeExit <-chan error, killServer func(), out io.Writer, grace time.Duration) error {
	var berr error
	got := false
	select {
	case berr = <-bridgeExit:
		got = true
	default:
		if werr != nil {
			select {
			case berr = <-bridgeExit:
				got = true
			case <-time.After(grace):
			}
		}
	}
	killServer() // flush the app-server death-noise BEFORE the cause line
	if !got {
		return werr // clean quit, or an abnormal TUI exit with no bridge cause
	}
	if berr != nil {
		fmt.Fprintf(out, "cbus: codex-bridge failed, TUI torn down: %v\n", berr)
		return fmt.Errorf("codex-bridge failed: %w", berr)
	}
	fmt.Fprintln(out, "cbus: codex-bridge exited (listener dormant), TUI torn down")
	return fmt.Errorf("codex-bridge exited before the TUI")
}
