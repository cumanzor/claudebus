package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexPermissionsPreviewNeverInstallsRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	program := filepath.Join(t.TempDir(), "cbus with spaces")
	if err := os.WriteFile(program, []byte("binary fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if code := runCodexPermissions([]string{"--binary", program}); code != 0 {
			t.Fatal(code)
		}
	})
	quoted, _ := json.Marshal(program)
	if !strings.Contains(out, "pattern = ["+string(quoted)+`, "send"]`) || !strings.Contains(out, `decision = "allow"`) {
		t.Fatalf("rule did not bind the exact executable/reply verb: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "rules")); !os.IsNotExist(err) {
		t.Fatal("preview wrote a permission file")
	}
}

func TestCodexPermissionsInstallIsExplicitAndProtectsEdits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	program := filepath.Join(t.TempDir(), "cbus")
	if err := os.WriteFile(program, []byte("binary fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"--binary", program, "--install"}
	runInstall := func(args []string, want int) {
		t.Helper()
		captureStdout(t, func() {
			if code := runCodexPermissions(args); code != want {
				t.Fatalf("exit=%d want=%d", code, want)
			}
		})
	}
	runInstall(args, 0)
	dst := filepath.Join(home, "rules", "cbus.rules")
	want := []byte("# user-managed rule\n")
	if err := os.WriteFile(dst, want, 0600); err != nil {
		t.Fatal(err)
	}
	runInstall(args, 1)
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != string(want) {
		t.Fatal("installer overwrote local permissions")
	}
	runInstall(append(args, "--force"), 0)
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("installer changed general Codex configuration")
	}
	for _, args := range [][]string{{"--path", dst}, {"--force"}, {"--install", "extra"}} {
		runInstall(args, 1)
	}
}

func TestCodexBusPermissionsRequireExplicitScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	program := filepath.Join(t.TempDir(), "cbus with spaces")
	if err := os.WriteFile(program, []byte("binary fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(want int, args ...string) {
		t.Helper()
		captureStdout(t, func() {
			if code := runCodexPermissions(append([]string{"--binary", program}, args...)); code != want {
				t.Fatalf("%v: exit=%d want=%d", args, code, want)
			}
		})
	}
	run(1, "--scope", "unknown", "--install")
	run(1, "--scope", "", "--install")
	run(0, "--scope", "bus")
	if _, err := os.Stat(filepath.Join(home, "rules")); !os.IsNotExist(err) {
		t.Fatal("preview or invalid scope installed permissions")
	}
	run(0, "--install")
	dst := filepath.Join(home, "rules", "cbus.rules")
	send, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	run(0, "--scope", "bus")
	stillSend, _ := os.ReadFile(dst)
	if string(stillSend) != string(send) {
		t.Fatal("preview broadened existing send permission")
	}
	run(0, "--scope", "bus", "--install")
	bus, _ := os.ReadFile(dst)
	if string(bus) == string(send) || !strings.Contains(string(bus), `pattern = ["cbus"]`) {
		t.Fatal("explicit bus opt-in did not upgrade the tracked send rule")
	}
}

func TestConnectArgsExplicitStorageBinding(t *testing.T) {
	pos, asJSON, opts, err := connectArgs([]string{"dev", "worker", "--codex-sqlite-home", "/state space", "--json"})
	if err != nil || !asJSON || len(pos) != 2 || opts.SQLiteHome != "/state space" {
		t.Fatalf("binding options lost: %v, %+v, %v", pos, opts, err)
	}
	for _, args := range [][]string{{"dev", "--codex-sqlite-home"}, {"dev", "--codex-sqlite-home", ""}, {"dev", "--codex-sqlite-home", "/one", "--codex-sqlite-home", "/two"}} {
		if _, _, _, err := connectArgs(args); err == nil {
			t.Fatalf("ambiguous storage selection accepted: %q", args)
		}
	}
}
