package client

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// unarmedGrace is the window a joined-but-never-armed peer survives before prune
// may sweep it — long enough that join's auto-prune can't reap a sibling
// mid-setup (bin/cbus:319, `find -mmin +10`).
const unarmedGrace = 10 * time.Minute

// PeerMeta is the subset of a peer's meta.json the read-only verbs render.
// Alias/SessionID are read for formation save, which captures a channel's roster.
type PeerMeta struct {
	ListenerPid   int    // 0 if null/absent
	OwnerPid      int    // 0 if null/absent
	ListenerStart string // "" if absent — a pre-P3 arm, which now reads dead
	Host          string
	Cwd           string
	Alias         string
	SessionID     string
	Origin        string // birth-record (cbus-m9l); "" when a pre-m9l/bash meta omits it
	Model         string
}

// ReadPeerMeta reads a peer's meta.json tolerantly (a torn/missing file yields
// ok=false — the same read-tolerance jget relies on).
func ReadPeerMeta(metaPath string) (PeerMeta, bool) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return PeerMeta{}, false
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return PeerMeta{}, false
	}
	return PeerMeta{
		ListenerPid:   rawInt(raw["listenerPid"]),
		OwnerPid:      rawInt(raw["ownerPid"]),
		ListenerStart: rawStr(raw["listenerStart"]),
		Host:          rawStr(raw["host"]),
		Cwd:           rawStr(raw["cwd"]),
		Alias:         rawStr(raw["alias"]),
		SessionID:     rawStr(raw["sessionId"]),
		Origin:        rawStr(raw["origin"]),
		Model:         rawStr(raw["model"]),
	}, true
}

// rawInt tolerantly reads a JSON int, digit-string, or null (-> 0).
func rawInt(r json.RawMessage) int {
	if len(r) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(r, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return v
		}
	}
	return 0
}

// rawStr tolerantly reads a JSON string (falling back to the raw token).
func rawStr(r json.RawMessage) string {
	if len(r) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return strings.Trim(string(r), `"`)
}

// MetaListenerAlive is the three-clause liveness predicate (bin/cbus:58-68): the
// recorded listenerPid exists AND that process is still the one that armed
// (pid-recycling guard, see listenerIdentityHolds) AND the ownerPid, if recorded, is
// alive (crash-orphan guard). Any missing clause => not listening.
func MetaListenerAlive(metaPath string) bool {
	m, ok := ReadPeerMeta(metaPath)
	if !ok || m.ListenerPid == 0 || !pidAlive(m.ListenerPid) {
		return false
	}
	if !listenerIdentityHolds(m, metaPath) {
		return false
	}
	if m.OwnerPid == 0 {
		return true
	}
	return pidAlive(m.OwnerPid)
}

// listenerIdentityHolds answers the pid-recycling question: is the process at
// listenerPid still the process that armed this peer, or a stranger wearing a reused
// pid? It is the one place that answers it, so the predicate and close.go's owner
// guard can never drift into disagreeing about who a listener is.
//
// There is exactly ONE way to answer it: the recorded witness against the process now
// wearing the pid. The argv-grep fallback that answered for pre-P3 metas is deleted, so
// a meta with no witness has nothing to be judged on and reads dead — do not add a
// second opinion here, an `or` would resurrect the recycled pids this rejects.
func listenerIdentityHolds(m PeerMeta, metaPath string) bool {
	// A zombie is EXITED but unreaped, and on linux it defeats the structural witness
	// on its own terms: /proc/<pid>/stat stays readable at state=Z with the ORIGINAL
	// starttime, and kill -0 still succeeds, so the recorded token byte-matches a
	// process that is no longer listening. The deleted argv clause caught this for free
	// (a zombie's cmdline is empty) and zombie=dead is a pinned edge, so the guard is
	// explicit here rather than left to a platform accident — on darwin proc_pidinfo
	// happens to error for a zombie, which is safe but is not a decision.
	//
	// It sits above the branch so BOTH callers inherit it: the predicate and close.go's
	// owner guard must not disagree about whether a corpse is a listener.
	if procZombie(m.ListenerPid) {
		return false
	}
	if m.ListenerStart == "" {
		return false // no witness, no identity: R1's posture for a stampless meta
	}
	cur, err := procStartTime(m.ListenerPid)
	if err != nil {
		return false // R2: a proc probe that cannot answer reads DEAD
	}
	return cur == m.ListenerStart
}

// pidAlive is `kill -0`: the process exists (EPERM still means it exists).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// PeerDead is the prune / broadcast-recipient / send-gate predicate (bin/cbus:316-323):
// a never-armed peer (null listenerPid, or a torn "field absent" read) is dead only
// once past the unarmed grace window; an armed-ever peer is dead iff its listener is
// no longer alive.
func PeerDead(metaPath string) bool {
	if m, ok := ReadPeerMeta(metaPath); ok && m.ListenerPid != 0 {
		return !MetaListenerAlive(metaPath)
	}
	return unarmedGraceElapsed(metaPath)
}

// unarmedGraceElapsed reports whether a never-armed peer is past the grace window,
// judged by lastActivity alone (P3: the bash-era mtime fallback is deleted). A
// missing or unreadable meta is not dead (a read error must not prune); a readable
// meta with no parseable stamp is past grace by definition — every Go join writes
// the stamp, so stampless means a pre-port relic or a damaged file, both prunable.
func unarmedGraceElapsed(metaPath string) bool {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	if ts, ok := parseLastActivity(b); ok {
		return time.Since(ts) > unarmedGrace
	}
	return true
}

// parseLastActivity extracts the lastActivity timestamp from raw meta.json bytes.
// The store writes the frozen "2006-01-02T15:04:05Z" layout; parsing accepts any
// RFC3339 form so a format drift can never read a live peer as stampless-dead.
func parseLastActivity(b []byte) (time.Time, bool) {
	var m struct {
		LastActivity string `json:"lastActivity"`
	}
	if json.Unmarshal(b, &m) != nil || m.LastActivity == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, m.LastActivity)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
