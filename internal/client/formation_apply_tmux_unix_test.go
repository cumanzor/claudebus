//go:build darwin || linux

package client

import (
	"os"
	"path/filepath"
	"testing"
)

// Every case here calls fakeTmux, and fakeTmux can only describe a unix machine: it
// writes an extension-less `#!/bin/sh` file named `tmux` and joins PATH with ":". On
// windows neither half works — PATHEXT will not run an extension-less file and the list
// separator is ";" — so the pane query returns nothing and the anchor logic is never
// reached. Cases that still went green there went green over EMPTY pane data, which is
// worse than a red: a fixture that describes nothing cannot fail. Tagged rather than
// skipped because the subject is the tmux backend itself, which windows does not have
// (D55, D57 item 4).

// fakeTmux puts a `tmux` on PATH that reports fixed pane geometry and marks the
// session as tmux-hosted, so PaneAnchor's real selection path runs end-to-end
// without a multiplexer. Only the geometry query reaches it — the fork itself goes
// through the injected forker.
func fakeTmux(t *testing.T, geometry string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'GEO'\n" + geometry + "\nGEO\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-fake,1,0")
	t.Setenv("TMUX_PANE", "%0")
}

// TestApplyChainsPaneAnchors is the feature in one assertion: with every pane the
// same size, each split anchors on the pane created just before it, so a run walks
// %0 -> %1 -> %2 instead of hammering the applier. The applier is first in the
// candidate list and loses every tie, which is what keeps it big.
func TestApplyChainsPaneAnchors(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fakeTmux(t, "%0 80 24\n%1 80 24\n%2 80 24")
	applierOn(t, "ch", "applier")

	f := applyFixture(panePeer("orchestrator"), panePeer("coder"), panePeer("documenter"))
	fk := &recForker{ids: []string{"%1", "%2"}}
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(fk.specs) != 3 {
		t.Fatalf("want 3 launches, got %d", len(fk.specs))
	}
	for i, want := range []string{"%0", "%1", "%2"} {
		if got := fk.specs[i].Anchor; got != want {
			t.Errorf("launch %d anchored on %q, want %q (the chain collapsed onto the applier)", i, got, want)
		}
	}
}

// TestApplyAnchorsOnTheLargestPane: when the applier stays the biggest surface, every
// split targets IT rather than chaining — largest-area is the rule, the chain is just
// what equal-sized panes produce.
func TestApplyAnchorsOnTheLargestPane(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fakeTmux(t, "%0 200 60\n%1 20 10\n%2 20 10")
	applierOn(t, "ch", "applier")

	f := applyFixture(panePeer("orchestrator"), panePeer("coder"), panePeer("documenter"))
	fk := &recForker{ids: []string{"%1", "%2"}}
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i, s := range fk.specs {
		if s.Anchor != "%0" {
			t.Errorf("launch %d anchored on %q, want the largest pane %%0", i, s.Anchor)
		}
	}
}

// TestApplyNeverAnchorsOnAnEmptyID: window/tab launches name no surface, so they must
// not enter the candidate set. A "" candidate would be a split targeting nothing.
func TestApplyNeverAnchorsOnAnEmptyID(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fakeTmux(t, "%0 80 24\n%1 80 24")
	applierOn(t, "ch", "applier")

	f := applyFixture(
		panePeer("orchestrator", func(p *FormationPeer) { p.Target = "tab" }), // names no surface
		panePeer("coder"),
		panePeer("documenter"),
	)
	fk := &recForker{ids: []string{"", "%1"}} // the tab launch returns no id
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// the tab peer is not a pane, so it gets no anchor at all
	if fk.specs[0].Anchor != "" {
		t.Errorf("a tab launch must not be given an anchor, got %q", fk.specs[0].Anchor)
	}
	for i, s := range fk.specs[1:] {
		if !validTmuxPaneID(s.Anchor) {
			t.Errorf("pane launch %d anchored on %q, which is not a pane id", i+1, s.Anchor)
		}
	}
	// and the chain still advanced onto the pane the previous split created
	if fk.specs[2].Anchor != "%1" {
		t.Errorf("last launch anchored on %q, want %%1", fk.specs[2].Anchor)
	}
}

// TestApplyNoNormalizeIsRunLevel is the mixed-file case: ONE peer declaring a
// direction suppresses the tmux main-vertical reflow for EVERY pane fork in the run,
// including its auto siblings. Without that, an auto sibling's reflow stomps the
// layout the file explicitly asked for — the exact bug the rule exists to prevent.
func TestApplyNoNormalizeIsRunLevel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fakeTmux(t, "%0 80 24\n%1 80 24")
	applierOn(t, "ch", "applier")

	f := applyFixture(
		panePeer("orchestrator", func(p *FormationPeer) { p.Split = "right" }),
		panePeer("coder"), // auto: no direction of its own
	)
	fk := &recForker{ids: []string{"%1", "%2"}}
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i, s := range fk.specs {
		if !s.NoNormalize {
			t.Errorf("launch %d (%s) did not suppress the normalize — an auto sibling will reflow the declared layout", i, s.Split)
		}
	}
	// the declared direction still rides on the peer that declared it, and only it
	if fk.specs[0].Split != "right" || fk.specs[1].Split != "" {
		t.Errorf("split directions = %q, %q; want right, auto", fk.specs[0].Split, fk.specs[1].Split)
	}
}

// TestApplyNormalizeStaysOnForAnAllAutoFile: with no declared direction anywhere,
// tmux keeps today's behavior exactly — the suppression is opt-in via the envelope.
func TestApplyNormalizeStaysOnForAnAllAutoFile(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fakeTmux(t, "%0 80 24\n%1 80 24")
	applierOn(t, "ch", "applier")

	f := applyFixture(panePeer("orchestrator"), panePeer("coder"))
	fk := &recForker{ids: []string{"%1", "%2"}}
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i, s := range fk.specs {
		if s.NoNormalize {
			t.Errorf("launch %d suppressed the normalize with no declared direction in the file", i)
		}
	}
}
