package client

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// PeerMeta is the subset of a peer's meta.json the read-only verbs render.
type PeerMeta struct {
	ListenerPid int // 0 if null/absent
	OwnerPid    int // 0 if null/absent
	Host        string
	Cwd         string
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
	inbox := filepath.Join(filepath.Dir(metaPath), "inbox.jsonl")
	if !psArgsContain(m.ListenerPid, inbox) {
		return false
	}
	if m.OwnerPid == 0 {
		return true
	}
	return pidAlive(m.OwnerPid)
}

// pidAlive is `kill -0`: the process exists (EPERM still means it exists).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// psArgsContain reports whether pid's full argv contains needle, via
// `ps -ww -p <pid> -o args=` (the -ww defeats argv truncation, bin/cbus:64).
func psArgsContain(pid int, needle string) bool {
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), needle)
}
