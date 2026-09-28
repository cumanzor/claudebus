//go:build darwin || linux

package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// nativeDisconnect is the daemon's disconnect, fenced to one exact registration.
// It returns the connection ID the daemon disconnected. (A var only for tests.)
var nativeDisconnect = func(target string, f disconnectFence) (string, error) {
	var out struct {
		ConnectionID string `json:"connectionId"`
	}
	err := DaemonCall("POST", "/disconnect", struct {
		Target string `json:"target"`
		disconnectFence
	}{target, f}, &out)
	return out.ConnectionID, err
}

// closeObserveCodex is the fresh Codex consumer probe, nativeTermGrace the wait
// after TERM, inspectIncarnation every liveness check and preSignalCheck the one
// made right before each signal. (Vars only for tests.)
var (
	closeObserveCodex  = observeCodexConsumer
	nativeTermGrace    = 5 * time.Second
	inspectIncarnation = incarnationGone
	preSignalCheck     = incarnationGone
	nativeTTYOf        = ttyOf
)

// nativeDaemonFences proves the running daemon checks a disconnect fence. An
// older daemon would disconnect unfenced and not say so. (A var only for tests.)
var nativeDaemonFences = func() error {
	var health struct {
		FencedDisconnect bool `json:"fencedDisconnect"`
	}
	if err := DaemonCall("GET", "/health", nil, &health); err != nil {
		return err
	}
	if !health.FencedDisconnect {
		return errors.New("the running cbus daemon predates fenced disconnects; run cbus daemon restart to load this version, then close again")
	}
	return nil
}

// nativeConsumer is one process incarnation that consumes a native inbox.
type nativeConsumer struct {
	pid   int
	start string
}

type nativeRefusal struct{ kind, detail string }

func (r *nativeRefusal) Error() string { return r.kind + ": " + r.detail }

func refuseNative(kind, format string, args ...any) error {
	return &nativeRefusal{kind, fmt.Sprintf(format, args...)}
}

var errConsumerGone = errors.New("consumer already gone")

// closeNativePeer ends the consumer bound to a daemon-managed peer. Order:
// resolve and validate the consumer, disconnect exactly this registration so the
// daemon stops injecting into a dying session, revalidate the same incarnation,
// then signal it. Checks and signal are separate system calls, so a process that
// exits and is replaced in between cannot be excluded absolutely; every check is
// repeated against the recorded start token right before each signal.
func closeNativePeer(ch, alias string, m PeerMeta, force bool) CloseReport {
	target := ch + "/" + alias
	c, err := readManagedJournal(ch, alias, m)
	if err != nil {
		return nativeRefusalReport(target, refuseNative("cannot inspect", "%v", err))
	}
	daemonPid := m.ListenerPid
	consumer, err := resolveNativeConsumer(&c, daemonPid)
	gone := errors.Is(err, errConsumerGone)
	if err != nil && !gone {
		return nativeRefusalReport(target, err)
	}
	if err := nativeDaemonFences(); err != nil {
		return nativeRefusalReport(target, refuseNative("cannot inspect", "%v", err))
	}
	pid, start := pinnedConsumer(&c)
	id, err := nativeDisconnect(target, disconnectFence{c.ID, c.ThreadID, pid, start})
	if err != nil {
		return CloseReport{target, false, "disconnect failed, no signal sent: " + err.Error()}
	}
	if id != c.ID {
		return CloseReport{target, false, "the daemon did not honour the disconnect fence; the connection may have been disconnected; no signal sent"}
	}
	if gone {
		return CloseReport{target, true, "already gone; connection disconnected, inbox retained"}
	}
	if err := revalidateNativeConsumer(&c, consumer, daemonPid); err != nil {
		if errors.Is(err, errConsumerGone) {
			return CloseReport{target, true, "already gone; connection disconnected, inbox retained"}
		}
		return CloseReport{target, false, "disconnected, no signal sent: " + err.Error()}
	}
	tty := nativeTTYOf(consumer.pid)
	if daemonPid > 0 && tty != "" && tty == ttyOf(daemonPid) {
		tty = "" // never sweep the daemon's terminal
	}
	if gone, err := preSignalCheck(consumer); err != nil {
		return CloseReport{target, false, "disconnected, no signal sent: cannot inspect: " + err.Error()}
	} else if gone {
		return CloseReport{target, true, "already gone; connection disconnected, inbox retained"}
	}
	if err := signalProcess(consumer.pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return CloseReport{target, true, "already gone; connection disconnected, inbox retained"}
		}
		return CloseReport{target, false, fmt.Sprintf("disconnected; SIGTERM pid %d failed: %v", consumer.pid, err)}
	}
	unconfirmed := func(sig string, err error) CloseReport {
		return CloseReport{target, false, fmt.Sprintf("disconnected; %s sent to pid %d, outcome unconfirmed: cannot inspect: %v", sig, consumer.pid, err)}
	}
	ended, err := waitIncarnationGone(consumer, nativeTermGrace)
	if err != nil {
		return unconfirmed("SIGTERM", err)
	}
	if !ended {
		if !force {
			return CloseReport{target, false, fmt.Sprintf("disconnected; pid %d still running after TERM; use --force", consumer.pid)}
		}
		// a changed start token is never permission to signal the pid's new occupant
		if gone, err := preSignalCheck(consumer); err != nil {
			return unconfirmed("SIGTERM", err)
		} else if gone {
			return CloseReport{target, true, fmt.Sprintf("disconnected; pid %d is no longer the consumer that got TERM; not killed", consumer.pid)}
		}
		_ = signalProcess(consumer.pid, syscall.SIGKILL)
		if gone, err := waitIncarnationGone(consumer, 2*time.Second); err != nil {
			return unconfirmed("SIGKILL", err)
		} else if !gone {
			return CloseReport{target, false, fmt.Sprintf("disconnected; pid %d survived SIGKILL", consumer.pid)}
		}
	}
	return CloseReport{target, true, "process ended; connection disconnected, inbox retained; " + sweepNativeSurface(tty)}
}

func nativeRefusalReport(target string, err error) CloseReport {
	var r *nativeRefusal
	if errors.As(err, &r) {
		return CloseReport{target, false, fmt.Sprintf("%s: %s; refusing to signal. Inspect with cbus connection status %s --json; cbus connection disconnect %s stops delivery without signalling", r.kind, r.detail, target, target)}
	}
	return nativeRefusalReport(target, refuseNative("cannot inspect", "%v", err))
}

// resolveNativeConsumer finds the process authorized to be signalled: the pinned
// incarnation. Claude's is the bound endpoint, still running this exact session.
// Codex's is the journaled consumer, and only if a fresh probe finds that same
// incarnation as the unique writer of this thread's rollout and queue.
func resolveNativeConsumer(c *ConnectionState, daemonPid int) (nativeConsumer, error) {
	pid, start := pinnedConsumer(c)
	consumer := nativeConsumer{pid, start}
	if pid <= 1 || start == "" {
		return consumer, refuseNative("cannot inspect", "no recorded consumer process")
	}
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
	case daemonHarnessCodex:
	default:
		return consumer, refuseNative("cannot inspect", "unsupported harness %q", c.Harness)
	}
	return consumer, revalidateNativeConsumer(c, consumer, daemonPid)
}

// revalidateNativeConsumer repeats every identity check against one incarnation.
func revalidateNativeConsumer(c *ConnectionState, consumer nativeConsumer, daemonPid int) error {
	if consumer.pid <= 1 || consumer.start == "" {
		return refuseNative("cannot inspect", "no recorded consumer process")
	}
	if consumer.pid == daemonPid {
		return refuseNative("shared daemon", "pid %d is the cbus daemon", consumer.pid)
	}
	ancestor, err := ownAncestor(consumer.pid)
	if err != nil {
		return refuseNative("cannot inspect", "%v", err)
	}
	if consumer.pid == os.Getpid() || ancestor {
		return refuseNative("this session", "pid %d is this process or one of its ancestors", consumer.pid)
	}
	if gone, err := inspectIncarnation(consumer); err != nil {
		return refuseNative("cannot inspect", "%v", err)
	} else if gone {
		return errConsumerGone
	}
	argv, err := procArgs(consumer.pid)
	if err != nil {
		return refuseNative("cannot inspect", "argv of pid %d: %v", consumer.pid, err)
	}
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		if !strings.Contains(argv, "claude") {
			return refuseNative("changed incarnation", "pid %d does not look like a claude session", consumer.pid)
		}
		if err := validateCurrentClaudeSession(c.Claude.Binding); err != nil {
			return refuseNative("different loaded session", "pid %d is not running session %s: %v", consumer.pid, c.ThreadID, err)
		}
	case daemonHarnessCodex:
		// the same process can load another thread, so the exact-thread writer
		// probe is repeated on every revalidation, not only before the disconnect
		if err := observedCodexMatches(c, consumer); err != nil {
			return err
		}
		comm, _, err := procParent(consumer.pid)
		if err != nil {
			return refuseNative("cannot inspect", "pid %d: %v", consumer.pid, err)
		}
		if !strings.EqualFold(commBase(comm), "codex") || !interactiveCodexProcess(argv) || codexDesktopAncestor(consumer.pid, procLookup()) {
			return refuseNative("changed incarnation", "pid %d is not an interactive codex CLI", consumer.pid)
		}
	}
	if gone, err := inspectIncarnation(consumer); err != nil {
		return refuseNative("cannot inspect", "%v", err)
	} else if gone {
		return errConsumerGone
	}
	return nil
}

// incarnationGone is proof of exit only: no such process, a zombie, or a
// different start token. Any other inspection failure is an error, never gone.
func incarnationGone(consumer nativeConsumer) (bool, error) {
	return codexOwnerExited(consumer.pid, consumer.start)
}

func waitIncarnationGone(consumer nativeConsumer, grace time.Duration) (bool, error) {
	deadline := time.Now().Add(grace)
	for {
		gone, err := inspectIncarnation(consumer)
		if err == nil && gone {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ancestryParent reads one step of this process's ancestry. (A var only for tests.)
var ancestryParent = procParent

// ownAncestor reports whether pid is above this process. It fails closed: a
// walk that cannot reach init is an error, never "not an ancestor".
func ownAncestor(pid int) (bool, error) {
	for p, i := os.Getppid(), 0; p > 1; i++ {
		if p == pid {
			return true, nil
		}
		if i >= 256 {
			return false, errors.New("this process's ancestry is deeper than expected")
		}
		_, parent, err := ancestryParent(p)
		if err != nil {
			return false, fmt.Errorf("read this process's ancestry at pid %d: %w", p, err)
		}
		p = parent
	}
	return false, nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// observedCodexMatches requires a fresh probe to find the pinned incarnation as
// the unique writer of this thread's rollout and queue.
func observedCodexMatches(c *ConnectionState, consumer nativeConsumer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := closeObserveCodex(ctx, c)
	if err != nil {
		return refuseNative("cannot inspect", "%v", err)
	}
	switch {
	case p.State == "exited":
		return errConsumerGone
	case p.State != "online":
		return refuseNative("cannot inspect", "%s", orDefault(p.Detail, "no unique Codex consumer observed"))
	case p.PID != consumer.pid || p.StartToken != consumer.start:
		return refuseNative("changed incarnation", "the observed Codex consumer is pid %d, not the pinned pid %d", p.PID, consumer.pid)
	}
	return nil
}

// nativeTTYProbe lists the processes on tty. (A var only for tests.)
var nativeTTYProbe = func(ctx context.Context, tty string) (stdout, stderr string, exitCode int, err error) {
	var out, errOut strings.Builder
	cmd := boundedCmd(ctx, "ps", "-t", tty, "-o", "pid=")
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), errOut.String(), exit.ExitCode(), nil
	}
	return out.String(), errOut.String(), 0, err
}

// sweepNativeSurface closes the consumer's surface only on positive proof that
// its tty is idle: ps ran and found no process on it, or the device is gone.
// Any failure to ask leaves the surface open.
func sweepNativeSurface(tty string) string {
	if tty == "" {
		return "surface unknown (no tty)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), surfaceSweepBudget)
	defer cancel()
	stdout, stderr, code, err := nativeTTYProbe(ctx, tty)
	if !ttyProvenIdle(stdout, stderr, code, err) {
		if err == nil && code == 0 && strings.TrimSpace(stdout) != "" {
			return "tty busy, surface left alone"
		}
		return "surface left open (could not confirm idle)"
	}
	return closeSurface(ctx, tty)
}

func ttyProvenIdle(stdout, stderr string, code int, err error) bool {
	if err != nil || strings.TrimSpace(stdout) != "" {
		return false
	}
	stderr = strings.TrimSpace(stderr)
	return code == 0 || (code == 1 && (stderr == "" || strings.HasSuffix(stderr, "No such file or directory")))
}
