package client

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// Uses ps for parity with the bash client on both darwin and linux.
func OwnerPID() (int, bool) {
	p := os.Getppid()
	for depth := 0; p > 1 && depth < 16; depth++ {
		comm := psField(p, "comm")
		if base := comm[strings.LastIndexByte(comm, '/')+1:]; base == "claude" || strings.HasPrefix(base, "claude-") {
			return p, true
		}
		next, err := strconv.Atoi(psField(p, "ppid"))
		if err != nil || next <= 0 {
			break
		}
		p = next
	}
	return 0, false
}

// psField runs `ps -p <pid> -o <field>=` (header suppressed) and trims the result.
func psField(pid int, field string) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", field+"=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
