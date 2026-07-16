package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadFormationRefusesNameMismatch is the reviewer's C1 repro, verbatim: copy
// an envelope to a second filename, load it under that name, and save. Before the
// fix the copy wrote itself over the ORIGINAL (Save derives its path from the name
// field), so making a backup and touching it destroyed the thing backed up.
func TestLoadFormationRefusesNameMismatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	rolesPath := filepath.Join(dir, formationsDir, "roles.json")
	original, err := os.ReadFile(rolesPath)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, formationsDir, "backup.json")
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadFormation("backup")
	if err == nil {
		t.Fatalf("want a refusal, got a formation named %q", got.Name)
	}
	for _, want := range []string{"backup.json", `records name "roles"`, "must agree"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should carry %q, got: %v", want, err)
		}
	}
	// the original is untouched, which is the whole point
	after, err := os.ReadFile(rolesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Errorf("the original formation was modified:\n%s", after)
	}
}

// TestLoadFormationNameMatchStillLoads: the guard must not break the ordinary path.
func TestLoadFormationNameMatchStillLoads(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := LoadFormation("roles")
	if err != nil {
		t.Fatalf("a matching name must load: %v", err)
	}
	if back.Name != "roles" {
		t.Errorf("name = %q want roles", back.Name)
	}
}

// TestFormationRenameByFieldAndFile documents the supported rename: set the name
// field and move the file to match. Either half alone is refused — loudly, with the
// remedy in the message — because either half alone is also exactly how the C1
// clobber began.
func TestFormationRenameByFieldAndFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	fdir := filepath.Join(dir, formationsDir)

	// half a rename: file moved, field stale -> refused
	if err := os.Rename(filepath.Join(fdir, "roles.json"), filepath.Join(fdir, "roles-v2.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFormation("roles-v2"); err == nil {
		t.Error("a moved file with a stale name field must be refused")
	} else if !strings.Contains(err.Error(), `set name to "roles-v2"`) {
		t.Errorf("the error must name the remedy, got: %v", err)
	}

	// the whole rename: field set to match the new filename -> loads, and saves
	// back to the new path, leaving no ghost at the old one
	f.Name = "roles-v2"
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := LoadFormation("roles-v2")
	if err != nil {
		t.Fatalf("a completed rename must load: %v", err)
	}
	if back.Name != "roles-v2" {
		t.Errorf("name = %q want roles-v2", back.Name)
	}
	if _, err := os.Stat(filepath.Join(fdir, "roles.json")); !os.IsNotExist(err) {
		t.Error("the old filename should not have been recreated")
	}
}

// TestListFormationsSurfacesNameMismatch: a mismatched file must not vanish from
// the listing — it is still on disk, and list is where a user would find it.
func TestListFormationsSurfacesNameMismatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, formationsDir, "roles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, formationsDir, "backup.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ListFormations()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want both files listed, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Name == "backup" && e.Err == nil {
			t.Error("the mismatched copy must be listed with its error")
		}
	}
}
