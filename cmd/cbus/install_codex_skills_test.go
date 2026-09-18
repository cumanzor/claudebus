package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"claudebus"
)

func TestInstallCodexSkillsShaGuard(t *testing.T) {
	dst := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	shipped, err := claudebus.CodexSkills.ReadFile("skills/codex/cbus-connect/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(dst, "cbus-connect", "SKILL.md")
	checkInstall := func(args []string, wantCode int, outcome string) {
		t.Helper()
		out := captureStdout(t, func() {
			if code := runInstallCodexSkills(args); code != wantCode {
				t.Fatalf("install rc = %d, want %d", code, wantCode)
			}
		})
		if !strings.Contains(out, "cbus-connect/SKILL.md") || !strings.Contains(out, outcome) {
			t.Errorf("missing per-skill outcome %q:\n%s", outcome, out)
		}
	}
	checkContent := func(want []byte) {
		t.Helper()
		got, err := os.ReadFile(installed)
		if err != nil || string(got) != string(want) {
			t.Fatalf("installed content mismatch: err=%v", err)
		}
	}

	checkInstall([]string{"--path", dst}, 0, "installed")
	checkContent(shipped)
	checkInstall([]string{"--path", dst}, 0, "up-to-date")
	if _, err := os.Stat(filepath.Join(codexHome, "skills")); !os.IsNotExist(err) {
		t.Fatalf("--path must override CODEX_HOME; unexpected default install: %v", err)
	}
	edited := []byte("local changes")
	if err := os.WriteFile(installed, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	checkInstall([]string{"--path", dst}, 1, "SKIPPED")
	checkContent(edited)
	checkInstall([]string{"--path", dst, "--force"}, 0, "installed")
	checkContent(shipped)
}

func TestInstallCodexSkillsDefaults(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "home fallback"
		if configured {
			name = "CODEX_HOME"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			testHome(t, home)
			t.Setenv("CODEX_HOME", "")
			want := filepath.Join(home, ".codex", "skills")
			if configured {
				config := filepath.Join(home, "custom-codex")
				t.Setenv("CODEX_HOME", config)
				want = filepath.Join(config, "skills")
			}
			captureStdout(t, func() {
				if code := runInstallCodexSkills(nil); code != 0 {
					t.Fatalf("default install rc = %d", code)
				}
			})
			if _, err := os.Stat(filepath.Join(want, "cbus-connect", "SKILL.md")); err != nil {
				t.Fatalf("skill missing from default destination %q: %v", want, err)
			}
		})
	}
}

func TestCodexTrustedSetupKeepsPermissionOptInAndEdits(t *testing.T) {
	home, skills := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	run := func(want int, args ...string) {
		t.Helper()
		captureStdout(t, func() {
			if got := runInstallCodexSkills(append([]string{"--path", skills}, args...)); got != want {
				t.Fatalf("%v: exit=%d want=%d", args, got, want)
			}
		})
	}
	rules := filepath.Join(home, "rules", "cbus.rules")
	run(0)
	if _, err := os.Stat(rules); !os.IsNotExist(err) {
		t.Fatal("ordinary skill installation granted permissions")
	}
	run(0, "--with-permissions")
	bus, err := os.ReadFile(rules)
	if err != nil || !strings.Contains(string(bus), `pattern = ["cbus"]`) {
		t.Fatalf("trusted setup did not install bus permissions in active Codex home: %v", err)
	}
	run(0) // The plain refresh used by selfupdate must retain the bus opt-in.
	retained, _ := os.ReadFile(rules)
	if string(retained) != string(bus) {
		t.Fatal("plain skill refresh changed permission scope")
	}
	edited := []byte("# locally managed permissions\n")
	if err := os.WriteFile(rules, edited, 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "--with-permissions", "--force")
	retained, _ = os.ReadFile(rules)
	if string(retained) != string(edited) {
		t.Fatal("skill --force overwrote locally managed permissions")
	}
	run(0)
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("trusted setup changed general Codex configuration")
	}
	if _, err := os.Stat(filepath.Join(skills, "cbus-connect", "SKILL.md")); err != nil {
		t.Fatal("trusted setup did not honor custom skill destination")
	}
}

func TestInstallCodexSkillsOnlyEntrypoints(t *testing.T) {
	embedded := fstest.MapFS{
		"skills/codex/one/SKILL.md":          {Data: []byte("skill one")},
		"skills/codex/one/README.md":         {Data: []byte("not installed")},
		"skills/codex/one/scripts/helper.sh": {Data: []byte("not installed")},
		"skills/codex/two/SKILL.md":          {Data: []byte("skill two")},
	}
	dst := t.TempDir()
	results, err := installCodexSkills(embedded, dst, false)
	if err != nil || len(results) != 2 {
		t.Fatalf("install results = %v, err=%v", results, err)
	}
	for _, result := range results {
		if result.outcome != "installed" {
			t.Errorf("install result = %+v", result)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dst, "one"))
	if err != nil || len(entries) != 2 || entries[0].Name() != codexSkillReceipt || entries[1].Name() != "SKILL.md" {
		t.Fatalf("only SKILL.md and its install receipt should be installed: %v, err=%v", entries, err)
	}
}

func TestCodexSkillUpgradePreservesLocalEdits(t *testing.T) {
	for _, state := range []string{"shipped", "edited", "missing-receipt", "corrupt-receipt"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			old, next := []byte("old shipped skill"), []byte("new shipped skill")
			if r := installCodexSkill(dir, "skill", old, false); r.outcome != "installed" {
				t.Fatal(r)
			}
			path := filepath.Join(dir, "SKILL.md")
			want := old
			switch state {
			case "shipped":
				want = next
			case "edited":
				want = []byte("user customization")
				if err := os.WriteFile(path, want, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				if err := os.Remove(filepath.Join(dir, codexSkillReceipt)); err != nil {
					t.Fatal(err)
				}
			case "corrupt-receipt":
				if err := os.WriteFile(filepath.Join(dir, codexSkillReceipt), []byte("not-a-hash"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := installCodexSkill(dir, "skill", next, false)
			if (state == "shipped" && r.outcome != "installed") || (state != "shipped" && r.outcome != "skipped") {
				t.Fatalf("unsafe upgrade result: %+v", r)
			}
			b, err := os.ReadFile(path)
			if err != nil || string(b) != string(want) {
				t.Fatalf("wrong installed bytes: %q, %v", b, err)
			}
			if r := installCodexSkill(dir, "skill", next, true); r.outcome != "installed" && r.outcome != "up-to-date" {
				t.Fatalf("explicit override failed: %+v", r)
			}
		})
	}
}

func TestCodexSkillNeverFollowsAssetOrReceiptSymlink(t *testing.T) {
	for _, name := range []string{"SKILL.md", codexSkillReceipt} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
				t.Skip(err)
			}
			if r := installCodexSkill(dir, "skill", []byte("new"), true); r.outcome != "failed" {
				t.Fatal(r)
			}
			b, err := os.ReadFile(outside)
			if err != nil || string(b) != "keep" {
				t.Fatal("followed a destination symlink")
			}
		})
	}
}

func TestInstallCodexSkillsRejectsSymlinkDirectory(t *testing.T) {
	dst, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dst, "cbus-connect")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	results, err := installCodexSkills(claudebus.CodexSkills, dst, true)
	if err != nil || len(results) != 1 || results[0].outcome != "failed" {
		t.Fatalf("symlink destination must fail even with --force: %v, err=%v", results, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("installer must not write through a symlink directory: %v", err)
	}
}

type renamedSkillEntry struct {
	fs.DirEntry
	name string
}

func (e renamedSkillEntry) Name() string { return e.name }

type invalidSkillNameFS struct {
	fs.FS
	name string
}

func (f invalidSkillNameFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, err
	}
	entries[0] = renamedSkillEntry{DirEntry: entries[0], name: f.name}
	return entries, nil
}

func TestInstallCodexSkillsRejectsTraversal(t *testing.T) {
	for _, name := range []string{".", "..", "../outside", "/outside", "a/b", `a\b`} {
		t.Run(name, func(t *testing.T) {
			embedded := invalidSkillNameFS{
				FS:   fstest.MapFS{"skills/codex/valid/SKILL.md": {Data: []byte("skill")}},
				name: name,
			}
			dst := t.TempDir()
			results, err := installCodexSkills(embedded, dst, true)
			if err != nil || len(results) != 1 || results[0].outcome != "failed" || results[0].reason != "invalid embedded skill directory name" {
				t.Fatalf("invalid skill name must be rejected: %v, err=%v", results, err)
			}
			entries, err := os.ReadDir(dst)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid skill name must not install files: %v, err=%v", entries, err)
			}
		})
	}
}
