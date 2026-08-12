//go:build darwin || linux

package client

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// surfaceSweepBudget bounds the whole surface sweep. Cleanup is best-effort by
// design, and an unbounded osascript can wedge for tens of seconds on a busy Apple
// Event queue — the close itself has already succeeded by then, so the sweep gets a
// deadline rather than the caller getting a stall.
// (a var, not a const, only so the timeout path is testable in milliseconds.)
var surfaceSweepBudget = 5 * time.Second

// ClosePeer tears down the LOCAL peer ch/alias on instruction: SIGTERM its owning
// claude process (graceful — the SessionEnd hook broadcasts 'left' and removes the
// registration), wait out a ≤5s grace, then sweep the terminal surface the peer
// lived in (tmux pane or iTerm2 pane/tab/window alike), located by the tty
// captured BEFORE the signal — a reaped pid has no tty left to read, so reading
// it after would lose the locator exactly when the graceful path worked.
//
// Refusals: this session itself (a close is not how you exit), and a pid whose
// argv no longer looks like a claude session (pid recycling — signalling a
// stranger is worse than failing). Without force a TERM-surviving process is
// reported, not escalated: closing its surface would be a disguised kill.
// Registrations are NEVER touched here — the SessionEnd hook handles the graceful
// path and the lazy-prune backstop the rest.
func ClosePeer(ch, alias string, force bool) CloseReport {
	target := ch + "/" + alias
	metaPath := filepath.Join(CBUSDir(), ch, alias, "meta.json")
	m, ok := ReadPeerMeta(metaPath)
	if !ok {
		return CloseReport{target, false, "no such peer"}
	}
	if sid := SessionID(); sid != "" && m.SessionID == sid {
		return CloseReport{target, false, "that peer is THIS session — refusing (exit it normally)"}
	}
	pid := m.OwnerPid
	if pid == 0 {
		// pre-fix registrations recorded ownerPid null (the comm-vs-version-string
		// walk, see ownerFromPid) — derive the owner NOW from the armed listener's
		// ancestry rather than false-succeeding on a live peer. The listener must
		// still be THIS peer's follower (the SAME identity test MetaListenerAlive
		// applies, via listenerIdentityHolds): a
		// recycled listenerPid that now belongs to a process under a DIFFERENT claude
		// session would otherwise donate that session's pid to the TERM below, killing
		// a window nobody asked to close. This is the highest-consequence caller of
		// that test — a wrong answer here signals a stranger's session.
		if m.ListenerPid > 0 && pidAlive(m.ListenerPid) && listenerIdentityHolds(m, metaPath) {
			pid, _ = ownerFromPid(m.ListenerPid)
		}
	}
	if pid == 0 || !pidAlive(pid) || procZombie(pid) {
		return CloseReport{target, true, "already gone (no live process; the sweep owns the registration)"}
	}
	if argv, err := procArgs(pid); err != nil || !strings.Contains(argv, "claude") {
		return CloseReport{target, false, fmt.Sprintf("pid %d does not look like a claude session (pid recycled?) — refusing to signal", pid)}
	}
	tty := ttyOf(pid)
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			// died between the argv check and the signal — a teardown that finds
			// nothing to tear down succeeded (idempotent sweeps).
			return CloseReport{target, true, "already gone (exited before the signal)"}
		}
		return CloseReport{target, false, fmt.Sprintf("SIGTERM pid %d: %v", pid, err)}
	}
	if !waitGone(pid, 5*time.Second) {
		if !force {
			return CloseReport{target, false, fmt.Sprintf("pid %d still running after TERM; use --force", pid)}
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
		if !waitGone(pid, 2*time.Second) {
			return CloseReport{target, false, fmt.Sprintf("pid %d survived SIGKILL", pid)}
		}
	}
	return CloseReport{target, true, "process ended; " + sweepSurface(tty)}
}

// ttyOf reads the controlling tty ("ttys012") of pid; "" when detached ("??") or
// unreadable. Callers capture it Pre-signal.
func ttyOf(pid int) string {
	out, err := exec.Command("ps", "-o", "tty=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || strings.HasPrefix(tty, "?") {
		return ""
	}
	return tty
}

// waitGone polls pid liveness until it exits or the grace lapses.
func waitGone(pid int, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) || procZombie(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !pidAlive(pid) || procZombie(pid)
}

// sweepSurface closes the terminal surface on tty if — and only if — the tty is
// DEAD. Any live pid on it means either a TERM-survivor (caller already reported
// it) or a recycled tty hosting a stranger; both must be left alone. The common
// graceful case is a pane iTerm2 already auto-closed, which reports as
// "surface already closed". Returns a human detail fragment, never an error —
// surface cleanup is best-effort by design.
func sweepSurface(tty string) string {
	if tty == "" {
		return "surface unknown (no tty)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), surfaceSweepBudget)
	defer cancel()

	// a NONZERO ps here is the normal case, not a failure: a dead tty makes ps exit 1
	// ("No such file or directory"), which is precisely the leftover surface we sweep.
	// Only a TIMEOUT is disqualifying, and it is caught once at the end rather than
	// after each step — an expired context fails every later command immediately, so
	// control always reaches the check without closing anything on the way.
	out, err := boundedCmd(ctx, "ps", "-t", tty, "-o", "pid=").Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return "tty busy — surface left alone"
	}
	dev := "/dev/" + tty
	if out, err := boundedCmd(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_tty}").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[1] == dev && validTmuxPaneID(f[0]) {
				if boundedCmd(ctx, "tmux", "kill-pane", "-t", f[0]).Run() == nil {
					return "tmux pane closed"
				}
			}
		}
	}
	if out, err := runOsascriptOutCtx(ctx, closeSurfaceScript(dev)); err == nil {
		if strings.TrimSpace(out) == "closed" {
			return "iTerm2 surface closed"
		}
	}
	if ctx.Err() != nil {
		return "surface sweep timed out — left alone"
	}
	return "surface already closed"
}

// closeSurfaceScript closes the iTerm2 session whose tty is dev. Same triple-loop
// rationale as findSessionScript; tty is matched exactly, and the caller has
// already proven the tty dead, so this can only close an inert leftover surface.
func closeSurfaceScript(dev string) string {
	return "tell application \"iTerm2\"\n" +
		"  repeat with w in windows\n" +
		"    repeat with t in tabs of w\n" +
		"      repeat with s in sessions of t\n" +
		"        if tty of s is " + appleScriptStr(dev) + " then\n" +
		"          close s\n" +
		"          return \"closed\"\n" +
		"        end if\n" +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		"  return \"none\"\n" +
		"end tell"
}
