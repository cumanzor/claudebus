//go:build darwin || linux

package client

import (
	"fmt"
	"os"
	"sync"
	"syscall"

	"claudebus/internal/core"
)

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
