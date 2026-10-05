package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"claudebus/internal/client"
)

const gcUsage = "usage: cbus connection gc [--dry-run] [--older-than DURATION] [--json]  (DURATION like 14d or 36h)"

const gcDefaultOlderThan = 14 * 24 * time.Hour

func runConnectionGC(args []string) int {
	p, err := splitVerbArgs(args, map[string]bool{"--older-than": true}, map[string]bool{"--dry-run": true, "--json": true}, false)
	if err != nil {
		return die("%v (%s)", err, gcUsage)
	}
	if err := noExtra(p.pos, 0, gcUsage); err != nil {
		return die("%v", err)
	}
	olderThan := gcDefaultOlderThan
	if v, ok := p.has("--older-than"); ok {
		if olderThan, err = parseGCDuration(v); err != nil {
			return die("--older-than: %v", err)
		}
	}
	if !p.flags["--dry-run"] {
		return collectConnections(olderThan, p.flags["--json"])
	}
	plan, err := client.PlanConnectionGC(olderThan)
	if err != nil {
		return die("%v", err)
	}
	if p.flags["--json"] {
		return printConnectionJSON(plan)
	}
	fmt.Print(renderGCPlan(plan))
	return 0
}

// parseGCDuration accepts Go durations plus a whole-day form, since an
// inactivity limit is naturally counted in days.
func parseGCDuration(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not a positive number of days", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a positive duration", v)
	}
	return d, nil
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
	fmt.Fprintf(&b, "dry run, nothing removed: %d connection records, inactivity limit %s\n", len(plan.Records), gcLimit(plan.OlderThan))
	fmt.Fprintf(&b, "  live     %4d  consumer running, kept\n", counts[client.GCLive])
	fmt.Fprintf(&b, "  pending  %4d  never collected until reconciled or abandoned\n", counts[client.GCPending])
	fmt.Fprintf(&b, "  collect  %4d  %d with unread mail that would be exported first\n", counts[client.GCCollect], unread)
	fmt.Fprintf(&b, "  keep     %4d  no consumer, inactive under the limit\n", counts[client.GCKeep])
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

func gcLimit(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return strings.TrimSuffix(strings.TrimSuffix(d.String(), "0s"), "0m")
}

// collectConnections asks the daemon, which owns every record in memory, to
// collect; editing the files behind a running daemon would race its lanes.
func collectConnections(olderThan time.Duration, asJSON bool) int {
	if err := ensureDaemon(); err != nil {
		return die("%v", err)
	}
	var pass client.GCPass
	err := client.DaemonCall("POST", "/gc", map[string]int64{"olderThanSeconds": int64(olderThan / time.Second)}, &pass)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return die("the running daemon predates connection gc; run cbus daemon restart, then retry")
		}
		return die("connection gc: %v", err)
	}
	if asJSON {
		return printConnectionJSON(pass)
	}
	fmt.Print(renderGCResults(pass, olderThan))
	return 0
}

func renderGCResults(pass client.GCPass, olderThan time.Duration) string {
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
	fmt.Fprintf(&b, "collected %d of %d connection records (inactivity limit %s); kept %d live, %d pending, %d under the limit\n",
		len(collected), len(results), gcLimit(olderThan), counts[client.GCLive], counts[client.GCPending], counts[client.GCKeep])
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
