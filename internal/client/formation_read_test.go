package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func strptr(s string) *string { return &s }

// TestPeerSidState covers the four states, including the one that matters most:
// a peer recorded on ANOTHER machine is unchecked, not stale. This host cannot see
// that host's transcripts, and reporting "stale" would be a guess dressed as a
// finding — the restore post-mortem's whole lesson.
func TestPeerSidState(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ccs", "instances", "personal")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("HOME", home)
	live := "a26d120e-4d73-4d91-8550-498ab65a5107"
	writeTranscript(t, cfg, "-Users-dev-repos-AI-claudebus", live)
	here := ShortHostname()

	for _, tc := range []struct {
		name string
		peer FormationPeer
		want SidState
	}{
		{"no sid", FormationPeer{Alias: "a"}, SidNone},
		{"reservation placeholder", FormationPeer{Alias: "a", SessionID: "reserved"}, SidNone},
		{"transcript present", FormationPeer{Alias: "a", SessionID: live}, SidPresent},
		{"transcript gone", FormationPeer{Alias: "a", SessionID: "dead-dead-dead"}, SidStale},
		{"gone but this machine", FormationPeer{Alias: "a", SessionID: "dead-dead-dead", Machine: here}, SidStale},
		{"gone on another machine", FormationPeer{Alias: "a", SessionID: "dead-dead-dead", Machine: "nuc"}, SidUnchecked},
		{"present beats a foreign machine tag", FormationPeer{Alias: "a", SessionID: live, Machine: "nuc"}, SidPresent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := tc.peer.SidState()
			if got != tc.want {
				t.Errorf("SidState: got %v (%s) want %v", got, detail, tc.want)
			}
		})
	}
}

func TestPeerRoleTODO(t *testing.T) {
	for _, tc := range []struct {
		name string
		peer FormationPeer
		want bool
	}{
		{"committed rolefile", FormationPeer{Rolefile: "roles/coder.md@b3a806e"}, false},
		{"freeform text", FormationPeer{Role: strptr("Implements the milestones.")}, false},
		{"nothing at all", FormationPeer{}, true},
		{"explicit null role", FormationPeer{Role: nil}, true},
		{"empty text", FormationPeer{Role: strptr("   ")}, true},
		{"todo marker", FormationPeer{Role: strptr("TODO: ask the peer to self-describe")}, true},
		{"lowercase todo marker", FormationPeer{Role: strptr("todo — unknown")}, true},
		{"rolefile wins over a todo text", FormationPeer{Rolefile: "roles/x.md@abc", Role: strptr("TODO")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.peer.RoleTODO(); got != tc.want {
				t.Errorf("RoleTODO: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestListFormations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)

	if got, err := ListFormations(); err != nil || got != nil {
		t.Fatalf("no formations dir: got (%v,%v) want (nil,nil)", got, err)
	}

	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	f2 := loadExample(t)
	f2.Name, f2.Channel = "b31", "un"
	if err := f2.Save(); err != nil {
		t.Fatal(err)
	}
	fdir := filepath.Join(dir, formationsDir)
	// an unreadable envelope must be LISTED with its error, never skipped
	if err := os.WriteFile(filepath.Join(fdir, "broken.json"), []byte(`{"schema":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// noise that must not appear: a write-in-progress temp and a non-json file
	if err := os.WriteFile(filepath.Join(fdir, ".formation.tmp.999"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "notes.txt"), []byte(`hi`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ListFormations()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	want := []string{"b31", "broken", "roles"} // ReadDir order = name-sorted
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names: got %v want %v", names, want)
	}
	for _, e := range entries {
		switch e.Name {
		case "broken":
			if e.Err == nil {
				t.Error("broken.json must be listed WITH its error")
			}
			if e.F != nil {
				t.Error("broken.json must not yield a formation")
			}
		default:
			if e.Err != nil {
				t.Errorf("%s: unexpected error %v", e.Name, e.Err)
			}
			if e.F == nil {
				t.Errorf("%s: want a loaded formation", e.Name)
			}
		}
	}
}

func TestRemoveFormation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	if _, err := RemoveFormation("ghost"); err == nil || !strings.Contains(err.Error(), "no formation") {
		t.Errorf("missing: want a no-formation error, got %v", err)
	}
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := RemoveFormation("roles")
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present after rm: %v", err)
	}
	if _, err := RemoveFormation("roles"); err == nil {
		t.Error("second rm: want an error")
	}
	for _, bad := range []string{"a/b", "..", ""} {
		if _, err := RemoveFormation(bad); err == nil {
			t.Errorf("RemoveFormation(%q) must be refused", bad)
		}
	}
}

// TestRemoveFormationCannotReachOutsideTheDir: rm takes a NAME, and the name is the
// only thing between a user typo and an arbitrary unlink.
func TestRemoveFormationCannotReachOutsideTheDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	victim := filepath.Join(dir, "important.json")
	if err := os.WriteFile(victim, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../important", "..%2Fimportant", "../../etc/passwd"} {
		if _, err := RemoveFormation(bad); err == nil {
			t.Errorf("RemoveFormation(%q) must be refused", bad)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("a file outside .formations was removed: %v", err)
	}
}

// TestListFormationsSurvivesATornWrite: list must not blow up on a half-written or
// foreign file; it reports the row and moves on.
func TestListFormationsSurvivesATornWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	fdir := filepath.Join(dir, formationsDir)
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "torn.json"), []byte(`{"schema":"cbus-formation/v1","na`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ListFormations()
	if err != nil {
		t.Fatalf("list must tolerate a torn file: %v", err)
	}
	if len(entries) != 1 || entries[0].Err == nil {
		t.Fatalf("want one row carrying an error, got %+v", entries)
	}
}

// TestFormationExtraSurvivesPeerStatus guards against a status helper mutating the
// envelope it inspects (show must be read-only).
func TestFormationExtraSurvivesPeerStatus(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := loadExample(t)
	f.Peers[0].Extra = map[string]json.RawMessage{"notes": json.RawMessage(`"keep"`)}
	before, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Peers {
		_, _ = f.Peers[i].SidState()
		_ = f.Peers[i].RoleTODO()
	}
	after, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("inspecting a peer mutated the envelope:\n%s\n%s", before, after)
	}
}
