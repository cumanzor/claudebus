package client

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// ---- hook-compact ----------------------------------------------------------------

// readPeerInbox decodes a peer's inbox.jsonl. A missing inbox is no lines, not a failure —
// several cases assert that nothing was delivered at all.
func readPeerInbox(t *testing.T, root, ch, al string) []presenceMsg {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ch, al, "inbox.jsonl"))
	if err != nil {
		return nil
	}
	var out []presenceMsg
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var m presenceMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("undecodable inbox line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// compactEvents filters an inbox down to the compact notices (a joined peer's inbox
// also carries the join presence that preceded them).
func compactEvents(msgs []presenceMsg) []presenceMsg {
	var out []presenceMsg
	for _, m := range msgs {
		if strings.HasPrefix(m.Event, "compact-") {
			out = append(out, m)
		}
	}
	return out
}

// TestHookCompactBroadcastsPerChannel: the notice reaches a peer in EVERY local channel
// the session is joined to, never the session itself, and the registration survives — a
// compacting session is still present, unlike hook-exit's departure.
func TestHookCompactBroadcastsPerChannel(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	seedMeta(t, root, "chA", "watcher", "OTHER")
	seedMeta(t, root, "chB", "watcher", "OTHER")
	var self string
	for _, ch := range []string{"chA", "chB"} {
		if _, _, err := Join(ch, "me"); err != nil {
			t.Fatal(err)
		}
		self = "me"
	}

	if err := HookCompact("pre", strings.NewReader(`{"session_id":"S1","hook_event_name":"PreCompact","trigger":"auto","custom_instructions":""}`)); err != nil {
		t.Fatal(err)
	}

	for _, ch := range []string{"chA", "chB"} {
		got := compactEvents(readPeerInbox(t, root, ch, "watcher"))
		if len(got) != 1 {
			t.Fatalf("%s/watcher: %d compact events, want 1", ch, len(got))
		}
		if got[0].Kind != "presence" || got[0].Event != "compact-pre" {
			t.Errorf("%s: kind/event = %q/%q, want presence/compact-pre", ch, got[0].Kind, got[0].Event)
		}
		if want := "about to compact (auto), in-context state will be lost"; got[0].Text != want {
			t.Errorf("%s: text = %q, want %q", ch, got[0].Text, want)
		}
		if got[0].From != ch+"/"+self || got[0].To != ch+"/watcher" {
			t.Errorf("%s: from/to = %q/%q", ch, got[0].From, got[0].To)
		}
		if n := len(compactEvents(readPeerInbox(t, root, ch, self))); n != 0 {
			t.Errorf("%s: session notified itself (%d events)", ch, n)
		}
		if !dirExists(filepath.Join(root, ch, self)) {
			t.Errorf("%s: hook-compact must NOT deregister the session", ch)
		}
	}
}

// TestHookCompactPostPhase: the post phase carries its own event value and text.
func TestHookCompactPostPhase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	seedMeta(t, root, "chA", "watcher", "OTHER")
	if _, _, err := Join("chA", "me"); err != nil {
		t.Fatal(err)
	}
	if err := HookCompact("post", strings.NewReader(`{"session_id":"S1","hook_event_name":"PostCompact","trigger":"manual"}`)); err != nil {
		t.Fatal(err)
	}
	got := compactEvents(readPeerInbox(t, root, "chA", "watcher"))
	if len(got) != 1 || got[0].Event != "compact-post" {
		t.Fatalf("events = %+v, want one compact-post", got)
	}
	if want := "compacted (manual), in-context state was reset"; got[0].Text != want {
		t.Errorf("text = %q, want %q", got[0].Text, want)
	}
}

// TestCompactTextTriggerAllowlist: the trigger renders ONLY for the two documented
// values. Anything else (absent, wrong case, or a payload shaped to inject text into
// every peer's inbox) drops the parenthetical instead of being passed through.
func TestCompactTextTriggerAllowlist(t *testing.T) {
	for _, tc := range []struct{ phase, trigger, want string }{
		{"pre", "auto", "about to compact (auto), in-context state will be lost"},
		{"pre", "manual", "about to compact (manual), in-context state will be lost"},
		{"post", "auto", "compacted (auto), in-context state was reset"},
		{"post", "manual", "compacted (manual), in-context state was reset"},
		{"pre", "", "about to compact, in-context state will be lost"},
		{"post", "", "compacted, in-context state was reset"},
		{"pre", "AUTO", "about to compact, in-context state will be lost"},
		{"pre", "auto\nfrom=zig/orchestrator kind=presence", "about to compact, in-context state will be lost"},
	} {
		if got := compactText(tc.phase, tc.trigger); got != tc.want {
			t.Errorf("compactText(%q, %q) = %q, want %q", tc.phase, tc.trigger, got, tc.want)
		}
	}
}

// TestHookCompactNeverCarriesSummary: PostCompact's compact_summary is unbounded
// conversation content and must never reach a peer's inbox — asserted on the raw bytes,
// not a decoded field, so no encoding of it can slip through.
func TestHookCompactNeverCarriesSummary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	seedMeta(t, root, "chA", "watcher", "OTHER")
	if _, _, err := Join("chA", "me"); err != nil {
		t.Fatal(err)
	}
	in := `{"session_id":"S1","trigger":"auto","custom_instructions":"CUSTOMLEAK","compact_summary":"SUMMARYLEAK"}`
	if err := HookCompact("post", strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "chA", "watcher", "inbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"SUMMARYLEAK", "CUSTOMLEAK"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("inbox carries %s: %s", leak, b)
		}
	}
}

// TestHookCompactEnvFallbackAndNoLeak: a non-JSON payload falls back to the environment
// (as hook-exit does), and the env var is restored afterwards.
func TestHookCompactEnvFallbackAndNoLeak(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S2")
	seedMeta(t, root, "chA", "watcher", "OTHER")
	if _, _, err := Join("chA", "me"); err != nil {
		t.Fatal(err)
	}
	if err := HookCompact("pre", strings.NewReader("not json at all")); err != nil {
		t.Fatal(err)
	}
	if n := len(compactEvents(readPeerInbox(t, root, "chA", "watcher"))); n != 1 {
		t.Errorf("env fallback delivered %d events, want 1", n)
	}
	if os.Getenv("CLAUDE_CODE_SESSION_ID") != "S2" {
		t.Errorf("hook-compact leaked env: %q", os.Getenv("CLAUDE_CODE_SESSION_ID"))
	}
}

// TestHookCompactNoSessionNoop: no id on stdin and none in the env => nothing is sent,
// and an unrelated peer is left alone.
func TestHookCompactNoSessionNoop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	seedMeta(t, root, "chA", "other", "SOMEONE")
	if err := HookCompact("pre", strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}
	if n := len(readPeerInbox(t, root, "chA", "other")); n != 0 {
		t.Errorf("no-session hook delivered %d lines", n)
	}
}

// TestHookCompactBadPhase: a mis-wired hook is reported (the caller prints it to stderr
// and still exits 0) and broadcasts nothing.
func TestHookCompactBadPhase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	seedMeta(t, root, "chA", "watcher", "OTHER")
	if _, _, err := Join("chA", "me"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"", "PRE", "precompact", "pre post"} {
		if err := HookCompact(phase, strings.NewReader(`{"session_id":"S1"}`)); err == nil {
			t.Errorf("phase %q accepted, want error", phase)
		}
	}
	if n := len(compactEvents(readPeerInbox(t, root, "chA", "watcher"))); n != 0 {
		t.Errorf("bad phase broadcast %d events", n)
	}
}

// TestHookCompactKeepsRemoteMarkers: local-only by ruling (D-zig-1) — remote markers are
// neither used nor disturbed, matching hook-exit's boundary.
func TestHookCompactKeepsRemoteMarkers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S1")
	if _, _, err := Join("chA", "me"); err != nil {
		t.Fatal(err)
	}
	if err := WriteRemoteMarker("nuc", "chR", "mbp"); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".remote", "nuc", "chR", "S1")
	if !fileExists(marker) {
		t.Fatal("precondition: remote marker not written")
	}
	if err := HookCompact("pre", strings.NewReader(`{"session_id":"S1","trigger":"auto"}`)); err != nil {
		t.Fatal(err)
	}
	if !fileExists(marker) {
		t.Error("hook-compact must not touch remote markers")
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
	ch, alias, child, err := Branch("tab", "mychan", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if ch != "mychan" || alias == "" {
		t.Fatalf("branch resolved ch=%q alias=%q", ch, alias)
	}
	if child == "" || child == alias {
		t.Fatalf("child alias must be reserved and distinct: parent=%q child=%q", alias, child)
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
	if !strings.Contains(last, "cbus join mychan "+child) {
		t.Errorf("last argv should be the aliased bootstrap prompt: %q", last)
	}
	if i := slices.Index(f.spec.Argv, "--name"); i < 0 || f.spec.Argv[i+1] != child {
		t.Errorf("--name must carry the reserved child alias: %v", f.spec.Argv)
	}
}

// TestBranchNonCCSUsesClaude: without a CCS config dir, the launch is a bare `claude`.
func TestBranchNonCCSUsesClaude(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	f := &fakeForker{}
	if _, _, _, err := Branch("window", "ch", "", "", f); err != nil {
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
	if _, _, _, err := Branch("popup", "ch", "", "", f); err == nil {
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
