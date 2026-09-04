package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJoinStampsHarness: join records the OWNING harness per peer, so a mixed channel
// can be told apart peer by peer. The seam supplies an ancestry a test binary lacks.
func TestJoinStampsHarness(t *testing.T) {
	setupStore(t)
	old := harnessNameFn
	harnessNameFn = func() string { return "codex" }
	t.Cleanup(func() { harnessNameFn = old })

	if _, _, err := Join("dev", "worker"); err != nil {
		t.Fatal(err)
	}
	m, ok := ReadPeerMeta(filepath.Join(CBUSDir(), "dev", "worker", "meta.json"))
	if !ok || m.Harness != "codex" {
		t.Fatalf("harness = %q, want codex", m.Harness)
	}
}

// TestReserveDoesNotStampHarness: HarnessName reads the CALLER's ancestry, so a
// reserving parent would stamp ITS harness onto a child that has not booted. The
// child's own join is the only party that knows. Same rule the ledger's spawn event
// already follows by zeroing ev.Harness.
func TestReserveDoesNotStampHarness(t *testing.T) {
	setupStore(t)
	old := harnessNameFn
	harnessNameFn = func() string { return "codex" }
	t.Cleanup(func() { harnessNameFn = old })

	if _, err := ReserveAlias("dev", "child", "fresh", ""); err != nil {
		t.Fatal(err)
	}
	m, ok := ReadPeerMeta(filepath.Join(CBUSDir(), "dev", "child", "meta.json"))
	if !ok {
		t.Fatal("no reservation meta")
	}
	if m.Harness != "" {
		t.Fatalf("reservation stamped harness = %q; the reserving parent's harness is not the child's", m.Harness)
	}
}

// TestRenamePreservesHarness: every meta rewriter must carry the field or it is
// silently dropped — the hazard the listenerStart comment already warns about.
func TestRenamePreservesHarness(t *testing.T) {
	root := setupStore(t)
	old := harnessNameFn
	harnessNameFn = func() string { return "grok" }
	t.Cleanup(func() { harnessNameFn = old })

	if _, _, err := Join("dev", "before"); err != nil {
		t.Fatal(err)
	}
	if err := renameMeta(filepath.Join(root, "dev", "before"), "after"); err != nil {
		t.Fatal(err)
	}
	m, ok := ReadPeerMeta(filepath.Join(root, "dev", "before", "meta.json"))
	if !ok || m.Harness != "grok" {
		t.Fatalf("harness after rename = %q, want grok", m.Harness)
	}
	if m.Alias != "after" {
		t.Fatalf("alias = %q, want after (rename did not apply)", m.Alias)
	}
}

// TestReadPeerMetaSurfacesHarness: a hand-written meta (bash-era shape plus harness)
// reads through, and an absent field reads blank rather than failing the parse.
func TestReadPeerMetaSurfacesHarness(t *testing.T) {
	dir := t.TempDir()
	with := filepath.Join(dir, "with.json")
	without := filepath.Join(dir, "without.json")
	if err := os.WriteFile(with, []byte(`{"alias":"a","channel":"c","listenerPid":null,"harness":"opencode"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(without, []byte(`{"alias":"a","channel":"c","listenerPid":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, ok := ReadPeerMeta(with); !ok || m.Harness != "opencode" {
		t.Errorf("harness = %q, want opencode", m.Harness)
	}
	if m, ok := ReadPeerMeta(without); !ok || m.Harness != "" {
		t.Errorf("absent harness = %q, want blank", m.Harness)
	}
}
