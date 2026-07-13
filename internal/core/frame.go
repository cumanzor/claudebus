// Package core holds the domain types, wire structs, name validation, and the
// message framer shared by the relay daemon and the (future) Go client. Its
// reason to exist: the framed delivery block was implemented twice — embedded
// python in bin/cbus and Go in the relay — and had to be kept byte-identical by
// hand. Centralizing it here makes frame parity a compile-time property.
package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Measured Claude Code Monitor harness constants. These are point-in-time
// MEASUREMENTS of the harness, not negotiated values — a harness update can
// silently invalidate them. Provenance: originally measured 2026-07-11/12,
// re-measured live on this machine 2026-07-13 (all confirmed unchanged).
const (
	// MonitorLineCap: any single stdout/ws line is truncated past this. Bisected
	// 2026-07-13: 500 chars pass intact, 505 truncated. This is why body lines
	// wrap at BodyWrap bytes (a 440-byte line passes whole). bin/cbus:504,
	// main.go:207 (measured).
	MonitorLineCap = 500

	// BodyWrap: message body segments hard-wrap at this many UTF-8 bytes,
	// rune-safe (never splits a codepoint). Chosen so a wrapped line clears
	// MonitorLineCap. bin/cbus:522, main.go:239.
	BodyWrap = 440

	// WSFrameSafe: the relay ⚠truncated warning threshold. Past the ~3000-char
	// per-notification ceiling (confirmed 2026-07-13: a multi-line burst is cut
	// at ~3000 received) the harness truncates the tail, so the relay warns in
	// the header (delivered first, survives the cut) once a framed block exceeds
	// this. main.go:204 (measured).
	//
	// Harness DELTA observed 2026-07-13: truncation now emits an explicit
	// "...(truncated)" marker at BOTH the per-line cap and the notification
	// ceiling — earlier the local path cut silently. Truncation detection thus
	// no longer relies solely on a missing "◀ cbus end" marker. This is
	// informational; it does not change the framing math below.
	WSFrameSafe = 2800
)

// wrapBytes hard-wraps a string at rune boundaries so no line exceeds limit
// bytes (the Monitor truncates any single line at MonitorLineCap). Empty -> [""].
func wrapBytes(seg string, limit int) []string {
	if len(seg) <= limit {
		return []string{seg}
	}
	var out []string
	start := 0
	for i, r := range seg {
		if i > start && i-start+utf8.RuneLen(r) > limit {
			out = append(out, seg[start:i])
			start = i
		}
	}
	return append(out, seg[start:])
}

// Reframe rewrites a stored {from,to,ts,text} JSON line into the same framed,
// line-wrapped block the local tail follower emits, so a long message survives
// the Monitor's per-line cap and arrives whole in one ws frame. A non-JSON /
// text-less payload passes through unchanged (defensive).
//
// The returned bytes carry NO trailing newline: on the relay path the block is
// sent as one ws OpText frame; the local follower is responsible for appending
// exactly one '\n' as its stdout line terminator. The golden parity contract is
// therefore: bash emit() bytes == Reframe(msg) + "\n" (see golden tests).
//
// Quirk preserved verbatim (protocol.md §4.4): the oversize total is computed
// BEFORE the header line exists (a 1-byte placeholder), so the ⚠truncated
// warning actually fires iff the block exceeds WSFrameSafe-1 + len(header) — a
// silent window of len(header)-1 bytes above WSFrameSafe. Bug-compatible with
// the relay; do not "fix" without a lockstep contract change.
func Reframe(payload []byte) []byte {
	var m struct {
		From string `json:"from"`
		To   string `json:"to"`
		TS   string `json:"ts"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &m); err != nil || m.Text == "" {
		return payload
	}
	lines := []string{""} // header filled in below (needs the total)
	for _, seg := range strings.Split(m.Text, "\n") {
		lines = append(lines, wrapBytes(seg, BodyWrap)...)
	}
	lines = append(lines, "◀ cbus end from="+m.From)
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	head := fmt.Sprintf("◀ cbus msg from=%s to=%s ts=%s", m.From, m.To, m.TS)
	if total > WSFrameSafe { // warn in the header (which survives) so it's not silent
		head += fmt.Sprintf(" ⚠truncated~%dB", len(m.Text))
	}
	lines[0] = head
	return []byte(strings.Join(lines, "\n"))
}
