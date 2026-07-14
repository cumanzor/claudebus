package core

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestReframeDivergenceMatrix pins the relay-side column of the framer divergence
// matrix (protocol.md §4.5). core.Reframe IS the relay framer, so these lock in its
// all-or-nothing gate: any non-string field or empty/missing text => byte-identical
// passthrough; only a well-formed object with a non-empty string text is framed. As
// of cbus-ijx.5 the relay RENDERS `kind` (server-side presence fan-out) instead of
// dropping it, matching LocalEmit; a kind-absent line is unchanged.
func TestReframeDivergenceMatrix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // for passthrough cases want == in (byte-identical)
	}{
		{"empty text -> passthrough",
			`{"from":"c/o","to":"c/a","ts":"t","text":""}`,
			`{"from":"c/o","to":"c/a","ts":"t","text":""}`},
		{"missing text key -> passthrough",
			`{"from":"c/o","to":"c/a","ts":"t"}`,
			`{"from":"c/o","to":"c/a","ts":"t"}`},
		{"non-string text -> passthrough (unmarshal error)",
			`{"from":"c/o","to":"c/a","ts":"t","text":123}`,
			`{"from":"c/o","to":"c/a","ts":"t","text":123}`},
		{"null text -> passthrough",
			`{"from":"c/o","to":"c/a","ts":"t","text":null}`,
			`{"from":"c/o","to":"c/a","ts":"t","text":null}`},
		{"non-string from -> passthrough (any non-string field aborts)",
			`{"from":123,"to":"c/a","ts":"t","text":"hi"}`,
			`{"from":123,"to":"c/a","ts":"t","text":"hi"}`},
		{"non-dict JSON -> passthrough",
			`[1,2,3]`, `[1,2,3]`},
		{"from/to missing, text ok -> framed with empty routing",
			`{"ts":"t","text":"hi"}`,
			"◀ cbus msg from= to= ts=t\nhi\n◀ cbus end from="},
		{"kind present, text ok -> framed with kind in header (cbus-ijx.5)",
			`{"from":"c/o","to":"c/a","ts":"t","text":"hi","kind":"presence"}`,
			"◀ cbus msg from=c/o to=c/a ts=t kind=presence\nhi\n◀ cbus end from=c/o"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(Reframe([]byte(c.in)))
			if got != c.want {
				t.Errorf("Reframe(%s)\n got  %q\n want %q", c.in, got, c.want)
			}
		})
	}
}

// TestWrapBytesProperties exercises the rune-safe wrap invariants across rune widths
// (1/3/4 bytes), lengths, and limits: no bytes lost or added, every piece is valid
// UTF-8, and a multi-rune piece never exceeds the limit (a lone rune wider than the
// limit is the only way a piece may exceed it — it cannot be split).
func TestWrapBytesProperties(t *testing.T) {
	runes := []string{"a", "你", "🎉"} // 1, 3, 4 bytes
	limits := []int{1, 3, 4, 7, BodyWrap}
	for _, ru := range runes {
		for n := 0; n <= 300; n++ {
			s := strings.Repeat(ru, n)
			for _, limit := range limits {
				pieces := wrapBytes(s, limit)
				if strings.Join(pieces, "") != s {
					t.Fatalf("wrapBytes(%d×%q, %d): reassembly changed the bytes", n, ru, limit)
				}
				for _, p := range pieces {
					if !utf8.ValidString(p) {
						t.Fatalf("wrapBytes(%d×%q, %d): produced invalid UTF-8 piece %q", n, ru, limit, p)
					}
					if utf8.RuneCountInString(p) > 1 && len(p) > limit {
						t.Fatalf("wrapBytes(%d×%q, %d): multi-rune piece %d bytes exceeds limit", n, ru, limit, len(p))
					}
					if s != "" && p == "" {
						t.Fatalf("wrapBytes(%d×%q, %d): empty piece from non-empty input", n, ru, limit)
					}
				}
			}
		}
	}
	// empty input is the one case that yields a single empty piece (preserved as an
	// empty body line).
	if got := wrapBytes("", BodyWrap); len(got) != 1 || got[0] != "" {
		t.Fatalf(`wrapBytes("", 440) = %q, want [""]`, got)
	}
}

// TestReframeBodyWrapRuneSafe drives the invariants through the full framer: body
// lines never exceed BodyWrap bytes, never contain a split codepoint, and a
// single-segment body reassembles to the original text exactly.
func TestReframeBodyWrapRuneSafe(t *testing.T) {
	texts := []string{
		strings.Repeat("你", 400),        // 1200 B, wraps at 3-byte rune boundaries
		strings.Repeat("🎉", 300),        // 1200 B, 4-byte runes
		strings.Repeat("a", BodyWrap),   // exactly the wrap size (no wrap)
		strings.Repeat("a", BodyWrap+1), // one over (forces a wrap)
		"ascii head " + strings.Repeat("z", 1000) + strings.Repeat("你", 100),
	}
	for _, txt := range texts {
		msg, err := json.Marshal(map[string]string{"from": "c/o", "to": "c/a", "ts": "t", "text": txt})
		if err != nil {
			t.Fatal(err)
		}
		out := string(Reframe(msg))
		lines := strings.Split(out, "\n")
		if len(lines) < 3 {
			t.Fatalf("expected header+body+end, got %d lines", len(lines))
		}
		if !strings.HasPrefix(lines[0], "◀ cbus msg from=c/o to=c/a ts=t") {
			t.Fatalf("bad header: %q", lines[0])
		}
		if lines[len(lines)-1] != "◀ cbus end from=c/o" {
			t.Fatalf("bad end marker: %q", lines[len(lines)-1])
		}
		body := lines[1 : len(lines)-1]
		for i, l := range body {
			if len(l) > BodyWrap {
				t.Fatalf("body line %d is %d bytes (>%d)", i, len(l), BodyWrap)
			}
			if !utf8.ValidString(l) || strings.ContainsRune(l, '�') {
				t.Fatalf("body line %d not rune-safe: %q", i, l)
			}
		}
		// single-segment text (no embedded newline) reassembles exactly.
		if got := strings.Join(body, ""); got != txt {
			t.Fatalf("body did not reassemble: got %d bytes, want %d", len(got), len(txt))
		}
	}
}

// TestReframeOversizeThreshold pins the WSFrameSafe warn behavior in core: a small
// message carries no warning; a large one gains ` ⚠truncated~<N>B` on the header with
// N = byte length of the ORIGINAL text (protocol.md §4.4).
func TestReframeOversizeThreshold(t *testing.T) {
	small, _ := json.Marshal(map[string]string{"from": "c/o", "to": "c/a", "ts": "t", "text": "hi"})
	if strings.Contains(string(Reframe(small)), "⚠") {
		t.Error("small message must not warn")
	}
	big, _ := json.Marshal(map[string]string{"from": "c/o", "to": "c/a", "ts": "t", "text": strings.Repeat("z", 4000)})
	head := strings.SplitN(string(Reframe(big)), "\n", 2)[0]
	if !strings.Contains(head, "⚠truncated~4000B") {
		t.Errorf("oversize header should warn with original text byte-count: %q", head)
	}
}
