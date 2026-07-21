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
	// the hidden detached poll runs before anything else and never triggers itself.
	if verb == updateCheckSubcmd {
		return cmdUpdateCheckRefresh()
	}
	// opt-in update hint (CBUS_UPDATE_CHECK=1): a best-effort stderr note + a detached
	// poll, both silent on failure — it must never break the command it precedes.
	maybeStartUpdateCheck(verb, hasJSONFlag(args))
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
	case "list":
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
	case "leave":
		return runLeave(args[1:])
	case "rename":
		return runRename(args[1:])
	case "unregister":
		return runUnregister(args[1:])
	case "prune":
		return runPrune(args[1:])
	case "close":
		return runClose(args[1:])

	case "hook-exit": // SessionEnd hook: announce departure (never fails the session)
		return runHookExit()
	case "hook-compact": // PreCompact/PostCompact hooks: announce compaction (never fails the session)
		return runHookCompact(args[1:])
	case "bootstrap":
		return runBootstrap(args[1:])
	case "branch":
		return runBranch(args[1:])
	case "spawn": // post-cutover Go-native verb (cbus-ijx.2) — no bash counterpart
		return runSpawn(args[1:])
	case "formation": // post-cutover Go-native verb — no bash counterpart
		return runFormation(args[1:])
	case "install-commands": // cbus-7sg: write the embedded /bus-* skills to ~/.claude/commands
		return runInstallCommands(args[1:])
	case "install-roles": // cbus-7sg: write the embedded role prompts to $CBUS_DIR/roles
		return runInstallRoles(args[1:])
	case "selfupdate": // cbus-7sg: gh-driven in-place update of the running binary
		return runSelfupdate(args[1:])

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
	// warnIfSessionless lives in the sub-handlers, AFTER a --session-id override is
	// applied — evaluating it here would print a false sessionless warning for a send
	// that supplies its own id.
	if client.IsRemote(args[0]) {
		return runSendRemote(args)
	}
	return runSendLocal(args)
}

func runSendLocal(args []string) int {
	target := args[0]
	// unknown --flags are NOT strict here: a message body may legitimately start with
	// '-' (and `--` terminates option parsing — the ruled delta).
	p, err := splitVerbArgs(args[1:], map[string]bool{"--from": true, "--session-id": true}, map[string]bool{"--force": true}, false)
	if err != nil {
		return die("%v", err)
	}
	if sid, ok := p.has("--session-id"); ok && sid != "" {
		defer client.OverrideSessionID(sid)()
	}
	warnIfSessionless()
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
	p, err := splitVerbArgs(args[1:], map[string]bool{"--from": true, "--session-id": true}, map[string]bool{"--force": true}, false)
	if err != nil {
		return die("%v", err)
	}
	if sid, ok := p.has("--session-id"); ok && sid != "" {
		defer client.OverrideSessionID(sid)()
	}
	warnIfSessionless()
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

const tailUsage = "usage: cbus tail [--steal] <channel>/<alias>"

func runTail(args []string) int {
	p, err := splitVerbArgs(args, nil, map[string]bool{"--steal": true}, true)
	if err != nil {
		return die("%v", err)
	}
	if len(p.pos) == 0 {
		return die(tailUsage)
	}
	if err := noExtra(p.pos, 1, tailUsage); err != nil {
		return die("%v", err)
	}
	if client.IsRemote(p.pos[0]) {
		if p.flags["--steal"] {
			// remote displacement is the relay's, and it already happens on attach
			return die("--steal is local-only; a remote tail displaces on attach")
		}
		return runTailRemote(p.pos[0])
	}
	warnIfSessionless()
	// local tail: arm this session as the listener and run the follower in-process.
	if err := client.ArmLocalTail(p.pos[0], p.flags["--steal"]); err != nil {
		return die("%v", err)
	}
	// reached when the follower goes dormant (displaced, renamed, re-joined,
	// unregistered). Exit 0: losing the listener slot is a deliberate outcome, and the
	// marker line on stdout has already said which one.
	return 0
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

// runHookCompact runs the PreCompact/PostCompact hook. It ALWAYS returns 0 — rc 2 would
// BLOCK compaction, and a hook must never fail the session — and writes NOTHING to
// stdout, which Claude Code parses as hook JSON on exit 0. A bad or missing phase is
// reported on stderr (the hook debug log) and still exits 0; trailing args are ignored
// rather than fatal (the noExtra rule would mean failing a hook, which is not allowed).
func runHookCompact(args []string) int {
	phase := ""
	if len(args) > 0 {
		phase = args[0]
	}
	if err := client.HookCompact(phase, os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "cbus: hook-compact: %v\n", err)
	}
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

// applySessionIDFlag pulls a `--session-id VALUE` pair out of args (anywhere, via
// extractFlag) and applies it as the in-process session override, returning the remaining
// args and a restore func the caller defers. The override outranks the $*_SESSION_ID env
// chain, so a hook or a scripted multi-session driver can act as a named session without
// exporting one. Absent or empty flag => a no-op restore. Used by the identity-recording
// verbs (join/leave/rename); send threads it through splitVerbArgs so a message body may
// still contain the literal token.
func applySessionIDFlag(args []string) (rest []string, restore func(), err error) {
	sid, rest, err := extractFlag(args, "--session-id")
	if err != nil {
		return nil, func() {}, err
	}
	if sid == "" {
		return rest, func() {}, nil
	}
	return rest, client.OverrideSessionID(sid), nil
}

func runBranch(args []string) int {
	const use = "usage: cbus branch [window|tab|tmux|pane] [channel] [--model m] [--name n]"
	model, name, args, merr := extractForkFlags(args)
	if merr != nil {
		return die("%v (%s)", merr, use)
	}
	// roles are spawn-only: a fork inherits its parent's intent, and forking
	// across roles is the documented anti-pattern (ghost orchestration).
	if role, rest, rerr := extractFlag(args, "--role"); rerr != nil || role != "" || len(rest) != len(args) {
		return die("--role rides fresh spawns only (a fork inherits its parent's intent) — use: cbus spawn ... --role <r>")
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
	const use = "usage: cbus spawn [window|tab|tmux|pane] [channel|<ch>@<host>] [--model m] [--name n] [--role r]"
	model, name, args, merr := extractForkFlags(args)
	if merr != nil {
		return die("%v (%s)", merr, use)
	}
	role, args, rerr := extractFlag(args, "--role")
	if rerr != nil {
		return die("%v (%s)", rerr, use)
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
	rAddr, child, err := client.Spawn(target, addr, model, name, role, client.OSAForker{})
	if err != nil {
		return die("%v", err)
	}
	if child != "" {
		brief := ""
		if role != "" {
			brief = " + role brief"
		}
		fmt.Printf("spawned: fresh session -> %s/%s (%s, alias fixed + session titled%s); it joins and arms itself\n", rAddr, child, target, brief)
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
	jsonMode := hasJSONFlag(args)
	if err := refuseRemoteJSON(args, jsonMode); err != nil {
		return die("%v", err)
	}
	if len(args) > 0 && client.IsRemote(args[0]) {
		return runListRemote(args[0])
	}
	active, chosen := false, ""
	for _, a := range args {
		switch a {
		case "--active", "-a":
			active = true
		case "--json", "-json":
		default:
			chosen = a // last non-flag wins (bash overwrite semantics)
		}
	}
	return runListLocal(active, chosen, jsonMode)
}

// refuseRemoteJSON is the R15 gate: --json is local-only in M5, and BOTH ways of
// asking for it remotely are silent wrong answers today. `list @host --json` reaches
// runListRemote, which never looks at the remaining args and drops the flag; `list
// --json @host` never reaches it, because remote detection inspects args[0] only, so
// the @-target falls through and becomes a local channel filter. Once --json is a
// contract, a request it cannot serve has to fail loudly instead of answering a
// different question.
func refuseRemoteJSON(args []string, jsonMode bool) error {
	if !jsonMode {
		return nil // the --active @host dead quirk is untouched
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if client.IsRemote(a) {
			return fmt.Errorf("--json is local-only; %q is a relay target and the remote shape rides the relay-wire follow-up. Drop --json, or drop the @ target", a)
		}
	}
	return nil
}

// runListLocal renders local peers with listen/off + pid + host + cwd
// (bin/cbus:589-611); --active shows only live listeners. Text and JSON walk the SAME
// snapshot, so the two renderings can never disagree about who is listening.
func runListLocal(active bool, chosen string, jsonMode bool) int {
	snap := client.ScanStore()
	if jsonMode {
		return emitListJSON(snap, active, chosen)
	}
	any := false
	for _, ch := range snap.Channels {
		if chosen != "" && ch.Name != chosen {
			continue
		}
		if ch.LegacyV1 {
			if active {
				continue
			}
			any = true
			fmt.Printf("%-7s %-28s legacy v1 entry — run: cbus prune\n", "off   ", ch.Name)
			continue
		}
		for _, p := range ch.Peers {
			if active && !p.Listening {
				continue
			}
			any = true
			live := "off   "
			if p.Listening {
				live = "listen"
			}
			pid := "?"
			if p.ListenerPid != 0 {
				pid = strconv.Itoa(p.ListenerPid)
			}
			fmt.Printf("%-7s %-28s pid=%-7s %s  %s\n", live, ch.Name+"/"+p.Alias, pid, orQ(p.Host), orQ(p.Cwd))
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
	jsonMode := hasJSONFlag(args)
	rest := args
	if jsonMode {
		rest = dropJSONFlag(args)
	}
	if err := noExtra(rest, 0, "usage: cbus channels [--json]"); err != nil {
		return die("%v", err)
	}
	snap := client.ScanStore()
	if jsonMode {
		return emitChannelsJSON(snap)
	}
	any := false
	for _, ch := range snap.Channels {
		if ch.LegacyV1 || len(ch.Peers) == 0 { // legacy v1 is not a channel
			continue
		}
		live := 0
		for _, p := range ch.Peers {
			if p.Listening {
				live++
			}
		}
		any = true
		fmt.Printf("%-20s %d peers (%d listening)\n", ch.Name, len(ch.Peers), live)
	}
	if !any {
		fmt.Println("no channels")
	}
	return 0
}

// dropJSONFlag removes the --json/-json token so a verb's positional count is
// unchanged by it (`cbus channels --json` must not read as one trailing positional).
func dropJSONFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			return append(out, args[i:]...)
		}
		if a == "--json" || a == "-json" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// runWhoami prints this session's local registrations (channel/alias) and remote
// from-default markers; exits 1 when the session has neither (bin/cbus:775-792).
func runWhoami(args []string) int {
	jsonMode := hasJSONFlag(args)
	rest := args
	if jsonMode {
		rest = dropJSONFlag(args)
	}
	if err := noExtra(rest, 0, "usage: cbus whoami [--json]"); err != nil {
		return die("%v", err)
	}
	local, remote := client.ResolveSelf(), client.SessionMarkers()
	if jsonMode {
		return emitWhoamiJSON(local, remote)
	}
	for _, reg := range local {
		fmt.Printf("%s/%s\n", reg.Channel, reg.Alias)
	}
	for _, m := range remote {
		fmt.Printf("%s@%s/%s (remote from-default — reachability: cbus list @%s)\n", m.Channel, m.Host, m.Alias, m.Host)
	}
	if len(local) == 0 && len(remote) == 0 {
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
	args, restore, err := applySessionIDFlag(args)
	if err != nil {
		return die("%v", err)
	}
	defer restore()
	if len(args) == 0 {
		return die("usage: cbus join <channel> [alias]")
	}
	if err := noExtra(args, 2, "usage: cbus join <channel> [alias]"); err != nil {
		return die("%v", err)
	}
	warnIfSessionless()
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
	args, restore, err := applySessionIDFlag(args)
	if err != nil {
		return die("%v", err)
	}
	defer restore()
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
	args, restore, err := applySessionIDFlag(args)
	if err != nil {
		return die("%v", err)
	}
	defer restore()
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

const closeUsage = "usage: cbus close <channel>/<alias> [...] [--force]"

// runClose ends peer sessions. Every target produces exactly one stdout line in the
// order given — refusals included, matching CloseReport's own design of reporting a
// failure rather than returning an error — and the exit code is 1 if ANY target
// failed. "already gone" is a success (Ok), so a scripted sweep can close the same
// roster twice.
//
// A remote target is refused HERE, before anything is signalled: ClosePeer takes a
// local (channel, alias) and cannot express a host, so an @host accepted this far
// would silently tear down a same-named LOCAL peer instead.
func runClose(args []string) int {
	// close parses its own argv instead of splitVerbArgs: that scanner stops at the
	// first positional (flags.go:60), which works for verbs taking a fixed leading
	// target but would swallow a trailing --force as a TARGET here, where targets are
	// variadic. Unknown flags are strict in either position — close has no free-text
	// body to protect, so a typo'd --forse must die rather than be signalled at.
	force := false
	var targets []string
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "-"):
			return die("unknown flag %s", a)
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		return die("%s", closeUsage)
	}
	rc := 0
	for _, t := range targets {
		r, err := closeOne(t, force)
		if err != nil {
			fmt.Printf("%s: %v\n", t, err)
			rc = 1
			continue
		}
		fmt.Printf("%s: %s\n", r.Target, r.Detail)
		if !r.Ok {
			rc = 1
		}
	}
	return rc
}

// closeOne resolves one target the way send does (bare alias searches this session's
// own channels) and tears it down.
func closeOne(target string, force bool) (client.CloseReport, error) {
	if client.IsRemote(target) {
		return client.CloseReport{}, fmt.Errorf("close is local-only — a remote peer must be closed on its own host")
	}
	ch, al, err := client.ParseLocal(target)
	if err != nil {
		return client.CloseReport{}, err
	}
	if ch == "" {
		found, ok := client.FindPeerChannel(al)
		if !ok {
			return client.CloseReport{}, fmt.Errorf("no peer %q in your channels — use <channel>/<alias> (cbus list)", al)
		}
		ch = found
	}
	return client.ClosePeer(ch, al, force), nil
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
