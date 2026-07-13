package client

import (
	"fmt"
	"strings"

	"claudebus/internal/core"
)

// Target is a parsed cbus address.
type Target struct {
	Remote  bool
	Channel string
	Host    string // remote only
	Alias   string
}

// IsRemote reports whether raw denotes a remote (relay-backed) target: a target
// is remote iff it contains '@' ANYWHERE (bin/cbus checks `case $1 in *@*`).
func IsRemote(raw string) bool { return strings.Contains(raw, "@") }

// ParseLocal splits a local target "<channel>/<alias>" (split at the FIRST '/')
// or a bare "<alias>" (empty channel). An empty channel half skips validation —
// so "/main" is indistinguishable from bare "main" (bin/cbus:82-89, a preserved
// quirk). A second '/' lands in the alias and fails validation ("a/b/c" -> bad
// alias "b/c"). Errors are hard (returned), matching the bash `die`.
func ParseLocal(raw string) (channel, alias string, err error) {
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		channel, alias = raw[:i], raw[i+1:]
	} else {
		channel, alias = "", raw
	}
	if channel != "" && !core.ValidName(channel) {
		return "", "", fmt.Errorf("bad channel %q", channel)
	}
	if !core.ValidName(alias) {
		return "", "", fmt.Errorf("bad alias %q", alias)
	}
	return channel, alias, nil
}

// ParseRemote splits "<channel>@<host>[/<alias>]": channel is everything before
// the FIRST '@', the remainder splits at the FIRST '/' into host / alias. Each
// PRESENT part is validated; empty parts are skipped — an empty channel
// ("@nuc/al") is accepted client-side (the relay rejects it with 400; `list`
// legitimately uses it for a whole host). bin/cbus:121-132.
func ParseRemote(raw string) (channel, host, alias string, err error) {
	at := strings.IndexByte(raw, '@')
	if at < 0 {
		return "", "", "", fmt.Errorf("not a remote target %q", raw)
	}
	channel = raw[:at]
	rest := raw[at+1:]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		host, alias = rest[:i], rest[i+1:]
	} else {
		host, alias = rest, ""
	}
	if channel != "" && !core.ValidName(channel) {
		return "", "", "", fmt.Errorf("bad channel %q", channel)
	}
	if !core.ValidName(host) {
		return "", "", "", fmt.Errorf("bad host %q", host)
	}
	if alias != "" && !core.ValidName(alias) {
		return "", "", "", fmt.Errorf("bad alias %q", alias)
	}
	return channel, host, alias, nil
}

// Resolver maps a bare alias to the sender's channel that contains it
// (find_peer_channel). It returns ok=false when none of the sender's own
// channels holds a peer with that alias.
type Resolver func(alias string) (channel string, ok bool)

// Parse resolves any target to a Target. Remote targets parse syntactically; a
// bare local alias is resolved to the sender's containing channel via resolve
// (which reads $CBUS_DIR). A nil resolver, or a bare alias that resolves to
// nothing, is a hard error (bash: `die "use <channel>/<alias>"`).
func Parse(raw string, resolve Resolver) (Target, error) {
	if IsRemote(raw) {
		ch, host, al, err := ParseRemote(raw)
		if err != nil {
			return Target{}, err
		}
		return Target{Remote: true, Channel: ch, Host: host, Alias: al}, nil
	}
	ch, al, err := ParseLocal(raw)
	if err != nil {
		return Target{}, err
	}
	if ch == "" {
		if resolve == nil {
			return Target{}, fmt.Errorf("use <channel>/<alias>")
		}
		rc, ok := resolve(al)
		if !ok {
			return Target{}, fmt.Errorf("use <channel>/<alias>")
		}
		ch = rc
	}
	return Target{Channel: ch, Alias: al}, nil
}
