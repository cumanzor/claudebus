package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"claudebus/internal/client"
)

// formationUsage advertises only the verbs that exist. save/apply/bootstrap land
// in later milestones and are absent on purpose — a help text that promises a verb
// the binary does not have is a bug report waiting to happen.
const formationUsage = "usage: cbus formation list | show <name> | rm <name>"

func runFormation(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
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
