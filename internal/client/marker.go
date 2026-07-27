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
// ownerPid tracks the SESSION's liveness (find_owner_pid, bin/cbus:44-53).
// Uses the platform procParent (sysctl on darwin, procfs on linux) — no ps spawn.
func OwnerPID() (int, bool) {
	return ownerFromPid(os.Getppid())
}

// ownerFromPid walks up from pid: the first ancestor within 16 hops that IS a
// coding-harness session. Identity is argv[0]'s basename (see isHarnessComm), NOT the
// kernel comm — the bun-compiled Claude CLI sets its accounting name to its version
// string (ucomm "2.1.214"), which starved every Go-era registration of its
// ownerPid until close's false-success exposed it. comm is kept as a fallback
// for any build where it still reads a real harness name.
func ownerFromPid(pid int) (int, bool) {
	p := pid
	for depth := 0; p > 1 && depth < 16; depth++ {
		comm, ppid, err := procParent(p)
		if err != nil {
			return 0, false
		}
		if isHarnessComm(commBase(comm)) {
			return p, true
		}
		if argv, aerr := procArgs(p); aerr == nil {
			if f := strings.Fields(argv); len(f) > 0 && isHarnessComm(commBase(f[0])) {
				return p, true
			}
		}
		if ppid <= 1 {
			break
		}
		p = ppid
	}
	return 0, false
}

// commBase is the basename of a command path/name (the segment after the last '/'),
// so identity is argv[0]-shaped and immune to a leading path.
func commBase(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// isHarnessComm reports whether base (a command basename) names a known coding-harness
// session process: claude / claude-* (Claude Code), grok and xai-grok-pager (grok CLI),
// opencode, or codex. Exact match on the set plus the claude-* prefix — argv[0]-shaped,
// immune to prompt text elsewhere in argv. Codex npm installs exec the native `codex`
// child under a node shim, and the ancestor walk hits codex before node, so an exact
// entry suffices (node and grokd stay false: the walk stops at the real session, and a
// suffix like grokd is not the harness).
//
// On windows two different binaries share the basename claude.exe: the CLI at
// .local\bin and the desktop Electron app at AppData\Local\AnthropicClaude. A basename
// cannot separate them, and this deliberately does not try — both normalize to claude,
// so the answer is the same either way. The case where the ambiguity could have bitten
// is a walk that had already crossed into an unrelated process tree, and
// ancestryPlausible stops that before identity is ever consulted.
func isHarnessComm(base string) bool {
	switch base {
	case "claude", "grok", "xai-grok-pager", "opencode", "codex":
		return true
	}
	return strings.HasPrefix(base, "claude-")
}
