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

// version is stamped at build time via -ldflags "-X main.version=<git describe>".
// A readiness delta (bash cbus has no version verb) — provenance for the installed
// binary during coexistence and cutover.
var version = "dev"

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
	case "--version", "version":
		fmt.Printf("cbus-go %s\n", version)
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
		return runWhoami(args[1:])
	case "inbox":
		return runInbox(args[1:])
	case "channels":
		return runChannels(args[1:])

	case "join":
		return runJoin(args[1:])
	case "register": // deprecated: v1 alias for the global channel
		return runJoin(append([]string{"global"}, args[1:]...))
	case "leave":
		return runLeave(args[1:])
	case "rename":
		return runRename(args[1:])
	case "unregister":
		return runUnregister(args[1:])
	case "prune":
		return runPrune(args[1:])

	case "hook-exit": // SessionEnd hook: announce departure (never fails the session)
		return runHookExit()
	case "bootstrap":
		return runBootstrap(args[1:])
	case "branch":
		return runBranch(args[1:])
	case "spawn": // post-cutover Go-native verb (cbus-ijx.2) — no bash counterpart
		return runSpawn(args[1:])

	default:
		// bash-exact (Option X): single-quoted verb + `cbus --help` so cutover is a
		// pure swap (bin/cbus:913).
		fmt.Fprintf(os.Stderr, "cbus: unknown command '%s' (cbus --help)\n", verb)
		return 1
	}
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
	return runSendLocal(args)
}

func runSendLocal(args []string) int {
	target := args[0]
	// unknown --flags are NOT strict here: a message body may legitimately start with
	// '-' (and `--` terminates option parsing — the ruled delta).
	p, err := splitVerbArgs(args[1:], map[string]bool{"--from": true}, map[string]bool{"--force": true}, false)
	if err != nil {
		return die("%v", err)
	}
	from, fromSet := p.has("--from")
	force := p.flags["--force"]
	if fromSet && from == "" {
		return die("--from: value must not be empty")
	}
	text := strings.Join(p.pos, " ")
	if text == "" {
		return die("empty message")
	}
	resolved, resolvedFrom, warn, err := client.LocalSend(target, from, force, text)
	if err != nil {
		return die("%v", err)
	}
	if warn {
		fmt.Fprintf(os.Stderr, "cbus: warning: %q is not listening — sending anyway\n", resolved)
	}
	fmt.Printf("sent to %s (from %s)\n", resolved, resolvedFrom)
	return 0
}

func runSendRemote(args []string) int {
	ch, host, al, err := client.ParseRemote(args[0])
	if err != nil {
		return die("%v", err)
	}
	if al == "" {
		return die("remote send needs <channel>@<host>/<alias>")
	}
	// --force is accepted but ignored remotely (the spool always queues); `--`
	// terminates option parsing (ruled delta) so a body may start with '-'.
	p, err := splitVerbArgs(args[1:], map[string]bool{"--from": true}, map[string]bool{"--force": true}, false)
	if err != nil {
		return die("%v", err)
	}
	from, fromSet := p.has("--from")
	// an explicit empty --from dies (bash ${2:?} null-check) rather than silently
	// falling back to the default — that would mask an unset-$VAR scripting bug.
	if fromSet && from == "" {
		return die("--from: value must not be empty")
	}
	text := strings.Join(p.pos, " ")
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
	// the re-exec'd follower (ArmLocalTail's syscall.Exec) carries a hidden --inbox;
	// when present, THIS process is the follower — run the blocking loop, don't re-arm.
	if inbox, mode, ok := client.ParseTailFollower(args); ok {
		client.RunFollower(inbox, mode) // blocks until the Monitor kills us; never returns
		return 0
	}
	if err := noExtra(args, 1, "usage: cbus tail <channel>/<alias>"); err != nil {
		return die("%v", err)
	}
	if client.IsRemote(args[0]) {
		return runTailRemote(args[0])
	}
	// local tail: arm this session as the listener and exec-replace into the follower.
	if err := client.ArmLocalTail(args[0]); err != nil {
		return die("%v", err)
	}
	return 0 // unreachable: ArmLocalTail image-replaces on success
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

// runHookExit runs the SessionEnd hook. It ALWAYS returns 0 — a hook must never fail
// the session — and produces no stdout (best-effort local-leave, remote markers survive).
func runHookExit() int {
	client.HookExit(os.Stdin)
	return 0
}

func runBootstrap(args []string) int {
	const use = "usage: cbus bootstrap <channel> [parent-alias] [child-alias]"
	if len(args) == 0 {
		return die(use)
	}
	if err := noExtra(args, 3, use); err != nil {
		return die("%v", err)
	}
	ch := args[0]
	parent := "main"
	if len(args) > 1 {
		parent = args[1]
	}
	if !core.ValidName(ch) {
		return die("bad channel %q", ch)
	}
	if !core.ValidName(parent) {
		return die("bad alias %q", parent)
	}
	if len(args) > 2 { // reserved-alias variant (what branch actually sends) — print-only, no reservation
		if !core.ValidName(args[2]) {
			return die("bad alias %q", args[2])
		}
		fmt.Println(client.BootstrapPromptAliased(ch, parent, args[2]))
		return 0
	}
	fmt.Println(client.BootstrapPrompt(ch, parent))
	return 0
}

// extractFlag pulls a `<flag> <value>` pair out of args wherever it appears
// (branch/spawn take flags trailing or leading), returning the remaining positionals.
func extractFlag(args []string, flag string) (val string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s: missing value", flag)
			}
			val = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return val, rest, nil
}

// extractForkFlags pulls the branch/spawn-shared `--model` and `--name` pairs.
func extractForkFlags(args []string) (model, name string, rest []string, err error) {
	if model, rest, err = extractFlag(args, "--model"); err != nil {
		return "", "", nil, err
	}
	if name, rest, err = extractFlag(rest, "--name"); err != nil {
		return "", "", nil, err
	}
	return model, name, rest, nil
}

func runBranch(args []string) int {
	const use = "usage: cbus branch [window|tab|tmux] [channel] [--model m] [--name n]"
	model, name, args, merr := extractForkFlags(args)
	if merr != nil {
		return die("%v (%s)", merr, use)
	}
	if err := noExtra(args, 2, use); err != nil {
		return die("%v", err)
	}
	target := "window"
	if len(args) > 0 {
		target = args[0]
	}
	ch := ""
	if len(args) > 1 {
		ch = args[1]
	}
	rch, alias, child, err := client.Branch(target, ch, model, name, client.OSAForker{})
	if err != nil {
		return die("%v", err)
	}
	fmt.Printf("parent: %s/%s; child: %s/%s (alias reserved + session titled — it joins as it boots)\n", rch, alias, rch, child)
	fmt.Printf("arm listening (if not armed) via the Monitor tool, NOT Bash (`cbus tail` blocks forever in a shell): cbus tail %s/%s\n", rch, alias)
	return 0
}

func runSpawn(args []string) int {
	const use = "usage: cbus spawn [window|tab|tmux] [channel|<ch>@<host>] [--model m] [--name n]"
	model, name, args, merr := extractForkFlags(args)
	if merr != nil {
		return die("%v (%s)", merr, use)
	}
	if err := noExtra(args, 2, use); err != nil {
		return die("%v", err)
	}
	target := "window"
	if len(args) > 0 {
		target = args[0]
	}
	addr := ""
	if len(args) > 1 {
		addr = args[1]
	}
	rAddr, child, err := client.Spawn(target, addr, model, name, client.OSAForker{})
	if err != nil {
		return die("%v", err)
	}
	if child != "" {
		fmt.Printf("spawned: fresh session -> %s/%s (%s, alias fixed + session titled); it joins and arms itself\n", rAddr, child, target)
	} else {
		fmt.Printf("spawned: fresh session -> %s (%s); it joins and arms itself (picks its own alias)\n", rAddr, target)
	}
	if client.IsRemote(rAddr) {
		fmt.Printf("verify: cbus list @%s\n", rAddr[strings.Index(rAddr, "@")+1:])
	} else {
		fmt.Printf("verify: cbus list %s\n", rAddr)
	}
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

func runChannels(args []string) int {
	if err := noExtra(args, 0, "usage: cbus channels"); err != nil {
		return die("%v", err)
	}
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
func runWhoami(args []string) int {
	if err := noExtra(args, 0, "usage: cbus whoami"); err != nil {
		return die("%v", err)
	}
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
	if err := noExtra(args, 1, "usage: cbus inbox <channel>/<alias>"); err != nil {
		return die("%v", err)
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

func runJoin(args []string) int {
	if len(args) == 0 {
		return die("usage: cbus join <channel> [alias]")
	}
	if err := noExtra(args, 2, "usage: cbus join <channel> [alias]"); err != nil {
		return die("%v", err)
	}
	ch := args[0]
	alias := ""
	if len(args) > 1 {
		alias = args[1]
	}
	chosen, already, err := client.Join(ch, alias)
	if err != nil {
		return die("%v", err)
	}
	if already {
		fmt.Printf("already joined \"%s\" as \"%s\"\n", ch, chosen)
		fmt.Printf("listen (if not armed) via the Monitor tool, NOT Bash (`cbus tail` blocks forever in a shell): cbus tail %s/%s\n", ch, chosen)
		return 0
	}
	sid := client.SessionID()
	if sid == "" {
		sid = "none"
	}
	fmt.Printf("joined channel \"%s\" as \"%s\" (session %s)\n", ch, chosen, sid)
	fmt.Printf("address: %s/%s\n", ch, chosen)
	fmt.Printf("now arm the Monitor tool (NOT Bash — `cbus tail` execs a follower that never exits, so a Bash call blocks forever) on: cbus tail %s/%s\n", ch, chosen)
	return 0
}

func runLeave(args []string) int {
	if err := noExtra(args, 1, "usage: cbus leave [channel]"); err != nil {
		return die("%v", err)
	}
	if len(args) > 0 && client.IsRemote(args[0]) {
		ch, host, _, err := client.ParseRemote(args[0])
		if err != nil {
			return die("%v", err)
		}
		if err := client.LeaveRemote(host, ch); err != nil {
			return die("%v", err)
		}
		fmt.Printf("left %s@%s (this session's marker removed; queued mail stays on the relay)\n", ch, host)
		return 0
	}
	ch := ""
	if len(args) > 0 {
		ch = args[0]
	}
	left, err := client.Leave(ch)
	if err != nil {
		return die("%v", err)
	}
	for _, l := range left {
		fmt.Printf("left %s\n", l)
	}
	return 0
}

func runRename(args []string) int {
	if len(args) == 0 {
		return die("usage: cbus rename <new-alias> [channel]")
	}
	if err := noExtra(args, 2, "usage: cbus rename <new-alias> [channel]"); err != nil {
		return die("%v", err)
	}
	if client.IsRemote(args[0]) {
		return die("rename is local-only — remote (@host) aliases are relay-side (see cbus leave/tail)")
	}
	newAlias := args[0]
	wantCh := ""
	if len(args) > 1 {
		wantCh = args[1]
	}
	ch, old, already, err := client.Rename(newAlias, wantCh)
	if err != nil {
		return die("%v", err)
	}
	if already {
		fmt.Printf("already named \"%s/%s\"\n", ch, old)
		return 0
	}
	fmt.Printf("renamed %s/%s -> %s/%s\n", ch, old, ch, newAlias)
	fmt.Printf("re-arm the Monitor tool (old tail is now stale; NOT Bash — `cbus tail` blocks forever in a shell): cbus tail %s/%s\n", ch, newAlias)
	return 0
}

func runUnregister(args []string) int {
	if len(args) == 0 {
		return die("usage: cbus unregister <channel>/<alias>")
	}
	if err := noExtra(args, 1, "usage: cbus unregister <channel>/<alias>"); err != nil {
		return die("%v", err)
	}
	ch, al, err := client.ParseLocal(args[0])
	if err != nil {
		return die("%v", err)
	}
	if ch == "" {
		return die("use <channel>/<alias>")
	}
	if err := client.Unregister(ch, al); err != nil {
		return die("%v", err)
	}
	fmt.Printf("unregistered %s/%s\n", ch, al)
	return 0
}

func runPrune(args []string) int {
	if len(args) > 0 && client.IsRemote(args[0]) {
		return runPruneRemote(args[0])
	}
	if err := noExtra(args, 1, "usage: cbus prune [channel | [channel]@host]"); err != nil {
		return die("%v", err)
	}
	chosen := ""
	if len(args) > 0 {
		chosen = args[0]
	}
	msgs := client.Prune(chosen)
	if len(msgs) == 0 {
		fmt.Println("nothing to prune")
		return 0
	}
	for _, m := range msgs {
		fmt.Println(m)
	}
	return 0
}

// runPruneRemote reaps off/no-mail peers from a relay's spool. It is channel-scoped
// like local prune: "<channel>@host" prunes one channel, "@host" prunes them all. A
// trailing "/alias" is rejected — prune never targets a single peer (a footgun on a
// destructive op), unlike list which silently ignores it.
func runPruneRemote(target string) int {
	ch, host, al, err := client.ParseRemote(target)
	if err != nil {
		return die("%v", err)
	}
	if al != "" {
		return die("prune is channel-scoped: use <channel>@%s or @%s", host, host)
	}
	ep, err := client.ResolveRemote(client.NewCredStore(), host)
	if err != nil {
		return die("%v", err)
	}
	pruned, err := client.RemotePrune(ep, ch)
	if err != nil {
		return die("%v", err)
	}
	if len(pruned) == 0 {
		fmt.Println("nothing to prune")
		return 0
	}
	for _, key := range pruned {
		c, a, _ := strings.Cut(key, "/")
		fmt.Printf("pruned %s@%s/%s\n", c, host, a)
	}
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
