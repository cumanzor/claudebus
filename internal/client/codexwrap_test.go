package client

import (
	"errors"
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
	got, err := discoverThread(c, rendezvous{wantCwd: "/work", timeout: 2 * time.Second}, nil)
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
	_, err := discoverThread(c, rendezvous{wantCwd: "/work", timeout: 2 * time.Second}, nil)
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
	_, err := discoverThread(c, rendezvous{wantCwd: "/work", timeout: 60 * time.Millisecond}, nil)
	if err == nil || !strings.Contains(err.Error(), "did not attach") {
		t.Errorf("timeout must diagnose the missing attach, got: %v", err)
	}
}

// TestUuidLike: only the 8-4-4-4-12 hex shape passes, so a session NAME is never mistaken for
// a thread id.
func TestUuidLike(t *testing.T) {
	for _, ok := range []string{"01a06968-3620-7a63-a5ba-1b25c394ccbd", "AAAAAAAA-bbbb-CCCC-dddd-000000000000"} {
		if !uuidLike(ok) {
			t.Errorf("uuidLike(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "--last", "my-session", "not-a-uuid-at-all-here-nope", "01a06968-3620-7a63-a5ba-1b25c394ccbdd", "01a06968_3620_7a63_a5ba_1b25c394ccbd", "01a06968-3620-7a63-a5ba-1b25c394ccbg"} {
		if uuidLike(bad) {
			t.Errorf("uuidLike(%q) = true, want false", bad)
		}
	}
}

// TestResumeLaunch: a resume is recognised wherever the subcommand sits, and the session id is
// read out only when the args actually name one — --last and a session name leave it empty, so
// the wrapper rendezvouses instead of joining under something the server never heard of.
func TestResumeLaunch(t *testing.T) {
	const sid = "01a06968-3620-7a63-a5ba-1b25c394ccbd"
	for name, tc := range map[string]struct {
		args       []string
		wantResume bool
		wantSID    string
	}{
		"fresh":            {nil, false, ""},
		"fresh with flags": {[]string{"--search", "-c", "model=o3"}, false, ""},
		"resume by id":     {[]string{"resume", sid}, true, sid},
		"resume last":      {[]string{"resume", "--last"}, true, ""},
		"resume by name":   {[]string{"resume", "my-session"}, true, ""},
		"resume picker":    {[]string{"resume"}, true, ""},
		"opts before sub":  {[]string{"-c", "model=o3", "resume", sid}, true, sid},
		"id with prompt":   {[]string{"resume", sid, "carry on"}, true, sid},
		"flag before id":   {[]string{"resume", "--all", sid}, true, sid},
	} {
		gotResume, gotSID := resumeLaunch(tc.args)
		if gotResume != tc.wantResume || gotSID != tc.wantSID {
			t.Errorf("%s: resumeLaunch(%v) = %v,%q want %v,%q", name, tc.args, gotResume, gotSID, tc.wantResume, tc.wantSID)
		}
	}
}

// TestThreadNoteID: the flat threadId a resumed thread's notifications carry is read, as is the
// nested thread.id of thread/started; anything else yields "".
func TestThreadNoteID(t *testing.T) {
	for name, tc := range map[string]struct{ params, want string }{
		"flat":    {`{"threadId":"T1","status":{"type":"idle"}}`, "T1"},
		"nested":  {`{"thread":{"id":"T2","cwd":"/work"}}`, "T2"},
		"neither": {`{"status":"disabled","serverName":"h"}`, ""},
	} {
		if got := threadNoteID([]byte(tc.params)); got != tc.want {
			t.Errorf("%s: threadNoteID = %q, want %q", name, got, tc.want)
		}
	}
}

// TestDiscoverThreadResumeAdoptsStatusNotification: a RESUMED thread is announced by
// thread/status/changed, never thread/started (measured, codex-cli 0.153.4), so the resume
// rendezvous adopts the id that notification names.
func TestDiscoverThreadResumeAdoptsStatusNotification(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		if req["method"] == "initialize" {
			s.notify("thread/status/changed", map[string]any{"threadId": "RESUMED", "status": map[string]any{"type": "idle"}})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	got, err := discoverThread(c, rendezvous{wantCwd: "/work", resume: true, timeout: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "RESUMED" {
		t.Errorf("resumed thread = %q, want RESUMED", got)
	}
}

// TestDiscoverThreadFreshIgnoresStatusNotification: the wider acceptance is scoped to resume.
// A fresh launch still waits for thread/started, so the cwd hard-check cannot be sidestepped by
// a notification that carries no cwd to check.
func TestDiscoverThreadFreshIgnoresStatusNotification(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		if req["method"] == "initialize" {
			s.notify("thread/status/changed", map[string]any{"threadId": "STRANGER", "status": map[string]any{"type": "idle"}})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	got, err := discoverThread(c, rendezvous{wantCwd: "/work", timeout: 150 * time.Millisecond}, nil)
	if err == nil {
		t.Fatalf("fresh launch adopted %q from a thread/status/changed; only thread/started is cwd-checked", got)
	}
	if !strings.Contains(err.Error(), "did not attach") {
		t.Errorf("want the fresh-path timeout diagnosis, got: %v", err)
	}
}

// TestDiscoverThreadResumeSkipsCwdCheck: a resumed session carries the cwd it was RECORDED in,
// which legitimately differs from the wrapper's, so cwd is not an identity check there.
func TestDiscoverThreadResumeSkipsCwdCheck(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		if req["method"] == "initialize" {
			s.notify("thread/started", map[string]any{"thread": map[string]any{"id": "ELSEWHERE", "cwd": "/somewhere/else"}})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	got, err := discoverThread(c, rendezvous{wantCwd: "/work", resume: true, timeout: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ELSEWHERE" {
		t.Errorf("resumed thread = %q, want ELSEWHERE", got)
	}
}

// TestDiscoverThreadAbortsOnTUIExit: a dead TUI ends the wait immediately with what actually
// happened, instead of sitting out a timer (five minutes on the picker path) and then blaming
// an attach that did happen.
func TestDiscoverThreadAbortsOnTUIExit(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) { s.reply(req["id"], map[string]any{}) })
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	tuiExit := make(chan error, 1)
	tuiExit <- errors.New("exit status 1")
	start := time.Now()
	_, err := discoverThread(c, rendezvous{resume: true, timeout: time.Minute}, tuiExit)
	if err == nil || !strings.Contains(err.Error(), "exited before its thread was known") {
		t.Fatalf("want the TUI-exit cause, got: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("cause must carry the TUI's own error: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("waited %s for a dead TUI; the exit must end the wait, not the timer", d)
	}
}

// TestDiscoverThreadRefusesWrongThread: when the launch named a session id, a thread that is
// not that one is refused loudly (both ids named) rather than bridged, so the bridge can never
// deliver into a thread the TUI is not showing.
func TestDiscoverThreadRefusesWrongThread(t *testing.T) {
	f := startFakeCodex(t, func(s *fakeSrv, req map[string]any) {
		s.reply(req["id"], map[string]any{})
		if req["method"] == "initialize" {
			s.notify("thread/status/changed", map[string]any{"threadId": "OTHER", "status": map[string]any{"type": "idle"}})
		}
	})
	c := mustDial(t, f.sock)
	defer c.close()
	if _, err := c.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	_, err := discoverThread(c, rendezvous{resume: true, wantID: "ASKED", timeout: 2 * time.Second}, nil)
	if err == nil {
		t.Fatal("a thread other than the one asked for must be refused")
	}
	for _, want := range []string{"OTHER", "ASKED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name both ids; missing %q in: %v", want, err)
		}
	}
}
