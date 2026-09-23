package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"claudebus/internal/core"
)

// CBUSDir is the local bus state root: $CBUS_DIR or ~/.claude-bus (bin/cbus:16).
func CBUSDir() string {
	if d := os.Getenv("CBUS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude-bus"
	}
	return filepath.Join(home, ".claude-bus")
}

// sessionOverride is the in-process session id set by OverrideSessionID. It outranks
// every env var in SessionID(): a hook or a --session-id caller names the session it
// acts as, and a stray exported CBUS_SESSION_ID must not shadow that. Not goroutine-safe
// — the CLI is single-threaded per invocation and the hooks call it straight-line.
var sessionOverride string

// OverrideSessionID pins the in-process session id to sid and returns a func that
// restores the previous value. It outranks all env vars in SessionID()'s lookup. An
// empty sid is a no-op override (SessionID falls back to the env chain), so a caller
// with nothing to pin can call it unconditionally.
func OverrideSessionID(sid string) (restore func()) {
	prev := sessionOverride
	sessionOverride = sid
	return func() { sessionOverride = prev }
}

// SessionID is this session's id by an ordered lookup: the in-process override
// (OverrideSessionID / the --session-id flag), then $CBUS_SESSION_ID (harness-neutral),
// then $CLAUDE_CODE_SESSION_ID (Claude Code), then $GROK_SESSION_ID (grok), then
// $CODEX_THREAD_ID (Codex). Preserve the established overrides: an inherited Codex
// thread id must not replace a child harness's own session id. Codex connect separately
// refuses conflicting inherited identities before it registers the current thread.
// Empty when none is set — the sessionless mode, where identity lookups yield nothing.
func SessionID() string {
	if sessionOverride != "" {
		return sessionOverride
	}
	for _, k := range []string{"CBUS_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "GROK_SESSION_ID", "CODEX_THREAD_ID"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// LocalReg is one channel/alias this session is registered under.
type LocalReg struct {
	Channel string
	Alias   string
}

// ResolveSelf returns every registration whose meta.json records this session's
// id, in $CBUS_DIR/<channel>/<alias> glob order (channel-major, alphabetical) —
// the order find_peer_channel's first-match depends on. Dot-prefixed entries
// (.remote, .reap.*) are skipped, matching the client's `*/` glob blindness. No
// session id => no registrations (bin/cbus:92-104).
func ResolveSelf() []LocalReg {
	sid := SessionID()
	if sid == "" {
		return nil
	}
	root := CBUSDir()
	channels, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []LocalReg
	for _, ch := range channels {
		if !ch.IsDir() || strings.HasPrefix(ch.Name(), ".") {
			continue
		}
		aliases, err := os.ReadDir(filepath.Join(root, ch.Name()))
		if err != nil {
			continue
		}
		for _, al := range aliases {
			if !al.IsDir() || strings.HasPrefix(al.Name(), ".") {
				continue
			}
			meta := filepath.Join(root, ch.Name(), al.Name(), "meta.json")
			if metaSessionID(meta) == sid {
				out = append(out, LocalReg{Channel: ch.Name(), Alias: al.Name()})
			}
		}
	}
	return out
}

// RemoteReg is one of this session's remote identity markers.
type RemoteReg struct {
	Channel string
	Host    string
	Alias   string
}

// SessionMarkers returns this session's remote from-default markers
// (.remote/<host>/<channel>/<sessionId> with a non-empty alias), in host-major
// then channel glob order (bin/cbus:781-790).
func SessionMarkers() []RemoteReg {
	sid := markerSID()
	root := filepath.Join(CBUSDir(), ".remote")
	hosts, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []RemoteReg
	for _, h := range hosts {
		if !h.IsDir() || strings.HasPrefix(h.Name(), ".") {
			continue
		}
		chans, err := os.ReadDir(filepath.Join(root, h.Name()))
		if err != nil {
			continue
		}
		for _, c := range chans {
			if !c.IsDir() || strings.HasPrefix(c.Name(), ".") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, h.Name(), c.Name(), sid))
			if err != nil {
				continue
			}
			var m struct {
				Alias string `json:"alias"`
			}
			if json.Unmarshal(b, &m) == nil && m.Alias != "" {
				out = append(out, RemoteReg{Channel: c.Name(), Host: h.Name(), Alias: m.Alias})
			}
		}
	}
	return out
}

// FindPeerChannel resolves a bare alias to the first of THIS session's channels
// (glob order) that holds a peer with that alias (bin/cbus:107-114).
func FindPeerChannel(alias string) (string, bool) {
	root := CBUSDir()
	for _, reg := range ResolveSelf() {
		if _, err := os.Stat(filepath.Join(root, reg.Channel, alias, "meta.json")); err == nil {
			return reg.Channel, true
		}
	}
	return "", false
}

// markerSID is this session's remote-marker id: $CLAUDE_CODE_SESSION_ID, or the
// deliberately-unroutable nosession-<ppid> fallback (bin/cbus:189).
func markerSID() string {
	if sid := SessionID(); sid != "" {
		return sid
	}
	return "nosession-" + strconv.Itoa(os.Getppid())
}

// HostLabel is this machine's label, the ONE place it is resolved: $CBUS_HOST when
// set, else `hostname -s` ("unknown" if the system has none). Both take the part
// before the first dot. An invalid $CBUS_HOST is an error, never a fallback: meta,
// ledger and formation machine all carry this value, so a silent substitute would
// record the machine under a name the user did not choose.
func HostLabel() (string, error) {
	if v := os.Getenv("CBUS_HOST"); v != "" {
		return screenHostLabel(v)
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown", nil
	}
	return shortLabel(h), nil
}

// ErrBadHostLabel marks an invalid CBUS_HOST so callers can tell it from I/O errors.
var ErrBadHostLabel = errors.New("invalid CBUS_HOST")

func screenHostLabel(v string) (string, error) {
	if s := shortLabel(v); core.ValidName(v) && s != "" && core.ValidName(s) {
		return s, nil
	}
	return "", fmt.Errorf("%w %q: use letters, digits, '.', '_' or '-' (the part before the first dot is the label), or unset it to use the system hostname", ErrBadHostLabel, v)
}

func shortLabel(h string) string {
	if i := strings.IndexByte(h, '.'); i >= 0 {
		return h[:i]
	}
	return h
}

// RemoteFromDefault is the default `from` for a remote send when --from is
// omitted: THIS session's identity marker on (host, channel) if it has a non-empty
// alias, else the unroutable <shorthost>-<ppid> fallback. It never consults local
// registrations or $CBUS_ALIAS — the remote chain differs from the local one by
// design (protocol.md §3.1).
func RemoteFromDefault(host, channel string) (string, error) {
	mf := filepath.Join(CBUSDir(), ".remote", host, channel, markerSID())
	if b, err := os.ReadFile(mf); err == nil {
		var m struct {
			Alias string `json:"alias"`
		}
		if json.Unmarshal(b, &m) == nil && m.Alias != "" {
			return channel + "@" + host + "/" + m.Alias, nil
		}
	}
	label, err := HostLabel()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d", label, os.Getppid()), nil
}

// metaSessionID reads the sessionId out of a meta.json, tolerating a missing or
// torn file as "absent" — the same read-tolerance jget relies on (a concurrent
// non-atomic rewrite can be seen truncated; bin/cbus:30-35, protocol.md §2.2).
func metaSessionID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.SessionID
}

// metaOrigin reads the birth-record origin out of a meta.json, "" when unreadable —
// a known subject fact that terminal and rename events would otherwise drop.
func metaOrigin(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		Origin string `json:"origin"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.Origin
}

// currentProfile is the CCS instance name this process runs under, derived
// structurally from CLAUDE_CONFIG_DIR (<root>/.ccs/instances/<profile>); blank when
// not under CCS. Structural parent checks, not a substring, so it holds on windows
// separators too.
func currentProfile() string {
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	if cfg == "" {
		return ""
	}
	// Clean first: a trailing separator would make Dir return the path itself and
	// silently fail the structural check for a perfectly valid instance dir.
	cfg = filepath.Clean(cfg)
	parent := filepath.Dir(cfg)
	if filepath.Base(parent) != "instances" || filepath.Base(filepath.Dir(parent)) != ".ccs" {
		return ""
	}
	return filepath.Base(cfg)
}
