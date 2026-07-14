package client

import (
	"slices"
	"strings"
	"testing"
)

func TestSpawnFreshArgvCCSProfile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	t.Setenv("PATH", "/usr/bin:/bin")
	f := &fakeForker{}
	addr, err := Spawn("window", "dev", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "dev" {
		t.Fatalf("addr = %q", addr)
	}
	if !f.called {
		t.Fatal("forker not called")
	}
	if f.spec.Argv[0] != "ccs" || f.spec.Argv[1] != "personal" {
		t.Fatalf("argv = %v", f.spec.Argv)
	}
	if slices.Contains(f.spec.Argv, "--resume") || slices.Contains(f.spec.Argv, "--fork-session") {
		t.Fatalf("fresh spawn must not resume/fork: %v", f.spec.Argv)
	}
	if got := f.spec.Argv[len(f.spec.Argv)-1]; got != SpawnPrompt("dev") {
		t.Fatalf("prompt positional = %q", got)
	}
	if f.spec.Env["PATH"] != "/usr/bin:/bin" || f.spec.Env["CLAUDE_CONFIG_DIR"] != "/Users/x/.ccs/instances/personal" {
		t.Fatalf("env replication = %v", f.spec.Env)
	}
	if f.spec.Dir == "" {
		t.Fatal("dir not replicated")
	}
}

func TestSpawnFreshArgvBareClaude(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := &fakeForker{}
	if _, err := Spawn("tab", "dev", f); err != nil {
		t.Fatal(err)
	}
	if f.spec.Argv[0] != "claude" {
		t.Fatalf("argv = %v", f.spec.Argv)
	}
}

func TestSpawnRemoteAddress(t *testing.T) {
	f := &fakeForker{}
	addr, err := Spawn("tab", "dev@nuc", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "dev@nuc" {
		t.Fatalf("addr = %q", addr)
	}
	prompt := f.spec.Argv[len(f.spec.Argv)-1]
	for _, want := range []string{"ws arm spec", "dev@nuc", "1006", "cbus list @nuc"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("remote prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSpawnPromptLocalContent(t *testing.T) {
	p := SpawnPrompt("dev")
	for _, want := range []string{"cbus join dev", "NEVER Bash", "cbus list dev"} {
		if !strings.Contains(p, want) {
			t.Fatalf("local prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "$addr") || strings.Contains(p, "$host") {
		t.Fatalf("unexpanded placeholder:\n%s", p)
	}
}

func TestSpawnRejectsAliasAndBadNames(t *testing.T) {
	f := &fakeForker{}
	for _, tc := range []struct{ target, addr, wantErr string }{
		{"window", "dev/main", "no alias"},
		{"window", "dev@nuc/mbp", "no alias"},
		{"pane", "dev", "target must be"},
		{"window", "a b", `bad channel "a b"`},
		{"window", "dev@", `bad host ""`},
		{"window", "@nuc", `bad channel ""`},
	} {
		if _, err := Spawn(tc.target, tc.addr, f); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("Spawn(%q,%q) err = %v, want %q", tc.target, tc.addr, err, tc.wantErr)
		}
	}
	if f.called {
		t.Fatal("forker must not be called on validation errors")
	}
}

func TestSpawnDefaultDerivesGlobalOutsideGit(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Chdir(t.TempDir())
	f := &fakeForker{}
	addr, err := Spawn("window", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "global" {
		t.Fatalf("default addr = %q, want global", addr)
	}
}
