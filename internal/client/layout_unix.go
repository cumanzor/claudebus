//go:build darwin || linux

package client

// The layout runtime is tmux/tty only: defaultTmuxRun execs tmux, and the pane
// resolvers reach a peer's controlling tty (ttyOf, close_unix.go). None of it exists on
// windows, where layout is a phase-1-excluded verb, so it lives here. The parser, planner,
// pane-table and the tmuxRun seam variable stay platform-neutral in layout.go; the windows
// build gets a loud defaultTmuxRun in layout_windows.go so the seam still resolves.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func defaultTmuxRun(argv []string) ([]byte, error) {
	return exec.Command("tmux", argv...).CombinedOutput()
}

// TmuxPanesByTTY maps /dev/ttysNNN → %N for every pane on the running server in ONE
// call, so a whole channel resolves without one tmux invocation per peer. It is the
// same tty→pane join the close sweep does per peer, hoisted.
func TmuxPanesByTTY() (map[string]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_tty} #{pane_id}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %v%s", err, cmdStderr(err))
	}
	return parsePaneTable(string(out), 1), nil
}

// PeerPane resolves ch/alias to the tmux pane the peer runs in, entirely live: meta
// → owning pid → controlling tty → pane. The owner fallback and its identity test
// are ClosePeer's, for the same reason — a recycled listener pid must not donate a
// stranger's process, and here that would drag an unrelated session's pane into
// someone's layout.
func PeerPane(ch, alias string, byTTY map[string]string) (string, error) {
	// the caller's own pane resolves here, not in one wrapper, so arrange, scatter and
	// focus all get it. Putting it in ResolvePeerPanes gave it to arrange alone, and
	// scatter then reported the very session running it as unlocatable and skipped it.
	if pane := selfPane(ch, alias, byTTY); pane != "" {
		return pane, nil
	}
	metaPath := filepath.Join(CBUSDir(), ch, alias, "meta.json")
	m, ok := ReadPeerMeta(metaPath)
	if !ok {
		return "", fmt.Errorf("no peer %s/%s", ch, alias)
	}
	pid := m.OwnerPid
	if pid == 0 && m.ListenerPid > 0 && pidAlive(m.ListenerPid) && listenerIdentityHolds(m, metaPath) {
		pid, _ = ownerFromPid(m.ListenerPid)
	}
	if pid == 0 {
		// BOTH pid fields are null in a fresh join and only stamped when the listener
		// arms, so a pidless meta means "never armed", not "dead". Reporting a live
		// session as not running sends the user hunting a process that is fine.
		if m.ListenerPid == 0 {
			return "", fmt.Errorf("%s has no recorded pid: it joined but never armed a listener, so there is nothing to locate it by (arm its Monitor, or run arrange from that session)", alias)
		}
		return "", fmt.Errorf("%s: cannot resolve the process owning listener pid %d", alias, m.ListenerPid)
	}
	if !pidAlive(pid) || procZombie(pid) {
		return "", fmt.Errorf("%s is not running", alias)
	}
	tty := ttyOf(pid)
	if tty == "" {
		return "", fmt.Errorf("%s has no controlling tty — nothing to arrange", alias)
	}
	pane, ok := byTTY["/dev/"+tty]
	if !ok {
		return "", fmt.Errorf("%s is not in a tmux pane (tty %s) — layout is tmux-only", alias, tty)
	}
	return pane, nil
}

// ResolvePeerPanes resolves every alias in one pass, reporting ALL failures rather
// than the first: told "coder is not running" a user fixes coder and reruns, only to
// learn reviewer is in iTerm2. One message, one round trip.
func ResolvePeerPanes(ch string, aliases []string) (map[string]string, error) {
	byTTY, err := TmuxPanesByTTY()
	if err != nil {
		return nil, err
	}
	panes := make(map[string]string, len(aliases))
	var bad []string
	for _, a := range aliases {
		pane, err := PeerPane(ch, a, byTTY)
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		panes[a] = pane
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return panes, nil
}

// TmuxPaneWindows maps %N → @M for every pane on the server. scatter needs it to
// tell a pane that shares a window (breakable) from one that is already alone in
// its own window, which break-pane refuses — checking beforehand turns that refusal
// into an accurate "already its own window" instead of an error the user must read
// past.
func TmuxPaneWindows() (map[string]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{window_id}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %v%s", err, cmdStderr(err))
	}
	return parsePaneTable(string(out), 0), nil
}
