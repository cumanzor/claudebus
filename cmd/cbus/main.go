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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
	case "send":
		return runSend(args[1:])
	case "list", "peers":
		return runList(args[1:])
	case "active":
		// dispatch prepends --active; for a remote target this defeats remote
		// detection (args[0] is no longer @-shaped), matching the bash dead quirk.
		return runList(append([]string{"--active"}, args[1:]...))

	case "tail":
		return runTail(args[1:])

	case "whoami":
		return runWhoami()
	case "inbox":
		return runInbox(args[1:])
	case "channels":
		return runChannels()

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

func runSend(args []string) int {
	if len(args) == 0 {
		return die("usage: cbus send <target> [--from ch/al] [--force] TEXT")
	}
	if client.IsRemote(args[0]) {
		return runSendRemote(args)
	}
	return notImplemented("send (local delivery is Phase 2)")
}

func runSendRemote(args []string) int {
	ch, host, al, err := client.ParseRemote(args[0])
	if err != nil {
		return die("%v", err)
	}
	if al == "" {
		return die("remote send needs <channel>@<host>/<alias>")
	}
	rest := args[1:]
	from := ""
	fromSet := false
	i := 0
loop:
	for i < len(rest) {
		switch rest[i] {
		case "--from":
			if i+1 >= len(rest) {
				return die("missing value for --from")
			}
			from, fromSet = rest[i+1], true
			i += 2
		case "--force":
			i++ // ignored remotely: the spool always queues
		default:
			break loop
		}
	}
	// an explicit empty --from dies (bash ${2:?} null-check) rather than silently
	// falling back to the default — that would mask an unset-$VAR scripting bug.
	if fromSet && from == "" {
		return die("--from: value must not be empty")
	}
	text := strings.Join(rest[i:], " ")
	if text == "" {
		return die("empty message")
	}
	ep, err := client.ResolveRemote(client.NewCredStore(), host)
	if err != nil {
		return die("%v", err)
	}
	if !fromSet {
		from = client.RemoteFromDefault(host, ch)
	}
	if err := client.RemoteSend(ep, core.SendReq{Channel: ch, Alias: al, From: from, Text: text}); err != nil {
		return die("%v", err)
	}
	fmt.Printf("sent to %s@%s/%s via %s relay (from %s)\n", ch, host, al, ep.Mode(), from)
	return 0
}

func runTail(args []string) int {
	if len(args) == 0 {
		return die("usage: cbus tail <channel>/<alias>")
	}
	if client.IsRemote(args[0]) {
		return runTailRemote(args[0])
	}
	// the local tail is the blocking Monitor follower — Phase 2
	return notImplemented("tail (local follower is Phase 2; cbus-go arms remote tails only)")
}

// runTailRemote prints the ws arm-spec and writes this session's identity marker.
// It runs nothing persistent (remote tail is print-only) and needs only the token
// (the ws leg is subprotocol-only). bin/cbus:272-293.
func runTailRemote(target string) int {
	ch, host, al, err := client.ParseRemote(target)
	if err != nil {
		return die("%v", err)
	}
	if al == "" {
		return die("remote tail needs <channel>@<host>/<alias>")
	}
	fd, err := client.ResolveFrontDoor(host)
	if err != nil {
		return die("%v", err)
	}
	token, _ := client.NewCredStore().Get(host, "token")
	if token == "" {
		return die("no relay token for %q — run: cbus auth set %s --token -", host, host)
	}
	if err := client.WriteRemoteMarker(host, ch, al); err != nil {
		return die("marker: %v", err)
	}
	fmt.Print(client.RemoteTailSpec(fd.Base, token, ch, host, al))
	return 0
}

func runList(args []string) int {
	if len(args) > 0 && client.IsRemote(args[0]) {
		return runListRemote(args[0])
	}
	active, chosen := false, ""
	for _, a := range args {
		switch a {
		case "--active", "-a":
			active = true
		default:
			chosen = a // last non-flag wins (bash overwrite semantics)
		}
	}
	return runListLocal(active, chosen)
}

// runListLocal renders local peers with listen/off + pid + host + cwd
// (bin/cbus:589-611); --active shows only live listeners.
func runListLocal(active bool, chosen string) int {
	root := client.CBUSDir()
	channels, _ := os.ReadDir(root)
	any := false
	for _, chE := range channels {
		if !chE.IsDir() || strings.HasPrefix(chE.Name(), ".") {
			continue
		}
		ch := chE.Name()
		if chosen != "" && ch != chosen {
			continue
		}
		chDir := filepath.Join(root, ch)
		if fileExists(filepath.Join(chDir, "meta.json")) { // legacy v1 entry
			if active {
				continue
			}
			any = true
			fmt.Printf("%-7s %-28s legacy v1 entry — run: cbus prune\n", "off   ", ch)
			continue
		}
		aliases, _ := os.ReadDir(chDir)
		for _, alE := range aliases {
			if !alE.IsDir() || strings.HasPrefix(alE.Name(), ".") {
				continue
			}
			metaPath := filepath.Join(chDir, alE.Name(), "meta.json")
			if !fileExists(metaPath) {
				continue
			}
			live := "off   "
			if client.MetaListenerAlive(metaPath) {
				live = "listen"
			}
			if active && live == "off   " {
				continue
			}
			any = true
			m, _ := client.ReadPeerMeta(metaPath)
			pid := "?"
			if m.ListenerPid != 0 {
				pid = strconv.Itoa(m.ListenerPid)
			}
			fmt.Printf("%-7s %-28s pid=%-7s %s  %s\n", live, ch+"/"+alE.Name(), pid, orQ(m.Host), orQ(m.Cwd))
		}
	}
	if !any {
		if active {
			fmt.Println("no active listeners")
		} else {
			fmt.Println("no peers registered")
		}
	}
	return 0
}

func runChannels() int {
	root := client.CBUSDir()
	channels, _ := os.ReadDir(root)
	any := false
	for _, chE := range channels {
		if !chE.IsDir() || strings.HasPrefix(chE.Name(), ".") {
			continue
		}
		chDir := filepath.Join(root, chE.Name())
		if fileExists(filepath.Join(chDir, "meta.json")) { // legacy v1, not a channel
			continue
		}
		aliases, _ := os.ReadDir(chDir)
		total, live := 0, 0
		for _, alE := range aliases {
			if !alE.IsDir() || strings.HasPrefix(alE.Name(), ".") {
				continue
			}
			metaPath := filepath.Join(chDir, alE.Name(), "meta.json")
			if !fileExists(metaPath) {
				continue
			}
			total++
			if client.MetaListenerAlive(metaPath) {
				live++
			}
		}
		if total == 0 {
			continue
		}
		any = true
		fmt.Printf("%-20s %d peers (%d listening)\n", chE.Name(), total, live)
	}
	if !any {
		fmt.Println("no channels")
	}
	return 0
}

// runWhoami prints this session's local registrations (channel/alias) and remote
// from-default markers; exits 1 when the session has neither (bin/cbus:775-792).
func runWhoami() int {
	any := false
	for _, reg := range client.ResolveSelf() {
		fmt.Printf("%s/%s\n", reg.Channel, reg.Alias)
		any = true
	}
	for _, m := range client.SessionMarkers() {
		fmt.Printf("%s@%s/%s (remote from-default — reachability: cbus list @%s)\n", m.Channel, m.Host, m.Alias, m.Host)
		any = true
	}
	if !any {
		fmt.Println("not joined in this session")
		return 1
	}
	return 0
}

// runInbox prints a peer's inbox path (no trailing newline, matching
// bin/cbus:27,794-798); a bare alias is refused.
func runInbox(args []string) int {
	if len(args) == 0 {
		return die("usage: cbus inbox <channel>/<alias>")
	}
	ch, al, err := client.ParseLocal(args[0])
	if err != nil {
		return die("%v", err)
	}
	if ch == "" {
		return die("use <channel>/<alias>")
	}
	fmt.Print(filepath.Join(client.CBUSDir(), ch, al, "inbox.jsonl"))
	return 0
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func runListRemote(target string) int {
	ch, host, _, err := client.ParseRemote(target)
	if err != nil {
		return die("%v", err)
	}
	ep, err := client.ResolveRemote(client.NewCredStore(), host)
	if err != nil {
		return die("%v", err)
	}
	peers, err := client.RemoteList(ep)
	if err != nil {
		return die("%v", err)
	}
	renderRemoteList(peers, ch, host)
	return 0
}

// renderRemoteList prints /peers the way bin/cbus:299-310 does: sorted by
// channel/alias key, optional channel filter, listen/off + queued + lastSeen.
func renderRemoteList(peers core.PeersResponse, channelFilter, host string) {
	keys := make([]string, 0, len(peers))
	for k := range peers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	shown := false
	for _, key := range keys {
		c, a, _ := strings.Cut(key, "/")
		if channelFilter != "" && c != channelFilter {
			continue
		}
		p := peers[key]
		shown = true
		state := "off   "
		if p.Connected {
			state = "listen"
		}
		addr := c + "@" + host + "/" + a
		fmt.Printf("%s  %-28s queued=%-3d lastSeen=%s\n", state, addr, p.Queued, p.LastSeen.Format(time.RFC3339Nano))
	}
	if !shown {
		if channelFilter != "" {
			fmt.Printf("no remote peers in %s@%s\n", channelFilter, host)
		} else {
			fmt.Println("no remote peers")
		}
	}
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
