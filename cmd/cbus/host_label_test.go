package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withStdin runs f with os.Stdin reading payload.
func withStdin(t *testing.T, payload string, f func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; _ = r.Close() }()
	f()
}

func assertFixItMessage(t *testing.T, stderr, bad string) {
	t.Helper()
	for _, want := range []string{"CBUS_HOST", `"` + bad + `"`, "letters, digits", "unset it"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal %q does not tell the user how to fix it (missing %q)", stderr, want)
		}
	}
}

func TestInvalidHostLabelRefusesNonHookVerbs(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CBUS_HOST", "bad/host")
	for _, args := range [][]string{{"list"}, {"channels"}, {"whoami"}, {"join", "cc", "coder"}, {"send", "cc/x", "hi"}, {"formation", "list"}} {
		var rc int
		stderr := captureStderr(t, func() { rc = run(args) })
		if rc == 0 {
			t.Errorf("%v under an invalid CBUS_HOST: rc 0, want a refusal", args)
			continue
		}
		assertFixItMessage(t, stderr, "bad/host")
	}
	if entries, _ := os.ReadDir(os.Getenv("CBUS_DIR")); len(entries) != 0 {
		t.Errorf("refused verbs still wrote to the store: %v", entries)
	}
}

func TestInvalidHostLabelSparesHelpVersionAndHooks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CBUS_HOST", "bad/host")
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--version"}, {"version"}} {
		if rc := captureRC(t, func() int { return run(args) }); rc != 0 {
			t.Errorf("%v must not resolve the host: rc %d", args, rc)
		}
	}
	t.Setenv("CBUS_CHANNEL", "cc")
	t.Setenv("CBUS_ALIAS", "coder")
	// a runner inside Claude Code inherits the socket; hook-join would then skip for
	// that reason instead of refusing the label, and the assertions below would pass blind
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	for _, hook := range [][]string{{"hook-join"}, {"hook-exit"}, {"hook-compact", "pre"}} {
		var rc int
		var stdout, stderr string
		stderr = captureStderr(t, func() {
			stdout = captureStdout(t, func() {
				withStdin(t, `{"session_id":"hook-sid"}`, func() { rc = run(hook) })
			})
		})
		if rc != 0 {
			t.Errorf("%v under an invalid CBUS_HOST: rc %d, want 0 (hook contract)", hook, rc)
		}
		if stdout != "" {
			t.Errorf("%v wrote %d stdout bytes under an invalid CBUS_HOST: %q", hook, len(stdout), stdout)
		}
		if hook[0] == "hook-join" && !strings.Contains(stderr, "CBUS_HOST") {
			t.Errorf("hook-join did not name CBUS_HOST on stderr: %q", stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "cc")); !os.IsNotExist(err) {
		t.Errorf("a hook registered a peer under an invalid CBUS_HOST: %v", err)
	}
}

// The daemon is a non-hook verb: it must refuse at start rather than serve under a
// substituted label. Run as a subprocess with a deadline so a daemon that does start
// fails the test instead of hanging it.
func TestDaemonRefusesToStartUnderAnInvalidHostLabel(t *testing.T) {
	bin := buildCbus(t)
	for _, sub := range []string{"serve", "start"} {
		store, err := os.MkdirTemp("/tmp", "cbh")
		if err != nil {
			t.Fatal(err)
		}
		env := append(os.Environ(), "CBUS_DIR="+store, "CBUS_HOST=bad/host")
		t.Cleanup(func() {
			// a daemon that wrongly started detaches; stop it before its store goes
			stop := exec.Command(bin, "daemon", "stop")
			stop.Env = append(os.Environ(), "CBUS_DIR="+store)
			_ = stop.Run()
			_ = os.RemoveAll(store)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, "daemon", sub)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Errorf("daemon %s kept running under an invalid CBUS_HOST", sub)
			continue
		}
		if err == nil {
			t.Errorf("daemon %s exited 0 under an invalid CBUS_HOST: %s", sub, out)
		}
		assertFixItMessage(t, string(out), "bad/host")
		if _, err := os.Stat(filepath.Join(store, ".daemon")); !os.IsNotExist(err) {
			t.Errorf("daemon %s created its state dir before refusing: %v", sub, err)
		}
	}
}
