// Package core holds the domain types, wire structs, name validation, and the
// message framer shared by the relay daemon and the (future) Go client. Its
// reason to exist: the framed delivery block was implemented twice — embedded
// python in bin/cbus and Go in the relay — and had to be kept byte-identical by
// hand. Centralizing it here makes frame parity a compile-time property.
package core

import (
	"bytes"
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
	// wrap at BodyWrap bytes (a 440-byte line passes whole). bin/cbus:504;
	// main.go:207 (pre-extraction anchor). (measured)
	MonitorLineCap = 500

	// BodyWrap: message body segments hard-wrap at this many UTF-8 bytes,
	// rune-safe (never splits a codepoint). Chosen so a wrapped line clears
	// MonitorLineCap. bin/cbus:522; main.go:239 (pre-extraction anchor).
	BodyWrap = 440

	// WSFrameSafe: the relay ⚠truncated warning threshold. Past the ~3000-char
	// per-notification ceiling (confirmed 2026-07-13: a multi-line burst is cut
	// at ~3000 received) the harness truncates the tail, so the relay warns in
	// the header (delivered first, survives the cut) once a framed block exceeds
	// this. main.go:204 (pre-extraction anchor). (measured)
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

// Frame markers, shared by Reframe (relay) and LocalEmit (local follower) so the
// literal exists once. bin/cbus:544,549.
const (
	markerMsg = "◀ cbus msg"
	markerEnd = "◀ cbus end"
)

// frameHead is the base header both framers build from: `◀ cbus msg from=.. to=..
// ts=..`. The relay may append a ⚠truncated warning; the local follower may append
// ` kind=..` (see the two callers).
func frameHead(from, to, ts string) string {
	return markerMsg + " from=" + from + " to=" + to + " ts=" + ts
}

// frameBody wraps text into rune-safe body lines (BodyWrap bytes) and appends the
// end marker for `from`. This is the shared wrap/marker core — there is no second
// copy of the wrap loop in LocalEmit.
func frameBody(text, from string) []string {
	var lines []string
	for _, seg := range strings.Split(text, "\n") {
		lines = append(lines, wrapBytes(seg, BodyWrap)...)
	}
	return append(lines, markerEnd+" from="+from)
}

// Reframe rewrites a stored {from,to,ts,text[,kind]} JSON line into the same
// framed, line-wrapped block the local tail follower emits, so a long message
// survives the Monitor's per-line cap and arrives whole in one ws frame. A
// non-JSON / text-less payload passes through unchanged (defensive). It renders
// ` kind=<k>` in the header when the stored line carries a kind — the relay's
// server-side presence fan-out (cbus-ijx.5) writes presence events as ordinary
// spool lines with kind=presence, so remote peers see them just like local ones.
// A kind-ABSENT line renders byte-identically to before (the golden parity
// domain is untouched), converging this framer with LocalEmit on the kind axis.
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
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &m); err != nil || m.Text == "" {
		return payload
	}
	lines := append([]string{""}, frameBody(m.Text, m.From)...) // [0] = header placeholder (needs the total)
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	head := frameHead(m.From, m.To, m.TS)
	if m.Kind != "" { // same position as LocalEmit, so both framers agree on kind lines
		head += " kind=" + m.Kind
	}
	if total > WSFrameSafe { // warn in the header (which survives) so it's not silent
		head += fmt.Sprintf(" ⚠truncated~%dB", len(m.Text))
	}
	lines[0] = head
	return []byte(strings.Join(lines, "\n"))
}

// LocalEmit renders one stored inbox line into the framed block the LOCAL tail
// follower streams to stdout — the in-process counterpart to Reframe. It differs
// from the relay framer in exactly two byte-visible ways: it KEEPS `kind=` in the
// header (presence events are displayed locally) and it appends the single '\n'
// stdout line terminator that is part of every local write. So on the parity
// domain (all fields present as strings, non-empty text, no kind) the golden
// contract holds verbatim: bash emit() bytes == LocalEmit(line) == Reframe(line)+"\n".
// The local follower has no oversize ⚠ warning (that is a relay-only concern).
//
// Degenerate inputs follow ruling D6 (deliberately NOT the bash follower's
// verbatim behavior): a non-JSON payload, any non-string field, or empty/null/
// missing text passes THROUGH unframed — plus its stdout terminator — which fixes
// bash's None-leak on null text; and a framed line with missing from/to renders
// "?" so the local column is never blank. These divergences are pinned in
// TestLocalEmitDivergenceMatrix (citing D6), never in the golden corpus.
func LocalEmit(payload []byte) []byte {
	line := bytes.TrimRight(payload, "\n")
	if len(line) == 0 {
		return nil // an empty inbox line emits nothing (bash: `if not line: return`)
	}
	// Pointer fields distinguish absent (nil -> "?") from present, and a non-string
	// field unmarshal-errors into passthrough (the D6 strict gate, == Reframe's).
	var m struct {
		From *string `json:"from"`
		To   *string `json:"to"`
		TS   *string `json:"ts"`
		Text *string `json:"text"`
		Kind *string `json:"kind"`
	}
	if err := json.Unmarshal(line, &m); err != nil || m.Text == nil || *m.Text == "" {
		out := make([]byte, len(line)+1)
		copy(out, line)
		out[len(line)] = '\n'
		return out
	}
	frm, to, ts := "?", "?", ""
	if m.From != nil {
		frm = *m.From
	}
	if m.To != nil {
		to = *m.To
	}
	if m.TS != nil {
		ts = *m.TS
	}
	head := frameHead(frm, to, ts)
	if m.Kind != nil && *m.Kind != "" {
		head += " kind=" + *m.Kind
	}
	lines := append([]string{head}, frameBody(*m.Text, frm)...)
	return []byte(strings.Join(lines, "\n") + "\n")
}
