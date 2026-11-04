package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const existingSettings = `{
  "zeta": 1.50,
  "model": "a&b<c>",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "python3 reminder.py && true"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "$HOME/.local/bin/cbus hook-exit"}]}]
  },
  "alpha": {"nested": [1, 2]}
}
`

func TestMergeClaudeHooksKeepsTheUsersFile(t *testing.T) {
	out, added, err := mergeClaudeHooks([]byte(existingSettings), "$HOME/.local/bin/cbus")
	if err != nil || len(added) != 2 {
		t.Fatalf("added=%v err=%v", added, err)
	}
	s := string(out)
	for _, want := range []string{`"zeta": 1.50`, `"a&b<c>"`, `python3 reminder.py && true`, `cbus hook-exit`} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %q:\n%s", want, s)
		}
	}
	if !(strings.Index(s, `"zeta"`) < strings.Index(s, `"model"`) && strings.Index(s, `"model"`) < strings.Index(s, `"hooks"`) && strings.Index(s, `"hooks"`) < strings.Index(s, `"alpha"`)) {
		t.Errorf("key order changed:\n%s", s)
	}
	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	stop := parsed.Hooks["Stop"]
	if len(stop) != 2 || stop[0].Hooks[0].Command != "python3 reminder.py && true" || stop[1].Hooks[0].Command != "$HOME/.local/bin/cbus hook-regressed" {
		t.Errorf("Stop = %+v", stop)
	}
	denied := parsed.Hooks["PermissionDenied"]
	if len(denied) != 1 || denied[0].Matcher != "*" || denied[0].Hooks[0].Type != "command" || denied[0].Hooks[0].Command != "$HOME/.local/bin/cbus hook-denied" {
		t.Errorf("PermissionDenied = %+v", denied)
	}

	again, added, err := mergeClaudeHooks(out, "$HOME/.local/bin/cbus")
	if err != nil || len(added) != 0 || again != nil {
		t.Errorf("second merge added %v, err %v", added, err)
	}
}

func TestMergeClaudeHooksSeesAHandWrittenEntry(t *testing.T) {
	raw := `{"hooks":{"PermissionDenied":[{"matcher":"Bash","hooks":[{"type":"command","command":"/opt/my tools/cbus hook-denied 2>/dev/null"}]}]}}`
	_, added, err := mergeClaudeHooks([]byte(raw), "cbus")
	if err != nil || len(added) != 1 || added[0].verb != "hook-regressed" {
		t.Errorf("added=%v err=%v", added, err)
	}
	notCbus := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo hook-regressed"}]}]}}`
	_, added, _ = mergeClaudeHooks([]byte(notCbus), "cbus")
	if len(added) != 2 {
		t.Errorf("an unrelated command counted as installed: %v", added)
	}
}

func TestMergeClaudeHooksRefusesWhatItCannotParse(t *testing.T) {
	for name, raw := range map[string]string{
		"comments":      "{\n  // mine\n  \"a\": 1\n}",
		"trailing":      `{"a":1} {"b":2}`,
		"not an object": `[1]`,
		"hooks shape":   `{"hooks":[]}`,
		"event shape":   `{"hooks":{"Stop":{}}}`,
	} {
		if out, _, err := mergeClaudeHooks([]byte(raw), "cbus"); err == nil {
			t.Errorf("%s: accepted:\n%s", name, out)
		}
	}
	if out, added, err := mergeClaudeHooks(nil, "cbus"); err != nil || len(added) != 2 || !json.Valid(out) {
		t.Errorf("empty file: %v %v", added, err)
	}
}

func TestRunInstallHooks(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.json")
	if err := os.WriteFile(shared, []byte(existingSettings), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(shared, link); err != nil {
		t.Skip(err)
	}

	if rc := runInstallHooks([]string{"--path", link, "--dry-run"}); rc != 0 {
		t.Fatalf("dry run rc=%d", rc)
	}
	if b, _ := os.ReadFile(shared); string(b) != existingSettings {
		t.Fatal("dry run changed the file")
	}
	if m, _ := filepath.Glob(shared + ".bak-*"); len(m) != 0 {
		t.Fatal("dry run wrote a backup")
	}

	if rc := runInstallHooks([]string{"--path", link}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced")
	}
	b, _ := os.ReadFile(shared)
	if !strings.Contains(string(b), " hook-denied") {
		t.Fatalf("target not updated:\n%s", b)
	}
	if fi, _ := os.Stat(shared); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 640", fi.Mode().Perm())
	}
	backups, _ := filepath.Glob(shared + ".bak-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if bak, _ := os.ReadFile(backups[0]); string(bak) != existingSettings {
		t.Error("backup is not the original")
	}

	before, _ := os.ReadFile(shared)
	if rc := runInstallHooks([]string{"--path", link}); rc != 0 {
		t.Fatalf("second run rc=%d", rc)
	}
	if after, _ := os.ReadFile(shared); string(after) != string(before) {
		t.Error("second run changed the file")
	}
	if b2, _ := filepath.Glob(shared + ".bak-*"); len(b2) != 1 {
		t.Errorf("second run wrote another backup: %v", b2)
	}

	bad := filepath.Join(dir, "bad.json")
	garbage := "{ not json"
	os.WriteFile(bad, []byte(garbage), 0o600)
	if rc := runInstallHooks([]string{"--path", bad}); rc == 0 {
		t.Error("garbage accepted")
	}
	if b, _ := os.ReadFile(bad); string(b) != garbage {
		t.Error("garbage file changed")
	}
}

func TestClaudeHooksHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if h := claudeHooksHint(); h != "" {
		t.Errorf("no settings file: %q", h)
	}
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(`{}`), 0o600)
	if h := claudeHooksHint(); !strings.Contains(h, "cbus install-hooks") {
		t.Errorf("missing hooks: %q", h)
	}
	if rc := runInstallHooks(nil); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if h := claudeHooksHint(); h != "" {
		t.Errorf("after install: %q", h)
	}
}

func TestUnknownVerbHint(t *testing.T) {
	if !strings.Contains(unknownVerbHint("install"), "install-opencode") {
		t.Error("install has no hint")
	}
	if unknownVerbHint("instal") != "" {
		t.Error("other verbs get the hint")
	}
}

func TestHookCommandPath(t *testing.T) {
	for _, tc := range []struct{ exe, home, want string }{
		{"/home/me/.local/bin/cbus", "/home/me", `"$HOME/.local/bin/cbus"`},
		{"/x/my home/.local/bin/cbus", "/x/my home", `"$HOME/.local/bin/cbus"`},
		{"/usr/local/bin/cbus", "/home/me", "/usr/local/bin/cbus"},
		{"/opt/my tools/cbus", "/home/me", "'/opt/my tools/cbus'"},
		{"/x/a&b/cbus", "/home/me", "'/x/a&b/cbus'"},
		{"/x/it's/cbus", "/home/me", `'/x/it'\''s/cbus'`},
		{"/home/me/a b/cbus", "/home/me", "'/home/me/a b/cbus'"},
		{"C:/tools/cbus.exe", "C:/Users/me", "C:/tools/cbus.exe"},
	} {
		if got := hookCommandPath(tc.exe, tc.home); got != tc.want {
			t.Errorf("hookCommandPath(%q, %q) = %s, want %s", tc.exe, tc.home, got, tc.want)
		}
	}
}

// what is written must run in a shell, in the awkward places too
func TestWrittenCommandRunsInAShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	for _, sub := range []string{"plain", "my home", "a&b", "it's (x)"} {
		home := filepath.Join(root, sub)
		bin := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(bin, "cbus")
		if err := os.WriteFile(exe, []byte("#!/bin/sh\necho ran:$1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, h := range []string{home, "/somewhere/else"} {
			cmd := exec.Command(bash, "-c", hookCommandPath(exe, h)+" hook-denied")
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "ran:hook-denied" {
				t.Errorf("%q from home %q: %v %s", sub, h, err, out)
			}
		}
	}
}

func TestDeadHookPathIsRepairedInPlace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	raw := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/gone/cbus hook-regressed","timeout":5}]}],"PermissionDenied":[{"matcher":"Bash","hooks":[{"type":"command","command":"$HOME/.local/bin/cbus hook-denied"}]}]}}`
	out, changed, err := mergeClaudeHooks([]byte(raw), "/new/cbus")
	if err != nil || len(changed) != 2 {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	s := string(out)
	if strings.Contains(s, "/gone/cbus") || strings.Contains(s, "$HOME/.local/bin/cbus") {
		t.Errorf("dead path kept:\n%s", s)
	}
	if strings.Count(s, "/new/cbus hook-regressed") != 1 || !strings.Contains(s, `"timeout": 5`) || !strings.Contains(s, `"matcher": "Bash"`) {
		t.Errorf("repair must keep the entry:\n%s", s)
	}
	if strings.Count(s, `"command": `) != 2 {
		t.Errorf("repair added an entry:\n%s", s)
	}
}

func TestLiveAndUnjudgedHookCommandsCountAsPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "cbus"), nil, 0o755)
	for _, cmd := range []string{
		"$HOME/.local/bin/cbus hook-denied",
		`"$HOME/.local/bin/cbus" hook-denied`,
		"cbus hook-denied",
		"/gone/cbus hook-denied 2>/dev/null",
		filepath.Join(bin, "cbus") + " hook-denied",
	} {
		if present, stale, _ := findHookCommand(groupsWith(cmd), "hook-denied", "/other/cbus"); !present || stale != nil {
			t.Errorf("%q: present=%v stale=%v", cmd, present, stale != nil)
		}
	}
}

func TestUnquotedHomeWithASpaceIsRepaired(t *testing.T) {
	home := filepath.Join(t.TempDir(), "my home")
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "cbus"), nil, 0o755)
	t.Setenv("HOME", home)
	present, stale, _ := findHookCommand(groupsWith("$HOME/.local/bin/cbus hook-denied"), "hook-denied", `"$HOME/.local/bin/cbus"`)
	if present || stale == nil {
		t.Errorf("present=%v stale=%v, want it repaired", present, stale != nil)
	}
}

func groupsWith(cmd string) []any {
	return []any{jsonObject{{"hooks", []any{jsonObject{{"type", "command"}, {"command", cmd}}}}}}
}

func TestInstallHooksWritesThroughADanglingLink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "settings.json")
	target := filepath.Join(dir, "dotfiles", "claude", "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if rc := runInstallHooks([]string{"--path", link}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if b, err := os.ReadFile(target); err != nil || !strings.Contains(string(b), "hook-denied") {
		t.Fatalf("target: %v %s", err, b)
	}
}

func TestInstallHooksRefusesEmptyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if rc := runInstallHooks([]string{"--path", ""}); rc == 0 {
		t.Error("an empty --path was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err == nil {
		t.Error("an empty --path wrote the default settings")
	}
}
