package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---- hook-exit -------------------------------------------------------------------

// TestHookExitLeavesLocalKeepsRemote: the SessionEnd hook leaves this session's LOCAL
// registrations (reading the id from stdin JSON) but leaves REMOTE markers untouched —
// the relay has no leave endpoint; a dead session's markers die via the ownerPid sweep.
func TestHookExitLeavesLocalKeepsRemote(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	if _, _, err := Join("chA", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Join("chB", ""); err != nil {
		t.Fatal(err)
	}
	if err := WriteRemoteMarker("nuc", "chR", "mbp"); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".remote", "nuc", "chR", "S1")
	if !fileExists(marker) {
		t.Fatal("precondition: remote marker not written")
	}

	HookExit(strings.NewReader(`{"session_id":"S1","cwd":"/x"}`))

	if dirExists(filepath.Join(root, "chA")) || dirExists(filepath.Join(root, "chB")) {
		t.Error("hook-exit must leave local registrations")
	}
	if !fileExists(marker) {
		t.Error("hook-exit must NOT delete remote markers (relay has no leave endpoint)")
	}
}

// TestHookExitEnvFallback: a non-JSON stdin falls back to CLAUDE_CODE_SESSION_ID.
func TestHookExitEnvFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S2")
	if _, _, err := Join("chA", ""); err != nil {
		t.Fatal(err)
	}
	HookExit(strings.NewReader("not json at all"))
	if dirExists(filepath.Join(root, "chA")) {
		t.Error("hook-exit env fallback must leave chA")
	}
}

// TestHookExitNoSessionNoop: no stdin id and no env id => nothing happens (and it
// must not touch an unrelated peer).
func TestHookExitNoSessionNoop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	seedMeta(t, root, "chA", "main", "other") // a peer owned by a DIFFERENT session
	HookExit(strings.NewReader("{}"))
	if !dirExists(filepath.Join(root, "chA")) {
		t.Error("hook-exit with no session id must be a no-op")
	}
}

// TestHookExitStdinBeatsEnv: the stdin session_id wins over the environment.
func TestHookExitStdinBeatsEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "ENVID")
	// registration belongs to STDINID, not ENVID.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "STDINID")
	if _, _, err := Join("chA", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "ENVID") // env now points elsewhere
	HookExit(strings.NewReader(`{"session_id":"STDINID"}`))
	if dirExists(filepath.Join(root, "chA")) {
		t.Error("hook-exit must leave the STDIN session's registration, not the env one")
	}
	// env must be restored to ENVID afterwards.
	if os.Getenv("CLAUDE_CODE_SESSION_ID") != "ENVID" {
		t.Errorf("hook-exit leaked env: %q", os.Getenv("CLAUDE_CODE_SESSION_ID"))
	}
}

// ---- bootstrap -------------------------------------------------------------------

// TestBootstrapPromptSubstitution: $ch (4x) and $parent (1x) expand correctly and the
// body carries no leftover placeholders or trailing newline.
func TestBootstrapPromptSubstitution(t *testing.T) {
	got := BootstrapPrompt("myrepo", "lead")
	if strings.Contains(got, "$ch") || strings.Contains(got, "$parent") {
		t.Fatalf("unsubstituted placeholder remains: %q", got)
	}
	if strings.Count(got, "myrepo") != 4 {
		t.Errorf("expected 4 channel substitutions, got %d", strings.Count(got, "myrepo"))
	}
	if !strings.Contains(got, "'myrepo/lead'") {
		t.Errorf("parent substitution missing: %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Error("BootstrapPrompt must not carry a trailing newline (the caller adds it)")
	}
}

// ---- branch: env replication via a fake forker -----------------------------------

type fakeForker struct {
	spec   ForkSpec
	called bool
}

func (f *fakeForker) Fork(s ForkSpec) error { f.spec = s; f.called = true; return nil }

// TestBranchReplicatesEnvCCS: under a CCS instance config dir, Branch joins and forks
// with `ccs <profile> --resume <sid> --fork-session <prompt>`, replicating PATH +
// CLAUDE_CONFIG_DIR + cwd — the essential function, asserted without a real terminal.
func TestBranchReplicatesEnvCCS(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID123")
	t.Setenv("PATH", "/custom/bin:/usr/bin")
	t.Setenv("CLAUDE_CONFIG_DIR", "/home/u/.ccs/instances/personal")

	f := &fakeForker{}
	ch, alias, err := Branch("tab", "mychan", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if ch != "mychan" || alias == "" {
		t.Fatalf("branch resolved ch=%q alias=%q", ch, alias)
	}
	if !f.called {
		t.Fatal("forker was not invoked")
	}
	if f.spec.Target != "tab" {
		t.Errorf("target = %q", f.spec.Target)
	}
	if f.spec.Env["PATH"] != "/custom/bin:/usr/bin" {
		t.Errorf("PATH not replicated: %q", f.spec.Env["PATH"])
	}
	if f.spec.Env["CLAUDE_CONFIG_DIR"] != "/home/u/.ccs/instances/personal" {
		t.Errorf("CLAUDE_CONFIG_DIR not replicated: %q", f.spec.Env["CLAUDE_CONFIG_DIR"])
	}
	if f.spec.Dir == "" {
		t.Error("cwd not replicated")
	}
	want := []string{"ccs", "personal", "--resume", "SID123", "--fork-session"}
	for i, w := range want {
		if i >= len(f.spec.Argv) || f.spec.Argv[i] != w {
			t.Fatalf("argv = %v, want prefix %v", f.spec.Argv, want)
		}
	}
	last := f.spec.Argv[len(f.spec.Argv)-1]
	if !strings.Contains(last, "cbus join mychan") {
		t.Errorf("last argv should be the bootstrap prompt: %q", last)
	}
}

// TestBranchNonCCSUsesClaude: without a CCS config dir, the launch is a bare `claude`.
func TestBranchNonCCSUsesClaude(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	f := &fakeForker{}
	if _, _, err := Branch("window", "ch", "", f); err != nil {
		t.Fatal(err)
	}
	if f.spec.Argv[0] != "claude" {
		t.Errorf("argv[0] = %q, want claude", f.spec.Argv[0])
	}
	if _, ok := f.spec.Env["CLAUDE_CONFIG_DIR"]; ok {
		t.Error("CLAUDE_CONFIG_DIR must be absent when unset")
	}
}

// TestBranchBadTarget: an invalid target is rejected before any join/fork.
func TestBranchBadTarget(t *testing.T) {
	f := &fakeForker{}
	if _, _, err := Branch("popup", "ch", "", f); err == nil {
		t.Fatal("expected target validation error")
	}
	if f.called {
		t.Error("forker must not run on a bad target")
	}
}

// TestForkShellCommandQuoting: the temp-file-free command string cd's, sets env, and
// execs the argv — everything POSIX-quoted (env keys sorted for determinism).
func TestForkShellCommandQuoting(t *testing.T) {
	spec := ForkSpec{
		Target: "window",
		Argv:   []string{"ccs", "personal", "--resume", "S", "--fork-session", "hi 'there'"},
		Env:    map[string]string{"PATH": "/a b", "CLAUDE_CONFIG_DIR": "/c"},
		Dir:    "/work dir",
	}
	got := forkShellCommand(spec)
	want := `cd '/work dir' && exec env CLAUDE_CONFIG_DIR='/c' PATH='/a b' 'ccs' 'personal' '--resume' 'S' '--fork-session' 'hi '\''there'\'''`
	if got != want {
		t.Fatalf("forkShellCommand:\n got  %s\n want %s", got, want)
	}
}

func TestAppleScriptEscaping(t *testing.T) {
	if got := appleScriptStr(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("appleScriptStr = %s", got)
	}
}

// TestLauncherScriptByteExact pins the iTerm2 launcher script (F1 fix): the self-
// deleting script iTerm2 runs, byte-for-byte, with an injected tmpfile path. This is
// the layer that DOES use POSIX quoting (iTerm2's own tokenizer never sees it).
func TestLauncherScriptByteExact(t *testing.T) {
	spec := ForkSpec{
		Target: "window",
		Argv:   []string{"ccs", "personal", "--resume", "S", "--fork-session", "hi 'there'"},
		Env:    map[string]string{"PATH": "/a b", "CLAUDE_CONFIG_DIR": "/c"},
		Dir:    "/work dir",
	}
	got := launcherScript(spec, "/tmp/fixed.sh")
	want := "#!/bin/bash\n" +
		"export CLAUDE_CONFIG_DIR='/c'\n" +
		"export PATH='/a b'\n" +
		"cd '/work dir'\n" +
		"rm -f '/tmp/fixed.sh'\n" +
		`exec 'ccs' 'personal' '--resume' 'S' '--fork-session' 'hi '\''there'\'''` + "\n"
	if got != want {
		t.Fatalf("launcherScript:\n got  %q\n want %q", got, want)
	}
}

// TestITerm2CommandBare: the command handed to iTerm2 is a BARE `/bin/bash <tmpfile>`
// with no quoting — iTerm2 tokenizes it itself (a quoted one-liner would launch
// nothing; the launcher-script indirection is why).
func TestITerm2CommandBare(t *testing.T) {
	if got := iterm2Command("/tmp/cc-branch.123.sh"); got != "/bin/bash /tmp/cc-branch.123.sh" {
		t.Fatalf("iterm2Command = %q, want a bare, unquoted two-token command", got)
	}
}

// TestLauncherScriptExecutes runs the generated script via `/bin/bash <tmpfile>` (the
// exact invocation iTerm2 makes) and proves the mechanism end-to-end WITHOUT iTerm2:
// PATH + CLAUDE_CONFIG_DIR + cwd are replicated, the exec runs, and the script deletes
// itself. (The iTerm2 tokenizer leg is the reviewer's live probe harness.)
func TestLauncherScriptExecutes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "probe.out")
	spec := ForkSpec{
		Target: "window",
		Argv:   []string{"/bin/sh", "-c", `printf 'cwd=%s\npath=%s\ncfg=%s\n' "$PWD" "$PATH" "$CLAUDE_CONFIG_DIR" > ` + out},
		// PATH keeps the real /bin:/usr/bin (the launcher's own `rm` resolves through
		// it, just like cc-branch.sh) plus a probe marker to prove replication.
		Env: map[string]string{"PATH": "/probe/bin:/bin:/usr/bin", "CLAUDE_CONFIG_DIR": "/probe/cfg"},
		Dir: dir,
	}
	script := filepath.Join(dir, "launch.sh")
	if err := os.WriteFile(script, []byte(launcherScript(spec, script)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/bin/bash", script).Run(); err != nil {
		t.Fatalf("launcher run: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cwd=" + dir, "path=/probe/bin:/bin:/usr/bin", "cfg=/probe/cfg"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("launcher did not replicate %q; probe output: %q", want, got)
		}
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("launcher must self-delete before exec; stat err = %v", err)
	}
}
