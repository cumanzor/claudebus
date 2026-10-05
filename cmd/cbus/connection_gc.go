package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"claudebus/internal/client"
)

const gcUsage = "usage: cbus connection gc [--dry-run] [--grace DURATION] [--older-than DURATION] [--json]  (DURATION like 15m, 36h or 14d)"

func runConnectionGC(args []string) int {
	p, err := splitVerbArgs(args, map[string]bool{"--grace": true, "--older-than": true}, map[string]bool{"--dry-run": true, "--json": true}, false)
	if err != nil {
		return die("%v (%s)", err, gcUsage)
	}
	if err := noExtra(p.pos, 0, gcUsage); err != nil {
		return die("%v", err)
	}
	settings, err := client.LoadGCSettings()
	if err != nil {
		return die("%v", err)
	}
	limits := settings.Limits
	for flag, into := range map[string]*time.Duration{"--grace": &limits.Grace, "--older-than": &limits.Inactive} {
		if v, ok := p.has(flag); ok {
			if *into, err = client.ParseGCDuration(v); err != nil {
				return die("%s: %v", flag, err)
			}
		}
	}
	if !p.flags["--dry-run"] {
		return collectConnections(limits, p.flags["--json"])
	}
	plan, err := client.PlanConnectionGC(limits)
	if err != nil {
		return die("%v", err)
	}
	if p.flags["--json"] {
		return printConnectionJSON(plan)
	}
	fmt.Print(renderGCPlan(plan))
	return 0
}

func renderGCPlan(plan client.GCPlan) string {
	var b strings.Builder
	counts := map[string]int{}
	unread := 0
	for _, r := range plan.Records {
		counts[r.Class]++
		if r.Class == client.GCCollect && r.Unread > 0 {
			unread++
		}
	}
	fmt.Fprintf(&b, "dry run, nothing removed: %d connection records; %s\n", len(plan.Records), gcLimitsText(plan.Limits))
	fmt.Fprintf(&b, "  live     %4d  consumer running, kept\n", counts[client.GCLive])
	fmt.Fprintf(&b, "  pending  %4d  consumer not confirmed gone; reconcile or abandon\n", counts[client.GCPending])
	fmt.Fprintf(&b, "  collect  %4d  %d with unread mail that would be exported first\n", counts[client.GCCollect], unread)
	fmt.Fprintf(&b, "  keep     %4d  inside the grace or under the inactivity limit\n", counts[client.GCKeep])
	if len(plan.Skipped) > 0 {
		fmt.Fprintf(&b, "  skipped  %4d  unreadable records: %s\n", len(plan.Skipped), strings.Join(plan.Skipped, ", "))
	}
	class := ""
	for _, r := range plan.Records {
		if r.Class != class {
			class = r.Class
			fmt.Fprintf(&b, "\n%s:\n", class)
		}
		line := fmt.Sprintf("  %-40s %-6s %-16s %s", r.Target, r.Harness, r.State, r.Reason)
		if r.Unread > 0 {
			line += fmt.Sprintf("; %d unread", r.Unread)
		}
		if r.InboxNote != "" {
			line += "; " + r.InboxNote
		}
		b.WriteString(line + "\n")
		if r.Next != "" {
			b.WriteString("      next: " + r.Next + "\n")
		}
	}
	return b.String()
}

func gcLimitsText(l client.GCLimits) string {
	return "grace " + gcLimit(l.Grace) + " after a consumer exits, " + gcLimit(l.Inactive) + " when none is on file"
}

func gcLimit(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return strings.TrimSuffix(strings.TrimSuffix(d.String(), "0s"), "0m")
}

// collectConnections asks the daemon, which owns every record in memory, to
// collect; editing the files behind a running daemon would race its lanes.
func collectConnections(limits client.GCLimits, asJSON bool) int {
	if err := ensureDaemon(); err != nil {
		return die("%v", err)
	}
	var pass client.GCPass
	err := client.DaemonCall("POST", "/gc", map[string]int64{"graceSeconds": int64(limits.Grace / time.Second), "inactiveSeconds": int64(limits.Inactive / time.Second)}, &pass)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return die("the running daemon predates connection gc; run cbus daemon restart, then retry")
		}
		return die("connection gc: %v", err)
	}
	if asJSON {
		return printConnectionJSON(pass)
	}
	fmt.Print(renderGCResults(pass, limits))
	return 0
}

func renderGCResults(pass client.GCPass, limits client.GCLimits) string {
	results := pass.Records
	var b strings.Builder
	var collected, left []client.GCResult
	counts := map[string]int{}
	for _, r := range results {
		switch {
		case r.Collected:
			collected = append(collected, r)
		case r.Class == client.GCCollect:
			left = append(left, r)
		default:
			counts[r.Class]++
		}
	}
	fmt.Fprintf(&b, "collected %d of %d connection records (%s); kept %d live, %d pending, %d inside a limit\n",
		len(collected), len(results), gcLimitsText(limits), counts[client.GCLive], counts[client.GCPending], counts[client.GCKeep])
	for _, r := range collected {
		line := fmt.Sprintf("  collected %-40s %s", r.Target, r.Reason)
		if r.Unread > 0 {
			line += fmt.Sprintf("; %d unread exported", r.Unread)
		}
		b.WriteString(line + "\n")
	}
	for _, r := range left {
		fmt.Fprintf(&b, "  left      %-40s %s\n", r.Target, r.Left)
	}
	if len(collected) > 0 {
		fmt.Fprintf(&b, "archived under %s (record, inbox folder, unread.jsonl); session tokens deleted\n", filepath.Dir(filepath.Dir(collected[0].Archive)))
	}
	if pass.OrphanTokens > 0 {
		fmt.Fprintf(&b, "removed %d session tokens no connection record references\n", pass.OrphanTokens)
	}
	if pass.TokenSweepOff != "" {
		b.WriteString("token sweep skipped: " + pass.TokenSweepOff + "\n")
	}
	if counts[client.GCPending] > 0 {
		b.WriteString("pending records are never collected; cbus connection gc --dry-run names the reconcile or abandon command for each\n")
	}
	return b.String()
}
