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
	addr, err := Spawn("window", "dev", "", "", f)
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
	if _, err := Spawn("tab", "dev", "", "", f); err != nil {
		t.Fatal(err)
	}
	if f.spec.Argv[0] != "claude" {
		t.Fatalf("argv = %v", f.spec.Argv)
	}
}

func TestSpawnRemoteAddress(t *testing.T) {
	f := &fakeForker{}
	addr, err := Spawn("tab", "dev@nuc", "", "", f)
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
		if _, err := Spawn(tc.target, tc.addr, "", "", f); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
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
	addr, err := Spawn("window", "", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "global" {
		t.Fatalf("default addr = %q, want global", addr)
	}
}

func TestSpawnModelFlag(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	f := &fakeForker{}
	if _, err := Spawn("window", "dev", "sonnet", "", f); err != nil {
		t.Fatal(err)
	}
	argv := f.spec.Argv
	i := slices.Index(argv, "--model")
	if i < 0 || argv[i+1] != "sonnet" {
		t.Fatalf("argv = %v", argv)
	}
	if argv[len(argv)-1] != SpawnPrompt("dev") {
		t.Fatalf("prompt must stay the final positional: %v", argv)
	}
}

func TestBranchModelFlagAndBadModel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-model-test")
	f := &fakeForker{}
	if _, _, err := Branch("tab", "modelchan", "opus", "", f); err != nil {
		t.Fatal(err)
	}
	argv := f.spec.Argv
	i := slices.Index(argv, "--model")
	if i < 0 || argv[i+1] != "opus" {
		t.Fatalf("argv = %v", argv)
	}
	if slices.Index(argv, "--fork-session") > i {
		t.Fatalf("--model must follow --fork-session: %v", argv)
	}
	if _, _, err := Branch("tab", "modelchan", "bad model", "", f); err == nil || !strings.Contains(err.Error(), `bad model "bad model"`) {
		t.Fatalf("bad model err = %v", err)
	}
	if _, err := Spawn("tab", "dev", "-x", "", f); err == nil || !strings.Contains(err.Error(), `bad model "-x"`) {
		t.Fatalf("spawn bad model err = %v", err)
	}
}

func TestSpawnNameFlagAndDefault(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := &fakeForker{}
	if _, err := Spawn("window", "dev", "", "runner 2", f); err != nil {
		t.Fatal(err)
	}
	argv := f.spec.Argv
	i := slices.Index(argv, "--name")
	if i < 0 || argv[i+1] != "runner 2" {
		t.Fatalf("argv = %v", argv)
	}
	if argv[len(argv)-1] != SpawnPrompt("dev") {
		t.Fatalf("prompt must stay the final positional: %v", argv)
	}
	f = &fakeForker{}
	if _, err := Spawn("window", "dev@nuc", "", "", f); err != nil {
		t.Fatal(err)
	}
	argv = f.spec.Argv
	if i = slices.Index(argv, "--name"); i < 0 || argv[i+1] != "dev@nuc" {
		t.Fatalf("default name should be the address: %v", argv)
	}
	if _, err := Spawn("window", "dev", "", "-x", f); err == nil || !strings.Contains(err.Error(), `bad name "-x"`) {
		t.Fatalf("bad name err = %v", err)
	}
}

func TestBranchNameFlagAndDefault(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-name-test")
	f := &fakeForker{}
	if _, _, err := Branch("tab", "namechan", "", "custom title", f); err != nil {
		t.Fatal(err)
	}
	argv := f.spec.Argv
	i := slices.Index(argv, "--name")
	if i < 0 || argv[i+1] != "custom title" {
		t.Fatalf("argv = %v", argv)
	}
	f = &fakeForker{}
	if _, _, err := Branch("tab", "namechan", "", "", f); err != nil {
		t.Fatal(err)
	}
	argv = f.spec.Argv
	if i = slices.Index(argv, "--name"); i < 0 || argv[i+1] != "namechan" {
		t.Fatalf("default name should be the channel: %v", argv)
	}
	if _, _, err := Branch("tab", "namechan", "", "-x", f); err == nil || !strings.Contains(err.Error(), `bad name "-x"`) {
		t.Fatalf("bad name err = %v", err)
	}
}
