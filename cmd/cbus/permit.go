package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"claudebus/internal/client"
)

const (
	permitUse            = `usage: cbus permit <actions> --repo R (--ref P | --target T | --effort TEXT) --says WORDS [--ttl D] [--channel C] | cbus permit revoke <id>`
	permitsUse           = "usage: cbus permits [--all] [--json] | cbus permits use <action> (--ref B | --target T) [--repo R] [--channel C]"
	permitExitNo         = 1
	permitExitBadRequest = 2
)

func permitUsageExit(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "cbus: "+format+"\n", a...)
	return permitExitBadRequest
}

func parsePermitTTL(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("--ttl: want a duration like 12h or 7d, got %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--ttl: want a duration like 12h or 7d, got %q", s)
	}
	return d, nil
}

// permitExit maps a client error to its exit code and message.
func permitExit(err error) int {
	var usage client.PermitUsageError
	if errors.As(err, &usage) {
		return permitUsageExit("%v", err)
	}
	fmt.Fprintln(os.Stderr, err)
	return permitExitNo
}

func permitChannel(p parsedArgs) (string, error) {
	if ch, ok := p.opts["--channel"]; ok {
		return ch, nil
	}
	return ownChannel()
}

func runPermit(args []string) int {
	if len(args) > 0 && args[0] == "revoke" {
		return runPermitRevoke(args[1:])
	}
	if len(args) == 0 {
		return permitUsageExit(permitUse)
	}
	p, err := splitVerbArgs(args[1:], map[string]bool{"--repo": true, "--ref": true, "--target": true, "--effort": true,
		"--says": true, "--ttl": true, "--channel": true}, nil, true)
	if err != nil {
		return permitUsageExit("%v (%s)", err, permitUse)
	}
	if len(p.pos) > 0 {
		return permitUsageExit("unexpected %q (%s)", p.pos[0], permitUse)
	}
	req := client.PermitRequest{Actions: strings.Split(args[0], ","), Repo: p.opts["--repo"], Ref: p.opts["--ref"],
		Target: p.opts["--target"], Effort: p.opts["--effort"], Says: p.opts["--says"]}
	if v, ok := p.opts["--ttl"]; ok {
		if req.TTL, err = parsePermitTTL(v); err != nil {
			return permitUsageExit("%v", err)
		}
	}
	if req.Channel, err = permitChannel(p); err != nil {
		return permitUsageExit("%v", err)
	}
	permit, err := client.NewPermit(req)
	if err != nil {
		return permitExit(err)
	}
	if err := client.WritePermit(permit); err != nil {
		return permitExit(err)
	}
	fmt.Printf("%s: %s, until %s, written by %s\n", permit.ID, permitScope(permit), permit.ExpiresAt.Format(time.RFC3339), permit.Writer)
	return 0
}

func permitScope(p client.Permit) string {
	s := strings.Join(p.Actions, ",")
	switch {
	case p.Target != "":
		s += " target " + p.Target
	case p.Ref != "":
		s += " ref " + p.Ref
	}
	return s + " on " + p.Repo
}

func runPermitRevoke(args []string) int {
	const use = "usage: cbus permit revoke <id>"
	if len(args) != 1 {
		return permitUsageExit(use)
	}
	by := "unregistered"
	if regs := client.ResolveSelf(); len(regs) > 0 {
		by = regs[0].Channel + "/" + regs[0].Alias
	}
	v, err := client.RevokePermit(args[0], by)
	if err != nil {
		return permitExit(err)
	}
	fmt.Printf("revoked %s (%s) by %s\n", v.ID, permitScope(v.Permit), v.RevokedBy)
	return 0
}

func runPermits(args []string) int {
	if len(args) > 0 && args[0] == "use" {
		return runPermitsUse(args[1:])
	}
	p, err := splitVerbArgs(args, nil, map[string]bool{"--all": true, "--json": true}, true)
	if err != nil {
		return permitUsageExit("%v (%s)", err, permitsUse)
	}
	if len(p.pos) > 0 {
		return permitUsageExit("unexpected %q (%s)", p.pos[0], permitsUse)
	}
	regs := client.ResolveSelf()
	if !p.flags["--all"] && len(regs) == 0 {
		return permitUsageExit("not joined in this session: cbus permits lists yours (--all lists every permit)")
	}
	views := client.ListPermits(regs, p.flags["--all"])
	if p.flags["--json"] {
		if views == nil {
			views = []client.PermitView{}
		}
		b, err := json.MarshalIndent(views, "", "  ")
		if err != nil {
			return die("%v", err)
		}
		fmt.Println(string(b))
		return 0
	}
	if len(views) == 0 {
		fmt.Println("no permits")
		return 0
	}
	for _, v := range views {
		fmt.Printf("%s  %-7s  %s  %s  until %s  by %s  uses %d\n", v.ID, v.State, v.Channel, permitScope(v.Permit), v.ExpiresAt.Format(time.RFC3339), v.Writer, v.Uses)
		if v.Effort != "" {
			fmt.Printf("    effort: %s\n", v.Effort)
		}
		if p.flags["--all"] {
			fmt.Printf("    says: %s\n", v.Says)
		}
	}
	return 0
}

func runPermitsUse(args []string) int {
	if len(args) == 0 {
		return permitUsageExit(permitsUse)
	}
	p, err := splitVerbArgs(args[1:], map[string]bool{"--ref": true, "--target": true, "--repo": true, "--channel": true}, nil, true)
	if err != nil {
		return permitUsageExit("%v (%s)", err, permitsUse)
	}
	if len(p.pos) > 0 {
		return permitUsageExit("unexpected %q (%s)", p.pos[0], permitsUse)
	}
	use := client.PermitUse{Action: args[0], Ref: p.opts["--ref"], Target: p.opts["--target"], Repo: p.opts["--repo"]}
	if use.Channel, err = permitChannel(p); err != nil {
		return permitUsageExit("%v", err)
	}
	if use.Repo == "" {
		if use.Repo, err = client.OriginRepo(); err != nil {
			return permitUsageExit("%v", err)
		}
	}
	v, err := client.UsePermit(use)
	if err != nil {
		return permitExit(err)
	}
	checked := "1 time"
	if v.Uses != 1 {
		checked = fmt.Sprintf("%d times", v.Uses)
	}
	fmt.Printf("covered by %s: %s, until %s (not consumed; checked %s)\n", v.ID, permitScope(v.Permit), v.ExpiresAt.Format(time.RFC3339), checked)
	return 0
}
