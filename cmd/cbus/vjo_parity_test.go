package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cbus-vjo. A directory in the store root that is not a channel used to appear in
// `list --json` as a phantom entry with an empty peers array, while the text `list`,
// `channels`, and `channels --json` all correctly showed nothing for it. Three
// surfaces to one, and the dissenter was the JSON twin of the text path it was
// supposed to mirror.
//
// The fixture mints that directory the way the real one is minted: `cbus install-roles`
// writes $CBUS_DIR/roles beside the channels. That is the door that created the defect,
// and it is the reason no earlier fixture could reach this state — every test channel
// came from `join`, and join always creates a peer along with the directory.
func TestPeerlessStoreDirIsNotAChannel(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-vjo")

	if rc := captureRC(t, func() int { return run([]string{"install-roles"}) }); rc != 0 {
		t.Fatalf("install-roles rc=%d", rc)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "roles")); err != nil || len(entries) == 0 {
		t.Fatalf("install-roles did not create a populated roles dir: %v", err)
	}
	if rc := captureRC(t, func() int { return run([]string{"join", "demo", "alpha"}) }); rc != 0 {
		t.Fatal("setup join failed")
	}

	// every surface must agree on which channels exist
	doc := decodeList(t, captureStdout(t, func() { run([]string{"list", "--json"}) }))
	if names := channelNames(doc); len(names) != 1 || names[0] != "demo" {
		t.Errorf("list --json channels = %v, want [demo] — a store dir is not a channel", names)
	}
	text := captureStdout(t, func() { run([]string{"list"}) })
	if strings.Contains(text, "roles") {
		t.Errorf("text list mentions roles:\n%s", text)
	}
	chText := captureStdout(t, func() { run([]string{"channels"}) })
	if strings.Contains(chText, "roles") {
		t.Errorf("text channels mentions roles:\n%s", chText)
	}

	// the chosen-filter case: naming the non-channel yields an empty document, not an
	// entry for it
	filtered := decodeList(t, captureStdout(t, func() { run([]string{"list", "--json", "roles"}) }))
	if len(filtered.Channels) != 0 {
		t.Errorf("list --json roles = %+v, want no channels", filtered.Channels)
	}
}

// The exemption a careless one-liner would break: a legacy v1 entry is peerless BY
// CONSTRUCTION (it predates the alias level), so the drop rule must not reach it. R18
// requires it visible so a GUI can surface the prune remedy.
func TestLegacyV1SurvivesThePeerlessDrop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-vjo-legacy")

	if rc := captureRC(t, func() int { return run([]string{"install-roles"}) }); rc != 0 {
		t.Fatalf("install-roles rc=%d", rc)
	}
	// hand-staged with its writer named: only the retired bash v1 client wrote a
	// channel-level meta.json, when a peer lived at $CBUS_DIR/<alias>/meta.json with no
	// channel above it (docs/architecture/protocol.md:139).
	if err := os.MkdirAll(filepath.Join(root, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old", "meta.json"), []byte(`{"alias":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := decodeList(t, captureStdout(t, func() { run([]string{"list", "--json"}) }))
	names := channelNames(doc)
	if len(names) != 1 || names[0] != "old" {
		t.Fatalf("channels = %v, want only the legacy entry kept", names)
	}
	if !doc.Channels[0].LegacyV1 || doc.Channels[0].Peers == nil || len(doc.Channels[0].Peers) != 0 {
		t.Errorf("legacy entry = %+v, want legacyV1 with an empty peers array", doc.Channels[0])
	}
	// and the text path still prints its remedy row
	if text := captureStdout(t, func() { run([]string{"list"}) }); !strings.Contains(text, "legacy v1 entry") {
		t.Errorf("text list lost the legacy row:\n%s", text)
	}
}

func channelNames(doc listJSON) []string {
	var out []string
	for _, c := range doc.Channels {
		out = append(out, c.Name)
	}
	return out
}
