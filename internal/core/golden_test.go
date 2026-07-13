package core

import (
	"bytes"
	"encoding/json"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden framer parity — the Phase 0 gate that pins core.Reframe byte-for-byte to
// the bash follower's emit().
//
// DOMAIN BOUNDARY (stated per the m4 ruling): the corpus is PLAIN tool-authored
// shapes only — all four fields present as strings, non-empty text, NO kind. That
// is exactly the set where the local follower emit() and the relay reframe() agree
// (protocol.md §4.5). The divergent shapes (empty text, non-string fields,
// presence/kind) are NOT parity cases and are pinned separately in
// frame_test.go:TestReframeDivergenceMatrix — do not add them here.
//
// CONTRACT: the local follower writes each frame as `Reframe(line)` bytes plus one
// trailing '\n' stdout terminator; the relay ws frame has no trailing newline. So
// the gate is: emit() bytes == Reframe(line) + "\n", byte-exact.
//
// GENERATOR of record: testdata/emit_golden.py, the follower's wrap()/emit() lifted
// VERBATIM from bin/cbus:515-551. testdata/gen_corpus.py builds corpus.jsonl the
// way the real client does (python json.dumps). Regenerate with:
//
//	python3 gen_corpus.py > corpus.jsonl
//	python3 emit_golden.py < corpus.jsonl > corpus.golden
//
// ONE-TIME VALIDATIONS (2026-07-13, this machine, recorded per the m4 ruling):
//
//   - LIFT == LIVE: captured the real `cbus tail` follower under a hermetic
//     CBUS_DIR=$(mktemp -d) — join scratch/x, append corpus.jsonl to its inbox,
//     first-arm replay, bounded stdout capture — and compared to corpus.golden.
//     Result: IDENTICAL (6861 B, 56 lines). Proves the lift is faithful to the live
//     follower (no env/encoding/locale drift). This bounded capture is the ONLY
//     sanctioned Bash use of the local tail — test-harness only, never in a session
//     (a session must arm it via the Monitor tool).
//   - D8 CROSS-PARSE: re-marshaled every corpus line the Go way (canonical-Go bytes
//     — compact, HTML-escaped <>&, raw UTF-8) and fed those through emit(). Result:
//     emit(go-marshaled) == emit(python-marshaled) == corpus.golden, byte-exact.
//     Proves bash-era consumers frame Go-marshaled lines identically during
//     coexistence (empirically upholds ruling D8).
//   - D8 PRESENCE CROSS-PARSE (P2.2 rider, 2026-07-13): the D8 extension to presence
//     lines, proven the same way. A go-canonical {from,to,ts,kind,event,text} line
//     and its python-marshaled equivalent, both framed via emit(), are BYTE-IDENTICAL
//     — including the "kind=presence" header (event is stored, never rendered). So a
//     canonical-Go presence line delivers identically to bash's; the raw-line D8
//     ruling stands for the local presence path too.

// TestGoldenCorpusParity walks the golden message-by-message, asserting each block
// equals Reframe(line)+"\n".
func TestGoldenCorpusParity(t *testing.T) {
	corpus, err := os.ReadFile(filepath.Join("testdata", "corpus.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "corpus.golden"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(corpus), "\n"), "\n")
	off := 0
	for i, line := range lines {
		want := append(Reframe([]byte(line)), '\n')
		if off+len(want) > len(golden) || !bytes.Equal(golden[off:off+len(want)], want) {
			end := off + len(want)
			if end > len(golden) {
				end = len(golden)
			}
			t.Fatalf("message %d diverges from Reframe(line)+\\n\n line:   %.80q\n want:   %q\n golden: %q",
				i, line, want, golden[off:end])
		}
		off += len(want)
	}
	if off != len(golden) {
		t.Fatalf("golden has %d unexpected trailing bytes after %d messages", len(golden)-off, len(lines))
	}
}

// TestReframeOversizeFlipPoint pins the header-exemption quirk (protocol.md §4.4):
// the ⚠truncated warning fires on `total > WSFrameSafe` where total is computed with
// a 1-byte header PLACEHOLDER, not the real header. So a block whose header-less
// total is exactly WSFrameSafe must NOT warn — a "count the header" refactor would
// warn here and this test would catch it. (Reviewer measured the real flip at an
// emitted block of 2852 B with a 52-byte header; the silent window is len(header)-1
// bytes.)
func TestReframeOversizeFlipPoint(t *testing.T) {
	total := func(text string) int { // exactly Reframe's header-less computation
		lines := []string{""} // placeholder for the header (1 byte via the +1 below)
		for _, seg := range strings.Split(text, "\n") {
			lines = append(lines, wrapBytes(seg, BodyWrap)...)
		}
		lines = append(lines, "◀ cbus end from=c/o")
		n := 0
		for _, l := range lines {
			n += len(l) + 1
		}
		return n
	}
	warns := func(text string) bool {
		b, _ := json.Marshal(map[string]string{"from": "c/o", "to": "c/a", "ts": "t", "text": text})
		return strings.Contains(string(Reframe(b)), "⚠")
	}
	var atCap, overCap string
	for k := 2700; k < 2900 && (atCap == "" || overCap == ""); k++ {
		s := strings.Repeat("a", k)
		switch total(s) {
		case WSFrameSafe:
			atCap = s
		case WSFrameSafe + 1:
			overCap = s
		}
	}
	if atCap == "" || overCap == "" {
		t.Fatal("could not construct boundary inputs around WSFrameSafe")
	}
	if warns(atCap) {
		t.Errorf("header-less total==%d (==WSFrameSafe) warned; must NOT — a header-inclusive total is the regression this pins", WSFrameSafe)
	}
	if !warns(overCap) {
		t.Errorf("header-less total==%d (>WSFrameSafe) must warn", WSFrameSafe+1)
	}
}

// TestGofmtClean is the standing gofmt gate over the module (minimal, no CI infra):
// every .go file must equal its gofmt form. Uses go/format so it needs no external
// process.
func TestGofmtClean(t *testing.T) {
	root := filepath.Join("..", "..") // repo root, relative to internal/core
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: does not parse: %v", path, err)
			return nil
		}
		if !bytes.Equal(src, formatted) {
			t.Errorf("%s is not gofmt-clean (run: gofmt -w %s)", path, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
