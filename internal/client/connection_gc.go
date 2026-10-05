package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Dry-run classes, in the order a report lists them.
const (
	GCLive    = "live"
	GCPending = "pending"
	GCCollect = "collect"
	GCKeep    = "keep"
)

// GCRecord is what a collection would do with one connection record. Planning
// only reads: nothing here changes a record, an inbox or a credential.
type GCRecord struct {
	ID         string    `json:"id"`
	Target     string    `json:"target"`
	Harness    string    `json:"harness"`
	State      string    `json:"state"`
	Class      string    `json:"class"`
	Reason     string    `json:"reason"`
	LastActive time.Time `json:"lastActive,omitzero"`
	Unread     int       `json:"unread"`
	InboxNote  string    `json:"inboxNote,omitempty"`
	Next       string    `json:"next,omitempty"`
}

type GCPlan struct {
	OlderThan time.Duration `json:"olderThanSeconds"`
	Records   []GCRecord    `json:"records"`
	Skipped   []string      `json:"skipped,omitempty"`
}

type gcProbe struct {
	now   time.Time
	alive func(pid int, start string) bool
	mtime func(path string) (time.Time, bool)
}

// PlanConnectionGC classifies every stored connection record. A record is never
// collected while it has a pending attempt or a running consumer; a detached one
// (leave or unregister) is collectable now; any other is collectable once its
// session has been inactive longer than olderThan.
func PlanConnectionGC(olderThan time.Duration) (GCPlan, error) {
	return planConnectionGC(DaemonDir(), CBUSDir(), olderThan, gcProbe{
		now: time.Now(),
		alive: func(pid int, start string) bool {
			got, err := procStartTime(pid)
			return err == nil && got == start
		},
		mtime: func(path string) (time.Time, bool) {
			info, err := os.Stat(path)
			if err != nil {
				return time.Time{}, false
			}
			return info.ModTime(), true
		},
	})
}

func planConnectionGC(daemonRoot, busRoot string, olderThan time.Duration, p gcProbe) (GCPlan, error) {
	plan := GCPlan{OlderThan: olderThan}
	if olderThan <= 0 {
		return plan, errors.New("the inactivity limit must be positive")
	}
	dir := filepath.Join(daemonRoot, "connections")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return plan, nil
	}
	if err != nil {
		return plan, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		c, err := readConnectionRecord(dir, e.Name())
		if err != nil {
			plan.Skipped = append(plan.Skipped, e.Name())
			continue
		}
		r := classifyGC(c, filepath.Join(dir, e.Name()), olderThan, p)
		r.Unread, r.InboxNote = gcUnread(c, daemonRoot, busRoot)
		plan.Records = append(plan.Records, r)
	}
	order := map[string]int{GCLive: 0, GCPending: 1, GCCollect: 2, GCKeep: 3}
	sort.SliceStable(plan.Records, func(i, j int) bool {
		a, b := plan.Records[i], plan.Records[j]
		if order[a.Class] != order[b.Class] {
			return order[a.Class] < order[b.Class]
		}
		return a.Target < b.Target
	})
	return plan, nil
}

func classifyGC(c *ConnectionState, record string, olderThan time.Duration, p gcProbe) GCRecord {
	r := GCRecord{ID: c.ID, Target: ConnectionTarget(c), Harness: c.Harness, State: c.State, LastActive: gcLastActive(c, record, p)}
	if r.Harness == "" {
		r.Harness = daemonHarnessCodex
	}
	idle := p.now.Sub(r.LastActive).Round(time.Hour)
	switch {
	case c.Pending != nil:
		r.Class, r.Reason = GCPending, "has a pending delivery attempt; never collected while it is unresolved"
		r.Next = "cbus connection reconcile " + r.Target + "  (then, if still pending: cbus connection abandon " + r.Target + " --pending " + c.Pending.ClientID + " --reason <text>)"
	case gcConsumerAlive(c, p):
		r.Class, r.Reason = GCLive, "its consumer process is running"
		if c.State == "disconnected" || c.State == "detached" {
			r.Reason += " (this record is " + c.State + ")"
		}
	case c.State == "detached":
		r.Class, r.Reason = GCCollect, "detached by leave or unregister"
	case r.LastActive.IsZero():
		r.Class, r.Reason = GCCollect, "no consumer and no record of activity"
	case idle > olderThan:
		r.Class, r.Reason = GCCollect, "no consumer; inactive "+gcDays(idle)
	default:
		r.Class, r.Reason = GCKeep, "no consumer; inactive "+gcDays(idle)+", under the limit"
	}
	return r
}

// gcConsumerAlive trusts a pid only with the start time recorded beside it, so a
// reused pid or a stale "online" observation never keeps a record.
func gcConsumerAlive(c *ConnectionState, p gcProbe) bool {
	if o := c.Consumer; o != nil && o.PID > 0 && o.StartToken != "" && p.alive(o.PID, o.StartToken) {
		return true
	}
	if c.Claude != nil {
		e := c.Claude.Binding.Endpoint
		return e.PID > 0 && e.StartToken != "" && p.alive(e.PID, e.StartToken)
	}
	return false
}

// gcLastActive is when the session itself last wrote: its Claude transcript or
// Codex rollout. The record file is a last resort, since every daemon probe
// rewrites it.
func gcLastActive(c *ConnectionState, record string, p gcProbe) time.Time {
	var last time.Time
	for _, path := range []string{c.RolloutPath, claudeTranscriptOf(c)} {
		if t, ok := p.mtime(path); path != "" && ok && t.After(last) {
			last = t
		}
	}
	if a := c.LastAccepted; a != nil {
		if t, err := time.Parse(time.RFC3339Nano, a.ObservedAt); err == nil && t.After(last) {
			last = t
		}
	}
	if last.IsZero() {
		last, _ = p.mtime(record)
	}
	return last
}

func claudeTranscriptOf(c *ConnectionState) string {
	if c.Claude == nil {
		return ""
	}
	return c.Claude.Binding.TranscriptPath
}

// gcUnread counts non-presence messages past the delivered offset, which a
// collection would export first. The inbox counts only while this record still
// owns it: a reconnect after a detach reuses the folder under a new id.
func gcUnread(c *ConnectionState, daemonRoot, busRoot string) (int, string) {
	dir := filepath.Join(busRoot, c.Channel, c.Alias)
	if c.Relay != nil {
		dir = filepath.Join(daemonRoot, "remote", c.ID)
	}
	if m, ok := ReadPeerMeta(filepath.Join(dir, "meta.json")); ok && m.ConnectionID != "" && m.ConnectionID != c.ID {
		return 0, "inbox now belongs to connection " + m.ConnectionID
	}
	inbox := filepath.Join(dir, "inbox.jsonl")
	f, err := openSharedRead(inbox)
	if os.IsNotExist(err) {
		return 0, "no inbox"
	}
	if err != nil {
		return 0, "inbox unreadable"
	}
	defer f.Close()
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok || !devMatches(c.Dev, dev) || ino != c.Ino {
		return 0, "inbox replaced since this record; unread unknown"
	}
	if size <= c.Offset {
		return 0, ""
	}
	if _, err := f.Seek(c.Offset, 0); err != nil {
		return 0, "inbox unreadable"
	}
	n := 0
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	for scan.Scan() {
		var m struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(scan.Bytes(), &m) == nil && m.Kind != "presence" {
			n++
		}
	}
	return n, ""
}

func gcDays(d time.Duration) string {
	if d < 48*time.Hour {
		return d.String()
	}
	return fmt.Sprintf("%dd", d/(24*time.Hour))
}
