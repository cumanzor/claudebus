package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Now is the client timestamp: UTC ISO-8601 to the second with a Z suffix
// (bin/cbus:20, `date -u +%Y-%m-%dT%H:%M:%SZ`).
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

// remoteMarker is the on-disk identity marker
// (.remote/<host>/<channel>/<sessionId>). Field order alias, ownerPid, ts is the
// A3 contract — its pretty-printed bytes must match the bash client's
// json.dump(indent=2) (bin/cbus:281-284).
type remoteMarker struct {
	Alias    string `json:"alias"`
	OwnerPid int    `json:"ownerPid"`
	TS       string `json:"ts"`
}

// marshalMarker renders a marker as indent=2 JSON (python json.dump parity).
func marshalMarker(alias string, ownerPid int, ts string) ([]byte, error) {
	return json.MarshalIndent(remoteMarker{Alias: alias, OwnerPid: ownerPid, TS: ts}, "", "  ")
}

// WriteRemoteMarker writes THIS session's identity marker for (host, channel),
// claiming alias as its default `from`. Replaces a legacy machine-global FILE
// marker at the channel path first (bin/cbus:278-284). ownerPid is the owning
// claude pid, or PPID when no claude ancestor is found (the documented
// sweep-bait fallback).
func WriteRemoteMarker(host, channel, alias string) error {
	dir := filepath.Join(CBUSDir(), ".remote", host, channel)
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		if err := os.Remove(dir); err != nil { // legacy machine-global marker: unowned, replace
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	owner := os.Getppid()
	if p, ok := OwnerPID(); ok {
		owner = p
	}
	b, err := marshalMarker(alias, owner, Now())
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, markerSID()), b, 0o644)
}

// OwnerPID walks up from PPID to the owning claude session pid so a marker's
// ownerPid tracks the SESSION's liveness (find_owner_pid, bin/cbus:44-53): the
// first ancestor within 16 hops whose command basename is "claude" or "claude-*".
// Uses the platform procParent (sysctl on darwin, procfs on linux) — no ps spawn.
func OwnerPID() (int, bool) {
	p := os.Getppid()
	for depth := 0; p > 1 && depth < 16; depth++ {
		comm, ppid, err := procParent(p)
		if err != nil {
			return 0, false
		}
		base := comm
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if base == "claude" || strings.HasPrefix(base, "claude-") {
			return p, true
		}
		if ppid <= 1 {
			break
		}
		p = ppid
	}
	return 0, false
}
