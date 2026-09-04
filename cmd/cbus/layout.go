package main

import (
	"fmt"
	"strings"

	"claudebus/internal/client"
)

const (
	arrangeUsage = "usage: cbus arrange <spec> [--channel <ch>] [--dry-run]   (spec: 'orchestrator | (coder / reviewer)')"
	scatterUsage = "usage: cbus scatter [channel] [--dry-run]"
	focusUsage   = "usage: cbus focus <channel>/<alias>"
)

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
