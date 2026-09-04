//go:build darwin || linux

package main

// The layout verbs drive tmux (arrange/scatter/focus rearrange live peers across panes)
// and reach a peer's controlling tty, so their bodies are unix-only. The arg parsing,
// usage text, dispatch and printOps stay platform-neutral in layout.go; windows gets
// refusal shims in layout_windows.go so the dispatch still resolves.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"claudebus/internal/client"
)

// runArrange rearranges live peers into the pane tree the spec describes. The
// layout is built in the window of the FIRST alias written — predictable, and in the
// common case (an orchestrator arranging its own formation from its own pane) that
// window is the caller's. Nothing is created and nothing is closed: every pane in
// the spec must already belong to a running peer.
func runArrange(args []string) int {
	spec, channel, dry, rc := parseLayoutArgs(args, arrangeUsage, true)
	if rc >= 0 {
		return rc
	}
	root, err := client.ParseLayout(spec)
	if err != nil {
		return die("%v", err)
	}
	aliases := client.LayoutAliases(root)
	ch, err := layoutChannel(channel, aliases[0])
	if err != nil {
		return die("%v", err)
	}
	panes, err := client.ResolvePeerPanes(ch, aliases)
	if err != nil {
		return die("%v", err)
	}
	ops, err := client.PlanLayout(root, panes)
	if err != nil {
		return die("%v", err)
	}
	if dry {
		printOps(ops)
		return 0
	}
	applied, err := client.RunLayoutOps(ops)
	if err != nil {
		// a half-built tree is on screen and re-running fixes it, but the user has to
		// know it is half-built — the count is the difference between "nothing
		// happened" and "look at your window".
		fmt.Fprintf(os.Stderr, "cbus: %v (applied %d of %d steps)\n", err, applied, len(ops))
		return 1
	}
	fmt.Printf("%s: arranged %d panes in %d steps\n", ch, len(aliases), applied)
	return 0
}

// runScatter is arrange's inverse: every live peer of the channel gets its own
// window back. A peer already alone in its window is reported and left alone rather
// than counted as failure — scatter is idempotent by intent.
func runScatter(args []string) int {
	_, channel, dry, rc := parseLayoutArgs(args, scatterUsage, false)
	if rc >= 0 {
		return rc
	}
	ch, err := layoutChannel(channel, "")
	if err != nil {
		return die("%v", err)
	}
	roster, err := client.ChannelRoster(ch)
	if err != nil {
		return die("%v", err)
	}
	byTTY, err := client.TmuxPanesByTTY()
	if err != nil {
		return die("%v", err)
	}
	windows, err := client.TmuxPaneWindows()
	if err != nil {
		return die("%v", err)
	}
	counts := map[string]int{}
	for _, w := range windows {
		counts[w]++
	}
	broke, resolved := 0, 0
	for _, p := range roster {
		pane, err := client.PeerPane(ch, p.Alias, byTTY)
		if err != nil {
			fmt.Printf("%s: skipped (%v)\n", p.Alias, err)
			continue
		}
		resolved++
		if counts[windows[pane]] < 2 {
			// nothing to break, but the window still gets the peer's name: scatter's
			// contract is one named window per peer, and a result where only the panes
			// that happened to move are labelled is worse than one that is uniform.
			if !dry {
				_ = exec.Command("tmux", "rename-window", "-t", pane, p.Alias).Run()
			}
			fmt.Printf("%s: already its own window\n", p.Alias)
			continue
		}
		argv := []string{"break-pane", "-d", "-s", pane, "-n", p.Alias}
		if dry {
			fmt.Printf("tmux %s\n", strings.Join(argv, " "))
			broke++
			continue
		}
		if out, err := exec.Command("tmux", argv...).CombinedOutput(); err != nil {
			fmt.Printf("%s: %v: %s\n", p.Alias, err, strings.TrimSpace(string(out)))
			continue
		}
		counts[windows[pane]]--
		broke++
		fmt.Printf("%s: broken out\n", p.Alias)
	}
	// success is the desired STATE, not work done: a channel whose peers already each
	// have a window is scattered, and reporting that as failure contradicts the verb's
	// own idempotence. Only "could not see a single peer" is a failure.
	if resolved == 0 {
		return 1
	}
	return 0
}

// runFocus moves the terminal's attention to a peer, whether it currently lives in
// a split or a window of its own — the same pane id answers both, which is exactly
// what iTerm2 cannot do.
func runFocus(args []string) int {
	if len(args) == 0 {
		return die("%s", focusUsage)
	}
	if err := noExtra(args, 1, focusUsage); err != nil {
		return die("%v", err)
	}
	if client.IsRemote(args[0]) {
		return die("focus is local-only — a remote peer lives on its own host's terminal")
	}
	ch, al, err := client.ParseLocal(args[0])
	if err != nil {
		return die("%v", err)
	}
	if ch == "" {
		found, ok := client.FindPeerChannel(al)
		if !ok {
			return die("no peer %q in your channels — use <channel>/<alias> (cbus list)", al)
		}
		ch = found
	}
	byTTY, err := client.TmuxPanesByTTY()
	if err != nil {
		return die("%v", err)
	}
	pane, err := client.PeerPane(ch, al, byTTY)
	if err != nil {
		return die("%v", err)
	}
	// select-window first: selecting a pane in a window that is not current would
	// otherwise leave the user looking at the old window with focus somewhere unseen.
	for _, argv := range [][]string{{"select-window", "-t", pane}, {"select-pane", "-t", pane}} {
		if out, err := exec.Command("tmux", argv...).CombinedOutput(); err != nil {
			return die("tmux %s: %v: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Printf("%s/%s: focused %s\n", ch, al, pane)
	return 0
}
