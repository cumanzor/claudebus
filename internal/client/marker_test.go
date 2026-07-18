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

// TestIsClaudeName: identity is argv[0]'s BASENAME being claude or claude-*. The
// first two false rows are the blocker itself — "2.1.214" is what the bun-compiled
// CLI reports as its kernel comm, and "/x/claudebus" is this repo's own binary,
// which a substring match would have happily claimed as a claude session.
func TestIsClaudeName(t *testing.T) {
	for name, want := range map[string]bool{
		"2.1.214":                  false, // the bug: the CLI's comm is its version string
		"/opt/x/claudebus":         false, // substring, not basename — cbus is not a session
		"claudebus":                false,
		"myclaude":                 false, // suffix must not match either
		"Claude":                   false, // exact case, no folding
		"":                         false,
		"sh":                       false,
		"claude":                   true,
		"/usr/local/bin/claude":    true,
		"claude-code":              true,
		"claude-":                  true, // degenerate but in-contract for claude-*
		"/nix/store/abc/claude-v2": true,
	} {
		if got := isClaudeName(name); got != want {
			t.Errorf("isClaudeName(%q) = %v, want %v", name, got, want)
		}
	}
}
