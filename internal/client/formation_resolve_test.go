package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tempRepoWithTemplate makes a throwaway git repo containing formations/<name>.json
// and chdirs into it, so repoFormationsDir (git rev-parse) resolves to it rather than
// the real claudebus checkout. Returns the repo root.
func tempRepoWithTemplate(t *testing.T, name, body string) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	fdir := filepath.Join(root, "formations")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(fdir, name+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

const resolveTemplate = `{"schema":"cbus-formation/v1","name":"dev-trio","channel":"dev-trio",` +
	`"host":null,"anchorAlias":"orchestrator","peers":[` +
	`{"alias":"orchestrator","rolefile":"roles/orchestrator.md","mode":"template"}]}`

func TestResolveFormationRuntimeFirst(t *testing.T) {
	// a temp repo holds dev-trio; runtime is empty -> resolves from the repo.
	tempRepoWithTemplate(t, "dev-trio", resolveTemplate)
	t.Setenv("CBUS_DIR", t.TempDir())

	f, source, err := ResolveFormation("dev-trio")
	if err != nil {
		t.Fatalf("repo resolve: %v", err)
	}
	if f.Name != "dev-trio" || !strings.Contains(source, "committed template") {
		t.Errorf("want the committed template, got name=%q source=%q", f.Name, source)
	}

	// now a runtime save of the SAME name must SHADOW the committed one (D20).
	rt := f
	rt.Channel = "runtime-chan"
	if err := rt.Save(); err != nil {
		t.Fatal(err)
	}
	f2, source2, err := ResolveFormation("dev-trio")
	if err != nil {
		t.Fatal(err)
	}
	if f2.Channel != "runtime-chan" || !strings.Contains(source2, "runtime store") {
		t.Errorf("runtime must shadow the template, got channel=%q source=%q", f2.Channel, source2)
	}
}

func TestResolveFormationNotFoundAndTorn(t *testing.T) {
	tempRepoWithTemplate(t, "ignored", "") // repo with no matching template
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, _, err := ResolveFormation("ghost"); err == nil || !strings.Contains(err.Error(), "no formation") {
		t.Errorf("missing everywhere: %v", err)
	}
	// a TORN runtime file stops the resolve — it must NOT silently fall through to a repo template
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	tempRepoWithTemplate(t, "dev-trio", resolveTemplate)
	fdir := filepath.Join(dir, formationsDir)
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "dev-trio.json"), []byte(`{"schema":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveFormation("dev-trio"); err == nil {
		t.Error("a torn runtime file must stop the resolve, not fall through to the repo template")
	}
}

// TestResolveEnforcesNameMatchInRepo is H2: C1 applies to committed templates too.
func TestResolveEnforcesNameMatchInRepo(t *testing.T) {
	// the file is named misfiled.json but the envelope says name "dev-trio"
	tempRepoWithTemplate(t, "misfiled", resolveTemplate)
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, _, err := ResolveFormation("misfiled"); err == nil || !strings.Contains(err.Error(), "must agree") {
		t.Errorf("repo template name!=filename must be refused, got %v", err)
	}
}

// TestRemoveFormationRefusesRepoTemplate is D22: rm never deletes a committed file.
func TestRemoveFormationRefusesRepoTemplate(t *testing.T) {
	root := tempRepoWithTemplate(t, "dev-trio", resolveTemplate)
	t.Setenv("CBUS_DIR", t.TempDir())
	_, err := RemoveFormation("dev-trio")
	if err == nil || !strings.Contains(err.Error(), "committed template") || !strings.Contains(err.Error(), "git") {
		t.Errorf("rm of a repo-only template must refuse and point at git, got %v", err)
	}
	// the committed file is untouched
	if _, statErr := os.Stat(filepath.Join(root, "formations", "dev-trio.json")); statErr != nil {
		t.Errorf("rm deleted a committed template: %v", statErr)
	}
}
