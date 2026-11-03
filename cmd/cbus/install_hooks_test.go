package main

import (
	"encoding/json"
	"os"
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
