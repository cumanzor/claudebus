package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"claudebus/internal/client"
)

// formationUsage advertises only the verbs that exist. bootstrap lands in a later
// milestone and is absent on purpose — a help text that promises a verb the binary
// does not have is a bug report waiting to happen.
const formationUsage = "usage: cbus formation save <name> [channel] | apply <name> [opts] | bootstrap <name> <alias> [--brief TEXT] | list | show <name> | rm <name>"

func runFormation(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "save":
		return runFormationSave(args)
	case "apply":
		return runFormationApply(args)
	case "bootstrap":
		return runFormationBootstrap(args)
	case "list":
		return runFormationList(args)
	case "show":
		return runFormationShow(args)
	case "rm":
		return runFormationRm(args)
	default:
		return die(formationUsage)
	}
}

func runFormationSave(args []string) int {
	const use = "usage: cbus formation save <name> [channel]"
	if len(args) == 0 {
		return die(use)
	}
	if err := noExtra(args, 2, use); err != nil {
		return die("%v", err)
	}
	name := args[0]
	ch := ""
	if len(args) > 1 {
		ch = args[1]
	}
	if ch == "" {
		var err error
		if ch, err = ownChannel(); err != nil {
			return die("%v (%s)", err, use)
		}
	}
	f, rep, err := client.SaveFormation(name, ch)
	if err != nil {
		return die("%v", err)
	}
	path, _ := client.FormationPath(name)
	state := "refreshed"
	if rep.New {
		state = "new"
	}
	fmt.Printf("saved formation %q (%s, %s)\n", name, path, state)
	var parts []string
	if len(rep.Added) > 0 {
		parts = append(parts, fmt.Sprintf("+%d new (%s)", len(rep.Added), strings.Join(rep.Added, ", ")))
	}
	if len(rep.Updated) > 0 {
		parts = append(parts, fmt.Sprintf("%d refreshed (%s)", len(rep.Updated), strings.Join(rep.Updated, ", ")))
	}
	if len(rep.Kept) > 0 {
		parts = append(parts, fmt.Sprintf("%d kept, not on the channel now (%s)", len(rep.Kept), strings.Join(rep.Kept, ", ")))
	}
	fmt.Printf("  channel %q: %s\n", f.Channel, strings.Join(parts, "; "))
	if len(rep.Added) > 0 {
		fmt.Println("  captured alias/sessionId/cwd/machine — the store records nothing else;")
		fmt.Println("  model, rolefile/role, origin and profile are yours to fill in")
	}
	fmt.Printf("  check it: cbus formation show %s\n", name)
	return 0
}

// defaultWait is how long apply waits for kickoff answers. A fresh session takes
// tens of seconds to boot, load its role and answer; 90s clears that with room, and
// a peer that misses it is reported as failed rather than silently assumed good.
const defaultWait = 90 * time.Second

func runFormationApply(args []string) int {
	const use = "usage: cbus formation apply <name> [--only a,b] [--dry-run] [--wait 90s|0]"
	if len(args) == 0 {
		return die(use)
	}
	// name first, then flags — the shape `send` uses (splitVerbArgs scans LEADING
	// options only, so the positional cannot sit behind them).
	name := args[0]
	p, err := splitVerbArgs(args[1:], map[string]bool{"--only": true, "--wait": true},
		map[string]bool{"--dry-run": true}, true)
	if err != nil {
		return die("%v (%s)", err, use)
	}
	if err := noExtra(p.pos, 0, use); err != nil {
		return die("%v", err)
	}
	opts := client.ApplyOptions{DryRun: p.flags["--dry-run"], Wait: defaultWait}
	if v, ok := p.has("--only"); ok {
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				opts.Only = append(opts.Only, a)
			}
		}
		if len(opts.Only) == 0 {
			return die("--only: name at least one peer")
		}
	}
	if v, ok := p.has("--wait"); ok {
		d, derr := time.ParseDuration(v)
		if derr != nil || d < 0 {
			return die("--wait: want a duration like 90s or 2m (0 = do not wait), got %q", v)
		}
		opts.Wait = d
	}
	f, err := client.LoadFormation(name)
	if err != nil {
		return die("%v", err)
	}
	rep, err := client.Apply(f, opts, client.OSAForker{})
	if rep != nil {
		renderApplyReport(f, rep, opts)
	}
	if err != nil {
		return die("%v", err)
	}
	if !rep.Converged() {
		return 1
	}
	return 0
}

// renderApplyReport prints what happened per peer. Every non-launch carries its
// reason: a skip or refusal with no stated cause is how an apply that did nothing
// gets read as an apply that worked.
func renderApplyReport(f *client.Formation, rep *client.ApplyReport, opts client.ApplyOptions) {
	what := "apply"
	if rep.DryRun {
		what = "apply --dry-run (planned only; nothing was launched)"
	}
	fmt.Printf("%s: formation %q -> channel %q\n", what, f.Name, f.Channel)
	for _, d := range rep.Drift {
		fmt.Printf("  DRIFT %s: saved %s, now %s — the snapshot is a cache, the ground is live; not blocking\n",
			d.Anchor, d.Saved, d.Now)
	}
	for _, r := range rep.Results {
		line := fmt.Sprintf("  %-16s %s", r.Alias, r.Outcome)
		if r.Detail != "" {
			line += " — " + r.Detail
		}
		fmt.Println(line)
		if r.Nonce != "" && !rep.DryRun {
			if r.Answered {
				fmt.Printf("  %-16s   answered its kickoff (round-trip verified)\n", "")
			} else if r.Outcome != client.OutcomeFailed {
				fmt.Printf("  %-16s   launched; not waiting for an answer (--wait 0)\n", "")
			}
		}
	}
	if rep.DryRun {
		fmt.Println("  (re-run without --dry-run to launch; the plan is built the same way either time)")
		return
	}
	if opts.Wait > 0 && !rep.Converged() {
		fmt.Println("  NOT converged: a peer never answered. apply reconciles — fix the cause and re-run it.")
	}
}

// runFormationBootstrap prints one peer's first-turn prompt and nothing else — the
// paste-it-yourself path for when a peer is launched by hand, or when apply cannot
// (another machine), or when someone wants to READ what a peer would be told before
// a fleet is opened.
//
// It composes through the same KickoffPrompt apply uses. A second renderer would
// drift, and the two drifting silently is how a peer gets briefed differently
// depending on who started it.
func runFormationBootstrap(args []string) int {
	const use = "usage: cbus formation bootstrap <name> <alias> [--brief TEXT]"
	if len(args) < 2 {
		return die(use)
	}
	name, alias := args[0], args[1]
	p, err := splitVerbArgs(args[2:], map[string]bool{"--brief": true}, nil, true)
	if err != nil {
		return die("%v (%s)", err, use)
	}
	if err := noExtra(p.pos, 0, use); err != nil {
		return die("%v", err)
	}
	brief, _ := p.has("--brief")
	f, err := client.LoadFormation(name)
	if err != nil {
		return die("%v", err)
	}
	prompt, err := client.BootstrapPeer(f, alias, brief)
	if err != nil {
		return die("%v", err)
	}
	fmt.Println(prompt)
	return 0
}

// ownChannel resolves the channel to save when none was given: this session's own.
// It refuses to guess between several rather than silently taking the first, the
// same way rename does.
func ownChannel() (string, error) {
	regs := client.ResolveSelf()
	switch len(regs) {
	case 0:
		return "", fmt.Errorf("not joined to a channel in this session — pass one")
	case 1:
		return regs[0].Channel, nil
	default:
		names := make([]string, 0, len(regs))
		for _, r := range regs {
			names = append(names, r.Channel)
		}
		return "", fmt.Errorf("joined to %d channels (%s) — pass one", len(regs), strings.Join(names, ", "))
	}
}

func runFormationList(args []string) int {
	if err := noExtra(args, 0, "usage: cbus formation list"); err != nil {
		return die("%v", err)
	}
	entries, err := client.ListFormations()
	if err != nil {
		return die("%v", err)
	}
	if len(entries) == 0 {
		fmt.Println("no formations saved")
		return 0
	}
	for _, e := range entries {
		if e.Err != nil {
			fmt.Printf("%-20s unreadable: %v\n", e.Name, e.Err)
			continue
		}
		fmt.Printf("%-20s channel=%-16s peers=%-3d saved=%s\n",
			e.Name, e.F.Channel, len(e.F.Peers), orQ(e.F.SavedAt))
	}
	return 0
}

func runFormationShow(args []string) int {
	const use = "usage: cbus formation show <name>"
	if len(args) == 0 {
		return die(use)
	}
	if err := noExtra(args, 1, use); err != nil {
		return die("%v", err)
	}
	f, err := client.LoadFormation(args[0])
	if err != nil {
		return die("%v", err)
	}
	renderFormation(f)
	return 0
}

func runFormationRm(args []string) int {
	const use = "usage: cbus formation rm <name>"
	if len(args) == 0 {
		return die(use)
	}
	if err := noExtra(args, 1, use); err != nil {
		return die("%v", err)
	}
	path, err := client.RemoveFormation(args[0])
	if err != nil {
		return die("%v", err)
	}
	fmt.Printf("removed formation %q (%s)\n", args[0], path)
	return 0
}

// renderFormation prints one envelope, flagging the two states that make a
// formation not applicable as written: a sid whose transcript is gone, and a peer
// with no brief to send. Warnings do NOT set exit 1 — show reports, it does not rule.
func renderFormation(f *client.Formation) {
	host := "local"
	if f.Host != nil && *f.Host != "" {
		host = *f.Host
	}
	fmt.Printf("formation: %s\n", f.Name)
	fmt.Printf("channel:   %s (%s)\n", f.Channel, host)
	fmt.Printf("saved:     %s by %s\n", orQ(f.SavedAt), orQ(f.SavedBy))
	if f.AnchorAlias != "" {
		fmt.Printf("anchor:    %s\n", f.AnchorAlias)
	}
	if len(f.DriftAnchors) > 0 {
		fmt.Println("drift_anchors:  (recorded at save — apply is what diffs them)")
		for _, k := range sortedRawKeys(f.DriftAnchors) {
			fmt.Printf("  %-10s %s\n", k+":", rawDisplay(f.DriftAnchors[k]))
		}
	}
	if len(f.Payload) > 0 && string(f.Payload) != "null" {
		fmt.Println("payload:   (opaque — cbus carries these references into briefs, never follows them)")
		var buf bytes.Buffer
		if json.Indent(&buf, f.Payload, "  ", "  ") == nil {
			fmt.Printf("  %s\n", buf.String())
		} else {
			fmt.Printf("  %s\n", f.Payload)
		}
	}

	stale, todo := 0, 0
	fmt.Printf("peers (%d):\n", len(f.Peers))
	for i := range f.Peers {
		p := &f.Peers[i]
		anchor := ""
		if p.Alias == f.AnchorAlias {
			anchor = "  [anchor]"
		}
		fmt.Printf("  %s%s\n", p.Alias, anchor)
		fmt.Printf("    model=%s mode=%s origin=%s target=%s machine=%s\n",
			orQ(p.Model), orQ(p.Mode), orQ(p.Origin), orQ(p.Target), orQ(p.Machine))
		switch {
		case p.Rolefile != "":
			fmt.Printf("    role:     %s\n", p.Rolefile)
		case p.RoleTODO():
			fmt.Printf("    role:     TODO — no committed rolefile and no usable role text; apply would brief this peer with nothing\n")
			todo++
		default:
			fmt.Printf("    role:     freeform text (%d bytes)\n", len(*p.Role))
		}
		state, detail := p.SidState()
		switch state {
		case client.SidPresent:
			fmt.Printf("    sid:      %s (transcript present)\n", p.SessionID)
		case client.SidStale:
			fmt.Printf("    sid:      %s  STALE — %s; resume/fork cannot run, onStale=%s applies\n",
				p.SessionID, detail, orDefault(p.OnStale, client.OnStaleTemplate))
			stale++
		case client.SidUnchecked:
			fmt.Printf("    sid:      %s  unchecked — %s\n", p.SessionID, detail)
		default:
			fmt.Printf("    sid:      none recorded\n")
		}
		for _, a := range p.Addresses {
			fmt.Printf("    address:  %s  (extra address — v1 apply prints it, does not arm it)\n", a)
		}
	}
	var warn []string
	if stale > 0 {
		warn = append(warn, fmt.Sprintf("%d stale sid(s)", stale))
	}
	if todo > 0 {
		warn = append(warn, fmt.Sprintf("%d role TODO(s)", todo))
	}
	if len(warn) > 0 {
		fmt.Printf("\nwarnings: %s\n", strings.Join(warn, ", "))
	}
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// rawDisplay unquotes a JSON string for display, leaving any other shape as-is.
func rawDisplay(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return string(r)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
