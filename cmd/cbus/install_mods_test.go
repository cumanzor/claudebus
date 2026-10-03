package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestInstallModsShaGuard(t *testing.T) {
	dst := t.TempDir()
	manifest := filepath.Join(dst, "cbus-compact", ".claude-plugin", "plugin.json")
	module := filepath.Join(dst, "cbus-compact", "hooks", "register.tsx")

	out := captureStdout(t, func() {
		if rc := runInstallMods([]string{"--path", dst}); rc != 0 {
			t.Fatalf("fresh install rc=%d", rc)
		}
	})
	if !strings.Contains(out, "cbus-compact/hooks/register.tsx") || !strings.Contains(out, "installed") {
		t.Errorf("fresh install output:\n%s", out)
	}
	for _, p := range []string{manifest, module, filepath.Join(dst, "cbus-compact", "hooks", "hooks.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s not installed: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "cbus-compact", "hooks", "cbus-compact.test.ts")); err == nil {
		t.Error("plugin tests must not be installed")
	}

	out = captureStdout(t, func() {
		if rc := runInstallMods([]string{"--path", dst}); rc != 0 {
			t.Fatalf("re-run rc=%d", rc)
		}
	})
	if strings.Contains(out, "installed") {
		t.Errorf("re-run should be all up-to-date:\n%s", out)
	}

	if err := os.WriteFile(module, []byte("locally hacked"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if rc := runInstallMods([]string{"--path", dst}); rc == 0 {
			t.Error("an edited file without --force must yield a non-zero exit")
		}
	})
	if !strings.Contains(out, "SKIPPED") || !strings.Contains(out, "--force") {
		t.Errorf("edited file must be skipped with a reason:\n%s", out)
	}
	if b, _ := os.ReadFile(module); string(b) != "locally hacked" {
		t.Error("skip must leave the edited file alone")
	}

	captureStdout(t, func() {
		if rc := runInstallMods([]string{"--path", dst, "--force"}); rc != 0 {
			t.Fatalf("--force rc=%d", rc)
		}
	})
	if b, _ := os.ReadFile(module); string(b) == "locally hacked" {
		t.Error("--force must overwrite the edited file")
	}
}

func TestInstallModsSkipsGeneratedTypes(t *testing.T) {
	fsys := fstest.MapFS{
		"mods/demo/.claude-plugin/plugin.json":                  {Data: []byte(`{"name":"demo"}`)},
		"mods/demo/.claude-plugin/types/claude-code/index.d.ts": {Data: []byte("// generated")},
		"mods/demo/hooks/register.ts":                           {Data: []byte("export const register = () => {}")},
		"mods/demo/hooks/demo.test.ts":                          {Data: []byte("test")},
	}
	dst := t.TempDir()
	keep := filepath.Join(dst, "demo", ".claude-plugin", "types", "claude-code", "index.d.ts")
	if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("// the engine's own"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := installMods(fsys, dst, true)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range results {
		names = append(names, r.name)
		if r.outcome != "installed" {
			t.Errorf("%s: %s %s", r.name, r.outcome, r.reason)
		}
	}
	if got := strings.Join(names, ","); got != "demo/.claude-plugin/plugin.json,demo/hooks/register.ts" {
		t.Errorf("installed set = %s", got)
	}
	if b, _ := os.ReadFile(keep); string(b) != "// the engine's own" {
		t.Error("the engine's typings must be left alone")
	}
}

func TestInstallModsRefusesSymlinkedModDir(t *testing.T) {
	fsys := fstest.MapFS{"mods/demo/hooks/register.ts": {Data: []byte("x")}}
	dst := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dst, "demo")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	results, err := installMods(fsys, dst, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].outcome != "failed" || !strings.Contains(results[0].reason, "symlinks") {
		t.Errorf("symlinked mod dir must fail: %+v", results)
	}
}
