package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"claudebus/internal/client"
)

// The operator confirmation reads /dev/tty. These are indirected so a test can play
// the operator; the real-terminal refusal is covered against the built binary.
var (
	grantTerminal = func() (io.ReadWriteCloser, string, error) { return client.OpenGrantTerminal() }
	grantStdTTY   = func() bool { return client.IsTerminal(os.Stdin) && client.IsTerminal(os.Stdout) }
	// a test binary's ancestry is `go test`; the walk itself is tested in client
	grantProvenance = client.GrantProvenanceNow
)

const grantUse = `usage: cbus grant <channel>/<alias> "<action>" [--once | --ttl <duration>]
       cbus grant revoke <id>`

// runGrant mints an operator grant after the operator confirms it at a terminal.
func runGrant(args []string) int {
	if len(args) > 0 && args[0] == "revoke" {
		return runGrantRevoke(args[1:])
	}
	if len(args) < 2 {
		return die(grantUse)
	}
	target, action := args[0], args[1]
	p, err := splitVerbArgs(args[2:], map[string]bool{"--ttl": true}, map[string]bool{"--once": true}, true)
	if err != nil {
		return die("%v (%s)", err, grantUse)
	}
	if err := noExtra(p.pos, 0, grantUse); err != nil {
		return die("%v", err)
	}
	mode, ttl := client.GrantOnce, time.Duration(0)
	if v, ok := p.has("--ttl"); ok {
		if p.flags["--once"] {
			return die("--once and --ttl are exclusive (%s)", grantUse)
		}
		if ttl, err = time.ParseDuration(v); err != nil {
			return die("--ttl: want a duration like 30m or 2h, got %q", v)
		}
		mode = client.GrantTTL
	}
	g, err := client.NewGrant(target, action, mode, ttl)
	if err != nil {
		return die("%v", err)
	}
	tty, dev, ok := openOperatorTerminal()
	if !ok {
		return 1
	}
	defer tty.Close()
	fmt.Fprintf(tty, "operator grant for %s/%s\n  session: %s%s\n  action:  %s\n  mode:    %s, expires %s\n",
		g.Channel, g.Alias, g.SessionID, connSuffix(g.ConnectionID), g.Action, g.Mode, g.ExpiresAt.Format(time.RFC3339))
	in := bufio.NewReader(tty)
	if !confirmTyped(tty, in, "Type the peer address to confirm", g.Channel+"/"+g.Alias) ||
		!confirmTyped(tty, in, "Type the action exactly", g.Action) {
		return die("confirmation did not match; nothing was written")
	}
	g.GrantedBy = grantProvenance(dev)
	if err := client.WriteGrant(g); err != nil {
		return die("%v", err)
	}
	if g.GrantedBy.HarnessAncestor || g.GrantedBy.AncestryTruncated {
		// never print the success line here: it is the string a peer would relay
		fmt.Printf("recorded %s as SUSPECT (unusable)\n", g.ID)
	} else {
		fmt.Printf("granted %s to %s/%s session %s (%s, expires %s)\n", g.ID, g.Channel, g.Alias, g.SessionID, g.Mode, g.ExpiresAt.Format(time.RFC3339))
		if err := client.DeliverGrantNotice(g); err != nil {
			fmt.Fprintf(os.Stderr, "cbus: notice not delivered (%v): tell %s/%s to run cbus grants\n", err, g.Channel, g.Alias)
		} else {
			fmt.Printf("notice delivered to %s/%s\n", g.Channel, g.Alias)
		}
	}
	switch {
	case g.GrantedBy.HarnessAncestor:
		fmt.Fprintf(os.Stderr, "cbus: this ran inside a %s process tree, so %s is recorded as suspect and cannot be used; run cbus grant from a plain terminal\n",
			g.GrantedBy.Harness, g.ID)
		return 1
	case g.GrantedBy.AncestryTruncated:
		fmt.Fprintf(os.Stderr, "cbus: the process ancestry could not be read to the top, so %s is recorded as suspect and cannot be used\n", g.ID)
		return 1
	}
	return 0
}

func runGrantRevoke(args []string) int {
	const use = "usage: cbus grant revoke <id>"
	if len(args) != 1 {
		return die(use)
	}
	id := args[0]
	tty, dev, ok := openOperatorTerminal()
	if !ok {
		return 1
	}
	defer tty.Close()
	if !confirmTyped(tty, bufio.NewReader(tty), "Type the grant id to revoke", id) {
		return die("confirmation did not match; nothing was written")
	}
	v, err := client.RevokeGrant(id, "operator "+dev)
	if err != nil {
		return die("%v", err)
	}
	fmt.Printf("revoked %s (%s/%s: %s)\n", v.ID, v.Channel, v.Alias, v.Action)
	return 0
}

// openOperatorTerminal refuses, writing nothing, when there is no controlling
// terminal or stdin/stdout are not terminals.
func openOperatorTerminal() (io.ReadWriteCloser, string, bool) {
	tty, dev, err := grantTerminal()
	if err != nil {
		die("grant needs the operator at a real terminal (%v); nothing was written", err)
		return nil, "", false
	}
	if !grantStdTTY() {
		tty.Close()
		die("grant needs the operator at a real terminal (stdin and stdout must be one); nothing was written")
		return nil, "", false
	}
	return tty, dev, true
}

func confirmTyped(w io.Writer, r *bufio.Reader, prompt, want string) bool {
	fmt.Fprintf(w, "%s: ", prompt)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.TrimRight(line, "\r\n") == want
}

const grantsUse = `usage: cbus grants [--all] [--json]
       cbus grants use <id>`

// runGrants lists grants for this session's own registrations, or all of them.
func runGrants(args []string) int {
	if len(args) > 0 && args[0] == "use" {
		return runGrantsUse(args[1:])
	}
	p, err := splitVerbArgs(args, nil, map[string]bool{"--all": true, "--json": true}, true)
	if err != nil {
		return die("%v (%s)", err, grantsUse)
	}
	if err := noExtra(p.pos, 0, grantsUse); err != nil {
		return die("%v", err)
	}
	regs := client.ResolveSelf()
	if !p.flags["--all"] && len(regs) == 0 {
		return die("not joined in this session: cbus grants lists the grants for your own address (--all lists every grant)")
	}
	views, err := client.ListGrants(regs, p.flags["--all"])
	if err != nil {
		return die("%v", err)
	}
	if p.flags["--json"] {
		if views == nil {
			views = []client.GrantView{}
		}
		b, _ := json.MarshalIndent(views, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(views) == 0 {
		fmt.Println("no grants")
		return 0
	}
	for _, v := range views {
		fmt.Printf("%s  %-7s  %s/%s  %s until %s  %s\n", v.ID, v.State, v.Channel, v.Alias, v.Mode,
			v.ExpiresAt.Format(time.RFC3339), v.Action)
		fmt.Printf("    session %s, minted on %s via %s%s\n", v.SessionID, v.GrantedBy.TTY, topAncestors(v.GrantedBy.Ancestors, 4), usesSuffix(v.Uses))
	}
	return 0
}

func runGrantsUse(args []string) int {
	if len(args) != 1 {
		return die(grantsUse)
	}
	v, err := client.UseGrant(args[0], client.SessionID(), client.ResolveSelf())
	if err != nil {
		return die("%v", err)
	}
	fmt.Printf("using %s for %s/%s: %s\n", v.ID, v.Channel, v.Alias, v.Action)
	return 0
}

func connSuffix(conn string) string {
	if conn == "" {
		return ""
	}
	return " (connection " + conn + ")"
}

func usesSuffix(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return ", used 1 time"
	}
	return fmt.Sprintf(", used %d times", n)
}

// topAncestors shows the nearest few minting ancestors, so a chain that a terminal
// multiplexer reparented (zsh < tmux) reads differently from a login shell's.
func topAncestors(chain []string, n int) string {
	if len(chain) == 0 {
		return "(none recorded)"
	}
	if len(chain) > n {
		return strings.Join(chain[:n], " < ") + " < ..."
	}
	return strings.Join(chain, " < ")
}
