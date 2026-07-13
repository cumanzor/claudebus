// Command cbus is the Go port of the bash cbus client. During the transition it
// installs as `cbus-go` alongside the bash `cbus`, sharing the same $CBUS_DIR and
// credential store (the A3/A6 coexistence contract); at the Phase 2 cutover it
// replaces `cbus`. This is the P1 skeleton: verb dispatch is in place and the
// read-only / remote verbs land milestone by milestone.
package main

import (
	"fmt"
	"os"
)

const usage = `cbus-go — message bus between live Claude Code sessions (transitional Go port)

During P1, cbus-go is installed as cbus-go alongside the bash cbus and shares its
$CBUS_DIR and credential store. Verbs are ported incrementally; unimplemented
verbs print a notice and exit non-zero. Use the bash cbus for anything not yet
ported.

  cbus-go --help                   this message

env: CBUS_DIR (default ~/.claude-bus), CBUS_SITE_<HOST>_URL / CBUS_RELAY_LOCAL_URL
     (relay endpoints)
`

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	switch verb {
	case "", "-h", "--help":
		fmt.Print(usage)
		return 0

	// P1 verbs — landing milestone by milestone (auth, remote send/list/tail,
	// whoami, inbox, channels, read-only list).
	case "auth", "send", "tail", "list", "peers", "active", "channels", "whoami", "inbox":
		return notImplemented(verb)

	// Phase 2 (local transport + harness) — not in the Go client during P1.
	case "join", "register", "prune", "leave", "hook-exit", "unregister", "rename", "bootstrap", "branch":
		return notImplemented(verb)

	default:
		fmt.Fprintf(os.Stderr, "cbus: unknown command %q (cbus-go --help)\n", verb)
		return 1
	}
}

func notImplemented(verb string) int {
	fmt.Fprintf(os.Stderr, "cbus-go: %q not implemented yet (P1 in progress; use the bash cbus)\n", verb)
	return 1
}
