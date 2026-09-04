package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"claudebus/internal/client"
)

const (
	arrangeUsage = "usage: cbus arrange <spec> [--channel <ch>] [--dry-run]   (spec: 'orchestrator | (coder / reviewer)')"
	scatterUsage = "usage: cbus scatter [channel] [--dry-run]"
	focusUsage   = "usage: cbus focus <channel>/<alias>"
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

// parseLayoutArgs reads the one-positional + --channel/--dry-run shape both arrange
// and scatter take. rc is -1 to continue, else the exit code to return: the flag
// scanner in flags.go stops at the first positional, which would swallow a trailing
// --dry-run as the channel. Unknown flags are strict — neither verb has free text to
// protect, so a typo'd --dry-runn must die rather than be silently ignored.
func parseLayoutArgs(args []string, usage string, wantPositional bool) (positional, channel string, dry bool, rc int) {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dry-run":
			dry = true
		case a == "--channel":
			if i+1 >= len(args) {
				return "", "", false, die("--channel needs a channel name")
			}
			i++
			channel = args[i]
		case strings.HasPrefix(a, "--channel="):
			channel = strings.TrimPrefix(a, "--channel=")
		case strings.HasPrefix(a, "-"):
			return "", "", false, die("unknown flag %s", a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) > 1 {
		return "", "", false, die("%s", usage)
	}
	if wantPositional {
		if len(pos) == 0 {
			return "", "", false, die("%s", usage)
		}
		positional = pos[0]
	} else if len(pos) == 1 {
		// scatter's lone positional IS the channel; --channel is accepted too so the
		// two verbs take the same flags.
		if channel != "" && channel != pos[0] {
			return "", "", false, die("channel given twice (%s and %s)", pos[0], channel)
		}
		channel = pos[0]
	}
	return positional, channel, dry, -1
}

// layoutChannel resolves which channel the verb acts on: an explicit name wins;
// otherwise the channel holding hint (arrange's first alias), otherwise this
// session's own registration. An ambiguous session — joined to several channels with
// no hint — is an error, because guessing which formation to rearrange is a guess
// the user watches happen to the wrong window.
func layoutChannel(explicit, hint string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if hint != "" {
		if ch, ok := client.FindPeerChannel(hint); ok {
			return ch, nil
		}
		return "", fmt.Errorf("no peer %q in your channels — pass --channel <ch> (cbus list)", hint)
	}
	regs := client.ResolveSelf()
	switch len(regs) {
	case 0:
		return "", fmt.Errorf("this session has joined no channel — pass a channel name")
	case 1:
		return regs[0].Channel, nil
	}
	var names []string
	for _, r := range regs {
		names = append(names, r.Channel)
	}
	return "", fmt.Errorf("this session is in several channels (%s) — name the one to act on", strings.Join(names, ", "))
}

func printOps(ops []client.LayoutOp) {
	for _, op := range ops {
		fmt.Printf("tmux %s\n", strings.Join(op.Argv, " "))
	}
}
