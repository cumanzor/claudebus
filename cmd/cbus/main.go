// Command cbus is the Go port of the bash cbus client. During the transition it
// installs as `cbus-go` alongside the bash `cbus`, sharing the same $CBUS_DIR and
// credential store (the A3/A6 coexistence contract); at the Phase 2 cutover it
// replaces `cbus`. This is the P1 skeleton: verb dispatch is in place and the
// read-only / remote verbs land milestone by milestone.
package main

import (
	"fmt"
	"io"
	"os"

	"claudebus/internal/client"
	"claudebus/internal/core"
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

	case "auth":
		return runAuth(args[1:])

	// P1 verbs — landing milestone by milestone (remote send/list/tail, whoami,
	// inbox, channels, read-only list).
	case "send", "tail", "list", "peers", "active", "channels", "whoami", "inbox":
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

// die prints "cbus: <msg>" to stderr and returns exit code 1 (bin/cbus:19).
func die(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "cbus: "+format+"\n", a...)
	return 1
}

func runAuth(args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	store := client.NewCredStore()
	switch sub {
	case "set":
		return runAuthSet(store, args, os.Stdin)
	case "status":
		return runAuthStatus(store, args)
	default:
		return die("usage: cbus auth set <host> ... | cbus auth status [host]")
	}
}

// runAuthSet: cbus auth set <host> [--token V] [--cf-id V] [--cf-secret V]
// (V='-' reads all of stdin). Values have ALL whitespace stripped; each '-'
// drains stdin, so at most one stdin-fed credential per invocation.
func runAuthSet(store *client.CredStore, args []string, stdin io.Reader) int {
	if len(args) == 0 {
		return die("usage: cbus auth set <host> [--token V] [--cf-id V] [--cf-secret V]  (V='-' reads stdin)")
	}
	host := args[0]
	args = args[1:]
	if !core.ValidName(host) {
		return die("bad host %q", host)
	}
	n := 0
	for len(args) > 0 {
		flag := args[0]
		switch flag {
		case "--token", "--cf-id", "--cf-secret":
		default:
			return die("unknown flag %s", flag)
		}
		if len(args) < 2 {
			return die("missing value for %s", flag)
		}
		field, val := flag[2:], args[1]
		args = args[2:]
		if val == "-" {
			b, _ := io.ReadAll(stdin)
			val = string(b)
		}
		val = client.StripWhitespace(val)
		if val == "" {
			return die("empty %s", field)
		}
		if err := store.Put(host, field, val); err != nil {
			return die("store %s: %v", field, err)
		}
		n++
	}
	if n == 0 {
		return die("nothing to set (pass --token / --cf-id / --cf-secret)")
	}
	fmt.Printf("stored %d credential(s) for %s in %s\n", n, host, store.Where(host))
	return 0
}

// runAuthStatus: cbus auth status [host] — masked credential state. Unlike the
// bash client it validates the host (closing the auth-status validation gap;
// documented C-delta).
func runAuthStatus(store *client.CredStore, args []string) int {
	host := "nuc"
	if len(args) > 0 {
		host = args[0]
	}
	if !core.ValidName(host) {
		return die("bad host %q", host)
	}
	fmt.Printf("site %s:\n", host)
	for _, f := range client.CredFields {
		v, _ := store.Get(host, f)
		if v != "" {
			fmt.Printf("  %-10s set (…%s)\n", f, client.MaskTail(v, 4))
		} else {
			fmt.Printf("  %-10s absent\n", f)
		}
	}
	return 0
}
