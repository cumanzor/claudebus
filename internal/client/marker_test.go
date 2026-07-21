package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalMarkerIndent2(t *testing.T) {
	b, err := marshalMarker("goalias", 4242, "2026-07-13T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"alias\": \"goalias\",\n  \"ownerPid\": 4242,\n  \"ts\": \"2026-07-13T01:00:00Z\"\n}"
	if string(b) != want {
		t.Fatalf("marker bytes =\n%q\nwant\n%q", b, want)
	}
}

func TestWriteAndReadRemoteMarker(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID123")
	if err := WriteRemoteMarker("nuc", "dev", "mbp"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".remote", "nuc", "dev", "SID123"))
	if err != nil {
		t.Fatal(err)
	}
	var m remoteMarker
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Alias != "mbp" || m.OwnerPid <= 0 || m.TS == "" {
		t.Errorf("marker = %+v", m)
	}
	if !strings.HasPrefix(string(b), "{\n  \"alias\": \"mbp\",") {
		t.Errorf("on-disk marker is not indent=2: %q", b)
	}
	if from := RemoteFromDefault("nuc", "dev"); from != "dev@nuc/mbp" {
		t.Errorf("RemoteFromDefault = %q, want dev@nuc/mbp", from)
	}
}

func TestWriteRemoteMarkerReplacesLegacyFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID")
	// legacy machine-global marker: a FILE where the channel dir should be
	if err := os.MkdirAll(filepath.Join(root, ".remote", "nuc"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(root, ".remote", "nuc", "dev")
	if err := os.WriteFile(legacyFile, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteRemoteMarker("nuc", "dev", "x"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(legacyFile); err != nil || !fi.IsDir() {
		t.Errorf("legacy file not replaced by a dir: %v", err)
	}
	if from := RemoteFromDefault("nuc", "dev"); from != "dev@nuc/x" {
		t.Errorf("RemoteFromDefault after replace = %q", from)
	}
}

func TestRemoteFromDefaultFallbackUnroutable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID")
	from := RemoteFromDefault("nuc", "nomarker") // no marker -> <shorthost>-<ppid>
	if strings.Contains(from, "@") {
		t.Errorf("fallback from must be unroutable (no @): %q", from)
	}
	if !strings.Contains(from, "-") {
		t.Errorf("fallback from should look like <host>-<pid>: %q", from)
	}
}

func TestRemoteTailSpec(t *testing.T) {
	spec := RemoteTailSpec("https://bus.example.com", "TOK123", "dev", "nuc", "mbp")
	want := "remote listening is armed with the Monitor tool's ws source (not a command):\n" +
		"  url:         wss://bus.example.com/tail?channel=dev&alias=mbp\n" +
		"  protocols:   [\"bearer.cbus.TOK123\"]\n" +
		"  description: cbus:dev@nuc/mbp   (persistent: true)\n" +
		"identity recorded for THIS session: sends to dev@nuc/* default to from=dev@nuc/mbp\n" +
		"note: the protocols entry carries the relay token — expected; it IS the auth.\n"
	if spec != want {
		t.Fatalf("arm-spec mismatch:\n got: %q\nwant: %q", spec, want)
	}
}

// TestIsHarnessComm: the pure predicate over a command BASENAME. The harness set is
// claude/claude-* (Claude Code), grok + xai-grok-pager (grok), opencode, codex. The
// false rows guard the blocker and the near-misses: "2.1.214" is the bun CLI's kernel
// comm (its version string), "claudebus" is this repo's own binary a substring match
// would claim, "grokd"/"myclaude" are suffix/prefix look-alikes that are not the harness.
func TestIsHarnessComm(t *testing.T) {
	for base, want := range map[string]bool{
		"claude":         true,
		"claude-3":       true,
		"claude-code":    true,
		"claude-":        true, // degenerate but in-contract for claude-*
		"grok":           true,
		"xai-grok-pager": true,
		"opencode":       true,
		"codex":          true,
		"node":           false, // codex runs under a node shim; the walk hits codex first
		"grokd":          false, // suffix look-alike, not an exact member
		"myclaude":       false, // prefix look-alike, not claude-*
		"claudebus":      false, // this repo's binary — not "claude-" (no dash)
		"2.1.214":        false, // the bug: the bun CLI's comm is its version string
		"Claude":         false, // exact case, no folding
		"codexd":         false,
		"":               false,
		"sh":             false,
	} {
		if got := isHarnessComm(base); got != want {
			t.Errorf("isHarnessComm(%q) = %v, want %v", base, got, want)
		}
	}
}

// TestCommBase: identity is argv[0]'s BASENAME, so a leading path is stripped before the
// predicate. Together with isHarnessComm this reproduces the old whole-path behavior —
// "/opt/x/claudebus" is not a session (substring, not basename), "/usr/local/bin/claude"
// is.
func TestCommBase(t *testing.T) {
	for name, wantBase := range map[string]string{
		"claude":                   "claude",
		"/usr/local/bin/claude":    "claude",
		"/nix/store/abc/claude-v2": "claude-v2",
		"/opt/x/claudebus":         "claudebus",
		"/usr/bin/node":            "node",
		"":                         "",
	} {
		if got := commBase(name); got != wantBase {
			t.Errorf("commBase(%q) = %q, want %q", name, got, wantBase)
		}
	}
	if isHarnessComm(commBase("/opt/x/claudebus")) {
		t.Error("/opt/x/claudebus is this repo's binary, not a session")
	}
	if !isHarnessComm(commBase("/usr/local/bin/claude")) {
		t.Error("/usr/local/bin/claude is a claude session")
	}
}
