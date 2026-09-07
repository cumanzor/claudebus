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
	"time"
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
	// resumeWait is the picker's window: `codex resume` with no id puts a HUMAN in front of a
	// session list, so the thread is not named until they choose. A TUI that dies first ends
	// the wait early (tuiExit), so the long timer only ever costs a real deliberation.
	resumeWait = 5 * time.Minute
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

// threadNoteID pulls a thread id out of any notification that names one. thread/started nests
// it under "thread"; the notifications a RESUMED thread produces instead — thread/status/changed
// and thread/goal/cleared, both measured on codex-cli 0.153.4 — carry a flat threadId.
func threadNoteID(params json.RawMessage) string {
	var p struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(params, &p)
	if p.ThreadID != "" {
		return p.ThreadID
	}
	return p.Thread.ID
}

// uuidLike reports whether s has the 8-4-4-4-12 hex shape of a codex session id. It is the
// guard on reading a thread id out of the passthrough: a `resume` arg that is not a session id
// is a session NAME (codex resolves those itself), and adopting one as a thread id would join
// the bus under something the app-server never heard of.
func uuidLike(s string) bool {
	groups := []int{8, 4, 4, 4, 12}
	parts := strings.Split(s, "-")
	if len(parts) != len(groups) {
		return false
	}
	for i, want := range groups {
		if len(parts[i]) != want {
			return false
		}
		for _, c := range parts[i] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// resumeLaunch reports whether the passthrough runs codex's `resume` subcommand, and returns
// the session id when the args already name one (`resume <uuid>`, the id form). A resumed
// thread never emits thread/started, so this is what tells the wrapper to rendezvous
// differently; the id, when present, means no rendezvous is needed at all.
//
// The scan is for a bare `resume` token anywhere in the passthrough rather than at position 0,
// so global options in front of the subcommand (`cbus codex -c model=o3 resume ...`) still
// read as a resume. A codex PROMPT of exactly "resume" would also match, which costs nothing:
// `codex resume` with no id is the picker either way.
func resumeLaunch(passthrough []string) (resume bool, sid string) {
	for i, a := range passthrough {
		if a != "resume" {
			continue
		}
		for _, rest := range passthrough[i+1:] {
			if uuidLike(rest) {
				return true, rest
			}
		}
		return true, ""
	}
	return false, ""
}

// rendezvous is how the wrapper expects to meet the thread its TUI is driving.
//
//   - fresh launch: wait for thread/started and hard-check its cwd (unchanged behaviour).
//   - resume: accept ANY notification that names a threadId. A resumed thread is announced by
//     thread/status/changed, not thread/started, and a fresh launch emits nothing else in the
//     same window (probed), so the wider acceptance cannot loosen the fresh path's cwd check.
//
// The wait is ORDERING, not just discovery, which is why a resume waits even when the operator
// already typed the session id. The app-server grants the thread's writer role to ONE
// connection: the TUI must be the one that claims it, or it exits with "already has an active
// writer" and the human loses the window they asked for. Waiting for the server to name the
// thread is what puts the TUI first; wantID then only CHECKS that the thread which showed up is
// the one that was asked for.
//
// cwd is NOT an identity check on the resume path: a resumed session legitimately carries the
// cwd it was recorded in, so a mismatch there is normal rather than a stranger thread. wantID
// is the identity check that replaces it whenever the launch names an id.
type rendezvous struct {
	wantCwd string
	wantID  string
	resume  bool
	timeout time.Duration
}

// accept applies the wantID check to a thread id the server named.
func (rz rendezvous) accept(id string) (string, error) {
	if rz.wantID != "" && id != rz.wantID {
		return "", fmt.Errorf("codex named thread %q but this launch asked for %q — refusing (the bridge would deliver into a thread the TUI is not showing)", id, rz.wantID)
	}
	return id, nil
}

// discoverThread blocks until the app-server names the TUI's thread on the passive connection
// (F1 probe), returning that thread id. The cwd is a HARD check on the fresh path: a per-peer
// app-server must serve exactly this wrapper's TUI, so a thread/started whose cwd disagrees
// with the wrapper's is refused loudly (both paths named) rather than adopted — same loudness
// as the rejected exactly-one-thread contract.
//
// tuiExit ends the wait the moment the TUI is gone, so a dead TUI reports what actually
// happened instead of the operator reading "did not attach" after sitting out the whole timer.
// On expiry it names the likely cause rather than hanging.
func discoverThread(conn *codexConn, rz rendezvous, tuiExit <-chan error) (string, error) {
	timer := time.NewTimer(rz.timeout)
	defer timer.Stop()
	for {
		select {
		case note := <-conn.notifications():
			if note.Method == "thread/started" {
				id, cwd := threadStartedInfo(note.Params)
				if id == "" {
					continue // malformed notification; the timer still bounds the wait
				}
				if !rz.resume && rz.wantCwd != "" && cwd != "" && cwd != rz.wantCwd {
					return "", fmt.Errorf("codex started a thread in %q but this wrapper runs in %q — refusing (a per-peer app-server must serve exactly this TUI)", cwd, rz.wantCwd)
				}
				return rz.accept(id)
			}
			if !rz.resume {
				continue
			}
			if id := threadNoteID(note.Params); id != "" {
				return rz.accept(id)
			}
		case werr := <-tuiExit:
			return "", fmt.Errorf("codex --remote exited before its thread was known (%v): nothing to join", werr)
		case <-timer.C:
			if rz.resume {
				return "", fmt.Errorf("codex --remote never named a resumed thread within %s: the TUI did not reach the app-server, or it is still sitting on the session picker", rz.timeout)
			}
			return "", fmt.Errorf("codex --remote never started a thread within %s: the TUI did not attach to the app-server", rz.timeout)
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
	dev, ino, _, ok := fileIdentity(InboxPath(channel, alias))
	if !ok {
		return
	}
	writeCursor(peerDir, dev, ino, 0)
	if cd, ci, co, state := readCursor(peerDir); state != cursorValid || cd != dev || ci != ino || co != 0 {
		return // seed did not round-trip: skip the claim rather than risk seek-END mail loss
	}
	armMeta(filepath.Join(peerDir, "meta.json"), start)
}

var (
	// bridgeCauseGrace bounds how long the teardown waits for a self-exited TUI's bridge to
	// name a cause (the same app-server reset can down both, bridge a beat behind). It only
	// bounds the WAIT, never suppresses a cause. serverReapWait bounds the SIGTERM reap before
	// escalating to SIGKILL. Vars, not consts, so tests shrink them.
	bridgeCauseGrace = 2 * time.Second
	serverReapWait   = 2 * time.Second
)

// awaitTeardownSignal blocks until a termination signal arrives or the wrapper finishes,
// returning the signal that arrived (nil when the wrapper finished first).
//
// term carries SIGTERM and SIGHUP for the wrapper's whole life: those are how a wrapper is
// killed from outside (`pkill`, a closed window, `tmux kill-session`), and the default action
// for both is immediate death, which skips the deferred app-server teardown and leaves an
// app-server holding the resumed thread's writer lock (measured twice: every later resume of
// that session is then refused).
//
// intr carries SIGINT and is the caller's to STOP once the TUI is up. Before that the wrapper
// owns the terminal and a Ctrl-C is a real signal to catch; after it, Ctrl-C is the TUI's own
// keystroke, read as a byte in raw mode with no signal generated at all (measured: a ^C to a
// live TUI quits it through the normal path, and the wrapper's ordinary teardown runs). Nothing
// catches SIGKILL, so `kill -9` still orphans the app-server.
func awaitTeardownSignal(term, intr <-chan os.Signal, done <-chan struct{}) os.Signal {
	select {
	case s := <-term:
		return s
	case s := <-intr:
		return s
	case <-done:
		return nil
	}
}

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
