package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mkMsg(t *testing.T, text string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"from": "ch/orch", "to": "ch/mbp", "ts": "2026-07-12T00:00:00Z", "text": text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// every emitted physical line must stay under the 500-char per-line cap.
func assertLinesUnder500(t *testing.T, out []byte) {
	t.Helper()
	for i, l := range strings.Split(string(out), "\n") {
		if len(l) > 500 {
			t.Fatalf("line %d is %d bytes (>500): %q...", i, len(l), l[:60])
		}
	}
}

func TestReframeShort(t *testing.T) {
	out := string(reframe(mkMsg(t, "hello world")))
	if !strings.HasPrefix(out, "◀ cbus msg from=ch/orch to=ch/mbp ts=") {
		t.Fatalf("bad header: %q", out)
	}
	if !strings.Contains(out, "\nhello world\n") {
		t.Fatalf("body missing: %q", out)
	}
	if !strings.HasSuffix(out, "◀ cbus end from=ch/orch") {
		t.Fatalf("bad end: %q", out)
	}
	if strings.Contains(out, "⚠") {
		t.Fatalf("short msg should not warn: %q", out)
	}
}

func TestReframeLongWraps(t *testing.T) {
	out := reframe(mkMsg(t, strings.Repeat("a", 2000)))
	assertLinesUnder500(t, out)
	lines := strings.Split(string(out), "\n")
	// header + ceil(2000/440)=5 body lines + end = 7
	if len(lines) < 6 {
		t.Fatalf("expected multiple wrapped lines, got %d", len(lines))
	}
	// reassembled body (drop header+end, concat) must equal the original text
	body := strings.Join(lines[1:len(lines)-1], "")
	if body != strings.Repeat("a", 2000) {
		t.Fatalf("body not reassemblable: got %d chars", len(body))
	}
}

func TestReframeUnicodeNoSplit(t *testing.T) {
	// 400 multibyte runes (3 bytes each = 1200 bytes) forces a wrap; must not
	// split a rune (invalid utf8 would appear).
	out := reframe(mkMsg(t, strings.Repeat("你", 400)))
	assertLinesUnder500(t, out)
	for _, l := range strings.Split(string(out), "\n") {
		for _, r := range l {
			if r == '�' {
				t.Fatal("rune split produced replacement char")
			}
		}
	}
}

func TestReframeNewlinesPreserved(t *testing.T) {
	out := string(reframe(mkMsg(t, "line1\nline2\nline3")))
	for _, want := range []string{"\nline1\n", "\nline2\n", "\nline3\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestReframePassthroughBadJSON(t *testing.T) {
	raw := []byte("not json at all")
	if string(reframe(raw)) != string(raw) {
		t.Fatal("bad json should pass through unchanged")
	}
	empty := mkMsg(t, "")
	if string(reframe(empty)) != string(empty) {
		t.Fatal("text-less json should pass through unchanged")
	}
}

func TestReframeOversizeWarns(t *testing.T) {
	out := string(reframe(mkMsg(t, strings.Repeat("z", 4000))))
	if !strings.Contains(strings.SplitN(out, "\n", 2)[0], "⚠truncated~4000B") {
		t.Fatalf("oversize header should warn: %q", strings.SplitN(out, "\n", 2)[0])
	}
}
