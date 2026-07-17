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

const devTrioTemplate = `{"schema":"cbus-formation/v1","name":"dev-trio","channel":"dev-trio",` +
	`"host":null,"anchorAlias":"orchestrator","peers":[` +
	`{"alias":"orchestrator","rolefile":"roles/orchestrator.md","mode":"template"},` +
	`{"alias":"coder","rolefile":"roles/coder.md","mode":"template"}]}`

// TestSaveBasedOnRepoTemplate is the declared H3 choice + H1: with no runtime file,
// save MAY read a committed template as its base (inheriting rolefile refs) but WRITES
// runtime only — the repo file is never touched.
func TestSaveBasedOnRepoTemplate(t *testing.T) {
	root := tempRepoWithTemplate(t, "dev-trio", devTrioTemplate)
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "dev-trio", "orchestrator", "sid-orch") // a live peer on the template's default channel

	repoPath := filepath.Join(root, "formations", "dev-trio.json")
	repoBefore, _ := os.ReadFile(repoPath)

	f, rep, err := SaveFormation("dev-trio", "dev-trio")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if rep.BasedOn == "" {
		t.Error("BasedOn must be set when the base is a committed template")
	}
	var orch *FormationPeer
	for i := range f.Peers {
		if f.Peers[i].Alias == "orchestrator" {
			orch = &f.Peers[i]
		}
	}
	if orch == nil || orch.Rolefile != "roles/orchestrator.md" {
		t.Errorf("the template's rolefile ref was not inherited: %+v", orch)
	}
	if orch.SessionID != "sid-orch" {
		t.Errorf("the live sid was not captured onto the inherited peer: %q", orch.SessionID)
	}
	// H1: the committed template file is byte-for-byte unchanged
	repoAfter, _ := os.ReadFile(repoPath)
	if string(repoBefore) != string(repoAfter) {
		t.Errorf("save mutated the committed template (H1 violation):\n%s", repoAfter)
	}
	// the write landed in the runtime store
	if !fileExists(filepath.Join(dir, formationsDir, "dev-trio.json")) {
		t.Error("save did not write to the runtime store")
	}
}

// TestSaveRepoBaseChannelInterplay: the template's default channel == its name, and
// save keys on channel — so basing a save on a template while targeting a different
// channel hits the repoint refusal. The channel-field interplay, pinned.
func TestSaveRepoBaseChannelInterplay(t *testing.T) {
	tempRepoWithTemplate(t, "dev-trio", devTrioTemplate) // channel "dev-trio"
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "un", "orchestrator", "sid-orch") // live on channel "un"

	_, _, err := SaveFormation("dev-trio", "un")
	if err == nil || !strings.Contains(err.Error(), "records channel") {
		t.Errorf("basing a dev-trio-channel template into 'un' must refuse the repoint, got %v", err)
	}
}

// TestCommittedTemplatesArePureAndLoad guards the repo's committed templates: they
// must reference rolefiles, never INLINE a prompt (reviewability + the canary, H5),
// must carry no personal path or identifier (public-repo face, H4), and must load and
// validate (H2 name==filename). The prompt markers are doctrine-block phrases that
// only appear if a role body was pasted into template JSON.
func TestCommittedTemplatesArePureAndLoad(t *testing.T) {
	dir, ok := repoFormationsDir()
	if !ok {
		t.Skip("not in a repo")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		t.Skip("no committed templates")
	}
	promptMarkers := []string{"Arm your listener", "Monitor tool", "cbus tail", "Standing doctrines", "NEVER Bash", "first reply (required)"}
	personalMarkers := []string{"/Users/", "carlos", ".ccs/", "carlos-mbp"}
	for _, fp := range files {
		b, err := os.ReadFile(fp)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		for _, m := range promptMarkers {
			if strings.Contains(body, m) {
				t.Errorf("%s inlines prompt/doctrine text %q — templates must REFERENCE rolefiles, not embed them", filepath.Base(fp), m)
			}
		}
		for _, m := range personalMarkers {
			if strings.Contains(body, m) {
				t.Errorf("%s carries a personal identifier %q — a committed template is the public-repo face", filepath.Base(fp), m)
			}
		}
		name := strings.TrimSuffix(filepath.Base(fp), ".json")
		if _, err := loadFormationFileAt(fp, name); err != nil {
			t.Errorf("%s does not load/validate: %v", filepath.Base(fp), err)
		}
	}
}

// TestDevTrioStarterShape pins the starter's contents: four roles, all template, no
// sids, no drift, models deferred (blank -> resolved from roles/*.md at apply).
func TestDevTrioStarterShape(t *testing.T) {
	dir, ok := repoFormationsDir()
	if !ok {
		t.Skip("not in a repo")
	}
	f, err := loadFormationFileAt(filepath.Join(dir, "dev-trio.json"), "dev-trio")
	if err != nil {
		t.Fatalf("dev-trio: %v", err)
	}
	if f.Channel != "dev-trio" || f.AnchorAlias != "orchestrator" || len(f.DriftAnchors) != 0 {
		t.Errorf("dev-trio shape: channel=%q anchor=%q drift=%v", f.Channel, f.AnchorAlias, f.DriftAnchors)
	}
	want := map[string]string{"orchestrator": "roles/orchestrator.md", "coder": "roles/coder.md",
		"reviewer": "roles/reviewer.md", "documenter": "roles/documenter.md"}
	if len(f.Peers) != len(want) {
		t.Fatalf("want %d peers, got %d", len(want), len(f.Peers))
	}
	for _, p := range f.Peers {
		if p.Mode != ModeTemplate || p.SessionID != "" || p.Model != "" {
			t.Errorf("%s: mode=%q sid=%q model=%q — want template, no sid, model deferred", p.Alias, p.Mode, p.SessionID, p.Model)
		}
		if p.Rolefile != want[p.Alias] {
			t.Errorf("%s: rolefile=%q want %q", p.Alias, p.Rolefile, want[p.Alias])
		}
		if strings.Contains(p.Rolefile, "@") {
			t.Errorf("%s: rolefile is pinned (%q) — a committed template must not pin (D15 scrub)", p.Alias, p.Rolefile)
		}
	}
}
