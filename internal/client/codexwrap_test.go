package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAllocCodexSocketUnderCBUSDir: a normal CBUS_DIR yields a socket under $CBUS_DIR/.sock
// (dot-prefixed so channel walkers skip it), SUN_LEN-safe.
func TestAllocCodexSocketUnderCBUSDir(t *testing.T) {
	// a short CBUS_DIR (t.TempDir on darwin is itself too long for SUN_LEN, which would force
	// the fallback this case is meant to avoid).
	root := fmt.Sprintf("/tmp/cbxwrap-%d", os.Getpid())
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("CBUS_DIR", root)
	sock, err := allocCodexSocket("abc123")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, ".sock")
	if filepath.Dir(sock) != wantDir {
		t.Errorf("sock dir = %q, want %q", filepath.Dir(sock), wantDir)
	}
	if !strings.HasSuffix(sock, "abc123.sock") {
		t.Errorf("sock = %q", sock)
	}
	if len(sock) > sunPathMax {
		t.Errorf("sock len %d exceeds SUN_LEN bound %d", len(sock), sunPathMax)
	}
	if _, err := os.Stat(wantDir); err != nil {
		t.Errorf(".sock dir not created: %v", err)
	}
}

// TestAllocCodexSocketFallsBackToTempDir: a CBUS_DIR too deep for SUN_LEN falls back to
// os.TempDir().
func TestAllocCodexSocketFallsBackToTempDir(t *testing.T) {
	deep := filepath.Join(t.TempDir(), strings.Repeat("d/", 60))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CBUS_DIR", deep)
	sock, err := allocCodexSocket("n1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(sock, deep) {
		t.Errorf("sock %q should have fallen back off the too-deep CBUS_DIR", sock)
	}
	if len(sock) > sunPathMax {
		t.Errorf("fallback sock len %d exceeds bound %d", len(sock), sunPathMax)
	}
}

// TestAllocCodexSocketUnique: two allocations with different nonces never collide.
func TestAllocCodexSocketUnique(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	a, _ := allocCodexSocket("aaaa")
	b, _ := allocCodexSocket("bbbb")
	if a == b {
		t.Errorf("nonce did not make the socket unique: %q == %q", a, b)
	}
}

// TestCodexRemoteArgs pins the codex --remote launch: attach + passthrough, and CRUCIALLY no
// hook config and no trust bypass (F1: hooks do not fire in this topology; the wrapper
// discovers the thread instead, and dropping the bypass is a real security/UX win).
func TestCodexRemoteArgs(t *testing.T) {
	args := codexRemoteArgs("/tmp/x.sock", []string{"--model", "gpt-5.5"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--remote unix:///tmp/x.sock") {
		t.Errorf("missing --remote attach: %s", joined)
	}
	for _, forbidden := range []string{"--dangerously-bypass-hook-trust", "hooks.SessionStart", "hook-join", "--skip-git-repo-check"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("TUI launch must NOT carry %q (F1 eliminated hooks + trust bypass from the TUI): %s", forbidden, joined)
		}
	}
	if args[len(args)-2] != "--model" || args[len(args)-1] != "gpt-5.5" {
		t.Errorf("passthrough must trail: %v", args)
	}
}

// TestCodexRemoteEnvScrubsLauncherIds pins the identity fix: the codex TUI env must have the
// whole SessionID() chain scrubbed (so codex's cbus commands cannot inherit and speak as the
// launcher) and CBUS_ALIAS/CBUS_CHANNEL set (so they self-identify as the peer). Unrelated env
// survives.
func TestCodexRemoteEnvScrubsLauncherIds(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "LAUNCHER")
	t.Setenv("CBUS_SESSION_ID", "LAUNCHER2")
	t.Setenv("GROK_SESSION_ID", "LAUNCHER3")
	t.Setenv("CBUS_ALIAS", "stale-inherited") // a stale inherited value must be replaced, not kept
	t.Setenv("CBXWRAP_KEEP", "survivor")

	m := map[string]string{}
	for _, kv := range codexRemoteEnv("cxch", "cxpeer") {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	for _, leaked := range []string{"CLAUDE_CODE_SESSION_ID", "CBUS_SESSION_ID", "GROK_SESSION_ID"} {
		if _, present := m[leaked]; present {
			t.Errorf("%s leaked into the codex TUI env (must be scrubbed)", leaked)
		}
	}
	if m["CBUS_ALIAS"] != "cxpeer" {
		t.Errorf("CBUS_ALIAS = %q, want cxpeer (self-identify as the peer)", m["CBUS_ALIAS"])
	}
	if m["CBUS_CHANNEL"] != "cxch" {
		t.Errorf("CBUS_CHANNEL = %q, want cxch", m["CBUS_CHANNEL"])
	}
	if m["CBXWRAP_KEEP"] != "survivor" {
		t.Errorf("unrelated env var not preserved: CBXWRAP_KEEP=%q", m["CBXWRAP_KEEP"])
	}
}

// TestCodexCommandsScrubBothProcesses pins the identity fix at BOTH launch sites: codex's tool
// shells execute in the APP-SERVER process tree (not the TUI), so the app-server env scrub is
// the load-bearing one and the TUI scrub is defense in depth. Both must carry the scrubbed env.
func TestCodexCommandsScrubBothProcesses(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "LAUNCHER")
	t.Setenv("CBUS_SESSION_ID", "LAUNCHER2")
	t.Setenv("GROK_SESSION_ID", "LAUNCHER3")

	srv, tui := codexCommands("cxch", "cxpeer", "/tmp/x.sock", nil)
	for name, cmd := range map[string]*exec.Cmd{"app-server": srv, "tui": tui} {
		if cmd.Env == nil {
			t.Fatalf("%s env not set — its tool shells would inherit the launcher session-id", name)
		}
		m := map[string]string{}
		for _, kv := range cmd.Env {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		for _, leaked := range []string{"CLAUDE_CODE_SESSION_ID", "CBUS_SESSION_ID", "GROK_SESSION_ID"} {
			if _, present := m[leaked]; present {
				t.Errorf("%s: %s leaked (both processes must scrub — tool shells run in the app-server tree)", name, leaked)
			}
		}
		if m["CBUS_ALIAS"] != "cxpeer" {
			t.Errorf("%s: CBUS_ALIAS = %q, want cxpeer", name, m["CBUS_ALIAS"])
		}
		if m["CBUS_CHANNEL"] != "cxch" {
			t.Errorf("%s: CBUS_CHANNEL = %q, want cxch", name, m["CBUS_CHANNEL"])
		}
	}
}

// TestThreadStartedInfo: id + cwd are pulled out of a thread/started payload.
func TestThreadStartedInfo(t *testing.T) {
	id, cwd := threadStartedInfo([]byte(`{"thread":{"id":"T1","cwd":"/work","name":null}}`))
	if id != "T1" || cwd != "/work" {
		t.Errorf("threadStartedInfo = %q,%q want T1,/work", id, cwd)
	}
}

// TestDiscoverThreadReturnsID: the first thread/started on the passive connection yields the
// thread id (cwd matches).
func TestDiscoverThreadReturnsID(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		if req["method"] == "initialize" {
			s.notify("thread/started", map[string]any{"thread": map[string]any{"id": "TUITHREAD", "cwd": "/work"}})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	got, err := discoverThread(c, "/work", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != "TUITHREAD" {
		t.Errorf("discovered thread = %q, want TUITHREAD", got)
	}
}

// TestDiscoverThreadRefusesCwdMismatch: a thread/started in a different cwd is REFUSED loudly
// (both paths named), not adopted — the hard-check ruling.
func TestDiscoverThreadRefusesCwdMismatch(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		if req["method"] == "initialize" {
			s.notify("thread/started", map[string]any{"thread": map[string]any{"id": "STRANGER", "cwd": "/somewhere/else"}})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	_, err := discoverThread(c, "/work", 2*time.Second)
	if err == nil {
		t.Fatal("cwd mismatch must be refused, not adopted")
	}
	for _, want := range []string{"/somewhere/else", "/work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name both paths; missing %q in: %v", want, err)
		}
	}
}

// TestJoinAs (m3): the wrapper joins channel/alias under the thread id as the session id, and
// the override does not leak past the call.
func TestJoinAs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	for _, k := range []string{"CBUS_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "GROK_SESSION_ID"} {
		t.Setenv(k, "")
	}
	if err := joinAs("THREAD9", "cxch", "cxpeer"); err != nil {
		t.Fatal(err)
	}
	if got := metaSessionID(filepath.Join(root, "cxch", "cxpeer", "meta.json")); got != "THREAD9" {
		t.Errorf("joinAs registered sid = %q, want THREAD9", got)
	}
	if SessionID() != "" {
		t.Errorf("joinAs leaked the session override: %q", SessionID())
	}
}

// TestDiscoverThreadTimeout: no thread/started before the deadline yields a diagnostic error,
// never a hang.
func TestDiscoverThreadTimeout(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{}) // never sends thread/started
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	_, err := discoverThread(c, "/work", 60*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not attach") {
		t.Errorf("timeout must diagnose the missing attach, got: %v", err)
	}
}
