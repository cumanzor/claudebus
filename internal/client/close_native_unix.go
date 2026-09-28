//go:build darwin || linux

package client

import (
	"context"
	"errors"
	"fmt"
	"os"
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
// after TERM and stillPinned the check before each signal. (Vars only for tests.)
var (
	closeObserveCodex = observeCodexConsumer
	nativeTermGrace   = 5 * time.Second
	stillPinned       = incarnationLive
)

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
	pid, start := pinnedConsumer(&c)
	id, err := nativeDisconnect(target, disconnectFence{c.ID, c.ThreadID, pid, start})
	if err != nil || id != c.ID {
		if err == nil {
			err = errors.New("the daemon did not confirm this exact registration")
		}
		return CloseReport{target, false, "disconnect failed, no signal sent: " + err.Error()}
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
	tty := ttyOf(consumer.pid)
	if daemonPid > 0 && tty != "" && tty == ttyOf(daemonPid) {
		tty = "" // never sweep the daemon's terminal
	}
	if !stillPinned(consumer) {
		return CloseReport{target, true, "already gone; connection disconnected, inbox retained"}
	}
	if err := syscall.Kill(consumer.pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return CloseReport{target, true, "already gone; connection disconnected, inbox retained"}
		}
		return CloseReport{target, false, fmt.Sprintf("disconnected; SIGTERM pid %d failed: %v", consumer.pid, err)}
	}
	if !waitIncarnationGone(consumer, nativeTermGrace) {
		if !force {
			return CloseReport{target, false, fmt.Sprintf("disconnected; pid %d still running after TERM; use --force", consumer.pid)}
		}
		// a changed start token is never permission to signal the pid's new occupant
		if !stillPinned(consumer) {
			return CloseReport{target, true, fmt.Sprintf("disconnected; pid %d is no longer the consumer that got TERM; not killed", consumer.pid)}
		}
		_ = syscall.Kill(consumer.pid, syscall.SIGKILL)
		if !waitIncarnationGone(consumer, 2*time.Second) {
			return CloseReport{target, false, fmt.Sprintf("disconnected; pid %d survived SIGKILL", consumer.pid)}
		}
	}
	return CloseReport{target, true, "process ended; connection disconnected, inbox retained; " + sweepSurface(tty)}
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p, err := closeObserveCodex(ctx, c)
		if err != nil {
			return consumer, refuseNative("cannot inspect", "%v", err)
		}
		switch {
		case p.State == "exited":
			return consumer, errConsumerGone
		case p.State != "online":
			return consumer, refuseNative("cannot inspect", "%s", orDefault(p.Detail, "no unique Codex consumer observed"))
		case p.PID != pid || p.StartToken != start:
			return consumer, refuseNative("changed incarnation", "the observed Codex consumer is pid %d, not the pinned pid %d", p.PID, pid)
		}
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
	if consumer.pid == os.Getpid() || ownAncestor(consumer.pid) {
		return refuseNative("this session", "pid %d is this process or one of its ancestors", consumer.pid)
	}
	exited, err := codexOwnerExited(consumer.pid, consumer.start)
	if err != nil {
		return refuseNative("cannot inspect", "%v", err)
	}
	if exited {
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
		comm, _, err := procParent(consumer.pid)
		if err != nil {
			return refuseNative("cannot inspect", "pid %d: %v", consumer.pid, err)
		}
		if !strings.EqualFold(commBase(comm), "codex") || !interactiveCodexProcess(argv) || codexDesktopAncestor(consumer.pid, procLookup()) {
			return refuseNative("changed incarnation", "pid %d is not an interactive codex CLI", consumer.pid)
		}
	}
	if !incarnationLive(consumer) {
		return errConsumerGone
	}
	return nil
}

func incarnationLive(consumer nativeConsumer) bool {
	exited, err := codexOwnerExited(consumer.pid, consumer.start)
	return err == nil && !exited
}

func waitIncarnationGone(consumer nativeConsumer, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for {
		exited, err := codexOwnerExited(consumer.pid, consumer.start)
		if err == nil && exited {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func ownAncestor(pid int) bool {
	for p, i := os.Getppid(), 0; p > 1 && i < 256; i++ {
		if p == pid {
			return true
		}
		_, parent, err := procParent(p)
		if err != nil {
			return false
		}
		p = parent
	}
	return false
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
