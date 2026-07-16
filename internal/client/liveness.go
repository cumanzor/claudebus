package client

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	ListenerPid int // 0 if null/absent
	OwnerPid    int // 0 if null/absent
	Host        string
	Cwd         string
	Alias       string
	SessionID   string
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
		ListenerPid: rawInt(raw["listenerPid"]),
		OwnerPid:    rawInt(raw["ownerPid"]),
		Host:        rawStr(raw["host"]),
		Cwd:         rawStr(raw["cwd"]),
		Alias:       rawStr(raw["alias"]),
		SessionID:   rawStr(raw["sessionId"]),
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
// recorded listenerPid exists AND that process's argv still references the inbox
// (pid-recycling guard) AND the ownerPid, if recorded, is alive (crash-orphan
// guard). Any missing clause => not listening.
func MetaListenerAlive(metaPath string) bool {
	m, ok := ReadPeerMeta(metaPath)
	if !ok || m.ListenerPid == 0 || !pidAlive(m.ListenerPid) {
		return false
	}
	if !argvContains(m.ListenerPid, metaInboxNeedle(metaPath)) {
		return false
	}
	if m.OwnerPid == 0 {
		return true
	}
	return pidAlive(m.OwnerPid)
}

// COMPAT(P3 #2): raw inbox spelling (needle half) — deletes with argv-grep liveness.
// metaInboxNeedle is the inbox path argvContains greps for — the SECOND Decision 2
// compat surface (F1). It MUST be the raw bash inbox_path() spelling so it matches a
// live follower's --inbox argv under ANY CBUS_DIR spelling: bash writes and greps the
// raw spelling, so a filepath.Join(dir,"inbox.jsonl") needle (cleaned) would miss a
// live bash follower under a trailing-slash CBUS_DIR, and a bash needle would miss a
// live Go follower. Rebuilt from the RAW CBUS_DIR + the peer's subpath (recovered by
// stripping the cleaned CBUS_DIR prefix off the cleaned meta dir), NOT from the
// already-cleaned metaPath — so the CBUS_DIR spelling is preserved. Handles legacy v1
// ($CBUS_DIR/<ch>/meta.json, rel="/<ch>") and v2 ($CBUS_DIR/<ch>/<al>/meta.json,
// rel="/<ch>/<al>") alike; for a v2 peer it equals compatInboxPath(CBUS_DIR, ch, al),
// the same string the arm puts in the follower's argv.
func metaInboxNeedle(metaPath string) string {
	dir := filepath.Dir(metaPath)
	rel := strings.TrimPrefix(dir, filepath.Clean(CBUSDir()))
	if rel == dir { // metaPath not under CBUS_DIR (shouldn't happen) — best-effort raw
		return dir + "/inbox.jsonl"
	}
	return CBUSDir() + rel + "/inbox.jsonl"
}

// pidAlive is `kill -0`: the process exists (EPERM still means it exists).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// argvContains reports whether pid's argv contains needle, read via the platform
// procArgs (sysctl KERN_PROCARGS2 on darwin, /proc/<pid>/cmdline on linux — no ps
// spawn). A read error — ESRCH/EPERM, or a darwin zombie (whose args EINVAL out of
// KERN_PROCARGS2 on current kernels; procZombie is a belt-and-braces hedge, F1) —
// returns false, so the argv clause reads DEAD with no invented leniency, matching
// `ps -o args=` going empty / "<defunct>" (Decision 1 condition iii, edge D1).
func argvContains(pid int, needle string) bool {
	args, err := procArgs(pid)
	if err != nil {
		return false
	}
	return strings.Contains(args, needle)
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

// unarmedGraceElapsed reports whether a never-armed peer is past the grace window.
// It prefers the dual-written lastActivity field and falls back to the meta file's
// mtime. COMPAT(P3 #3): the mtime fallback bridges bash-written peers (bash never
// wrote lastActivity) — it deletes at homogenization (-> lastActivity-only). A missing
// meta is not dead.
func unarmedGraceElapsed(metaPath string) bool {
	if ts, ok := lastActivity(metaPath); ok {
		return time.Since(ts) > unarmedGrace
	}
	fi, err := os.Stat(metaPath)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) > unarmedGrace
}

// lastActivity reads the dual-written lastActivity timestamp from a meta.json.
func lastActivity(metaPath string) (time.Time, bool) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return time.Time{}, false
	}
	var m struct {
		LastActivity string `json:"lastActivity"`
	}
	if json.Unmarshal(b, &m) != nil || m.LastActivity == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse("2006-01-02T15:04:05Z", m.LastActivity)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
