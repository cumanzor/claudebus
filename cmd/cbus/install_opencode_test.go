package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallOpencodeFreshThenUpToDate(t *testing.T) {
	dst := t.TempDir()
	out := captureStdout(t, func() {
		if rc := runInstallOpencode([]string{"--path", dst}); rc != 0 {
			t.Fatalf("fresh install rc=%d", rc)
		}
	})
	for _, f := range []string{"plugins/cbus-server.js", "cbus-tui.js", "commands/bus-join.md", "tui.jsonc"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("%s not installed: %v\n%s", f, err, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "tui.jsonc")); !strings.Contains(string(b), `"./cbus-tui.js"`) {
		t.Errorf("created tui.jsonc does not list the TUI plugin: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "plugins", "cbus-tui.js")); err == nil {
		t.Error("TUI plugin installed under plugins/, where OpenCode loads it as a server plugin and rejects it")
	}
	out = captureStdout(t, func() {
		if rc := runInstallOpencode([]string{"--path", dst}); rc != 0 {
			t.Fatalf("second install rc=%d", rc)
		}
	})
	if strings.Count(out, "up-to-date") != 4 {
		t.Errorf("second install should leave all four files alone:\n%s", out)
	}
}

func TestInstallOpencodeNeverRewritesAnExistingTUIConfig(t *testing.T) {
	dst := t.TempDir()
	cfg := "{\n  // the user's own comment\n  \"plugin\": [\"./herdr-tui-session.js\"]\n}\n"
	path := filepath.Join(dst, "tui.jsonc")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var rc int
	out := captureStdout(t, func() { rc = runInstallOpencode([]string{"--path", dst}) })
	if rc == 0 {
		t.Errorf("install reported success while the TUI plugin is not registered:\n%s", out)
	}
	if !strings.Contains(out, `add "./cbus-tui.js" to its "plugin" list`) {
		t.Errorf("missing the line to add:\n%s", out)
	}
	if b, _ := os.ReadFile(path); string(b) != cfg {
		t.Errorf("existing tui.jsonc was modified:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "plugins", "cbus-server.js")); err != nil {
		t.Errorf("server plugin should still install: %v", err)
	}
}

func TestInstallOpencodeAcceptsARegisteredTUIJSON(t *testing.T) {
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "tui.json"), []byte(`{"plugin": ["./other.js", "./cbus-tui.js"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := runInstallOpencode([]string{"--path", dst}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(dst, "tui.jsonc")); err == nil {
		t.Error("created tui.jsonc beside an existing tui.json")
	}
}

func TestInstallOpencodeKeepsLocalEditsUnlessForced(t *testing.T) {
	dst := t.TempDir()
	if rc := runInstallOpencode([]string{"--path", dst}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	edited := filepath.Join(dst, "cbus-tui.js")
	if err := os.WriteFile(edited, []byte("// local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rc int
	captureStdout(t, func() { rc = runInstallOpencode([]string{"--path", dst}) })
	if b, _ := os.ReadFile(edited); rc == 0 || string(b) != "// local edit\n" {
		t.Errorf("unforced install rc=%d overwrote or accepted a local edit", rc)
	}
	if rc := runInstallOpencode([]string{"--path", dst, "--force"}); rc != 0 {
		t.Fatalf("forced install rc=%d", rc)
	}
	if b, _ := os.ReadFile(edited); string(b) == "// local edit\n" {
		t.Error("--force did not restore the shipped plugin")
	}
}

func TestDefaultOpencodeConfigDirHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if got, _ := defaultOpencodeConfigDir(); got != filepath.Join("/x/config", "opencode") {
		t.Errorf("got %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/h")
	if got, _ := defaultOpencodeConfigDir(); got != filepath.Join("/h", ".config", "opencode") {
		t.Errorf("got %q", got)
	}
}

func TestRefreshOpencodeSkipsWithoutAnInstallAndUpdatesOne(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(cfg, "opencode")
	out := captureStdout(t, func() {
		if rc := refreshOpencodePlugins(nil); rc != 0 {
			t.Fatalf("refresh without an install rc=%d", rc)
		}
	})
	if _, err := os.Stat(dir); err == nil || !strings.Contains(out, "skipped") {
		t.Fatalf("refresh created %s or did not say it skipped:\n%s", dir, out)
	}
	if rc := runInstallOpencode([]string{"--path", dir}); rc != 0 {
		t.Fatalf("install rc=%d", rc)
	}
	stale := filepath.Join(dir, "cbus-tui.js")
	if err := os.WriteFile(stale, []byte("// older plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := refreshOpencodePlugins(nil); rc != 0 {
		t.Fatalf("refresh rc=%d", rc)
	}
	if b, _ := os.ReadFile(stale); string(b) == "// older plugin\n" {
		t.Error("refresh left an outdated plugin in place")
	}
}
