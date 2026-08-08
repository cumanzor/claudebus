package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/client"
)

// reviewer1 round 3, finding 3: a split run must be SURFACED at the user-facing call
// site, not merely recorded in SaveReport. A report field no path renders reports to
// nobody — the same shape as the permanently-zero bookkeeping removed in mec.1.
//
// The save must still SUCCEED: it exists precisely for pausing/dying formations, and
// refusing to save a split would destroy the evidence of the split.
func TestFormationSaveWarnsOnRunConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SA") // the saver is peer a, so the mint resolves an anchor

	// two live peers claiming DIFFERENT runs: a split
	for _, p := range []struct{ alias, sid, run string }{
		{"a", "SA", "run_X"}, {"b", "SB", "run_Y"},
	} {
		dir := filepath.Join(root, "cc", p.alias)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		meta := `{"alias":"` + p.alias + `","channel":"cc","sessionId":"` + p.sid +
			`","lastActivity":"` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + `"}`
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".run"), []byte(p.run+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var rc int
	out := captureStdout(t, func() { rc = runFormationSave([]string{"cc", "cc"}) })

	if rc != 0 {
		t.Fatalf("a split formation must still save (evidence of the split), rc=%d", rc)
	}
	if !strings.Contains(out, "run_X") || !strings.Contains(out, "run_Y") {
		t.Errorf("the conflicting run ids were not named at the CLI:\n%s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "WARNING") {
		t.Errorf("a split saved with no warning at the user-facing call site:\n%s", out)
	}
	// and the envelope stays blank rather than picking one
	f, err := client.LoadFormation("cc")
	if err != nil {
		t.Fatal(err)
	}
	if f.FormationRunID != "" {
		t.Errorf("envelope picked a run despite the split: %q", f.FormationRunID)
	}
}
