package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// 1. per-peer app-server in its own process group, so teardown takes the native child.
	srv := exec.Command("codex", "app-server", "--listen", "unix://"+sock)
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start codex app-server: %w", err)
	}
	defer func() {
		_ = syscall.Kill(-srv.Process.Pid, syscall.SIGTERM)
		_ = os.Remove(sock)
	}()
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

	// 3. the TUI, attached to the app-server; it takes over the terminal. No hook, no CBUS env.
	tui := exec.Command("codex", codexRemoteArgs(sock, passthrough)...)
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

	// 5. bridge the alias inbox into the TUI thread. RunCodexBridge blocks in the follower for
	//    the wrapper's lifetime; if it ever returns (startup failure OR the listener going
	//    dormant) while the TUI still lives, the peer is deaf. Fail the WHOLE unit: kill the TUI
	//    so the human sees the session end instead of a healthy-looking window with no delivery
	//    (the session is recoverable via codex resume). The bridge cause is signalled before the
	//    kill and printed to the real stderr after the alternate screen tears down.
	bridgeExit := make(chan error, 1)
	go func() {
		berr := RunCodexBridge(channel+"/"+alias, sock, threadID)
		bridgeExit <- berr
		_ = tui.Process.Kill()
	}()

	// 6. the human drives the TUI; teardown (deferred) runs when it exits.
	werr := tui.Wait()
	select {
	case berr := <-bridgeExit:
		if berr != nil {
			fmt.Fprintf(os.Stderr, "cbus: codex-bridge failed, TUI torn down: %v\n", berr)
			return fmt.Errorf("codex-bridge failed: %w", berr)
		}
		fmt.Fprintln(os.Stderr, "cbus: codex-bridge exited (listener dormant), TUI torn down")
		return fmt.Errorf("codex-bridge exited before the TUI")
	default:
		return werr // normal TUI exit (the human quit)
	}
}
