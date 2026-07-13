package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// SessionID is this session's $CLAUDE_CODE_SESSION_ID (empty if unset — the
// sessionless mode, where identity lookups yield nothing).
func SessionID() string { return os.Getenv("CLAUDE_CODE_SESSION_ID") }

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

// ShortHostname is `hostname -s` (the label before the first dot); "unknown" on
// failure.
func ShortHostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	return h
}

// RemoteFromDefault is the default `from` for a remote send when --from is
// omitted: THIS session's identity marker on (host, channel) if it has a non-empty
// alias, else the unroutable <shorthost>-<ppid> fallback. It never consults local
// registrations or $CBUS_ALIAS — the remote chain differs from the local one by
// design (protocol.md §3.1).
func RemoteFromDefault(host, channel string) string {
	mf := filepath.Join(CBUSDir(), ".remote", host, channel, markerSID())
	if b, err := os.ReadFile(mf); err == nil {
		var m struct {
			Alias string `json:"alias"`
		}
		if json.Unmarshal(b, &m) == nil && m.Alias != "" {
			return channel + "@" + host + "/" + m.Alias
		}
	}
	return fmt.Sprintf("%s-%d", ShortHostname(), os.Getppid())
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
