package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTailArmsAndStreamsInProcess drives `cbus tail` through the REAL binary, the way
// a session actually arms, because the in-process follower changed how that entry
// behaves and an in-process call would not have exercised it. It asserts the three
// things a session depends on and that unit tests cannot see together: the arm records
// a live listener, the SAME process streams frames (no re-exec), and a peer sees it as
// listening.
//
// HARNESS EXCEPTION to the never-run-`cbus tail`-under-Bash doctrine: the follower is
// spawned as a child with a hard read deadline and killed on cleanup, so it cannot
// wedge this test the way a live session's blocking tail would. That bound is the
// whole reason this is allowed here and nowhere else.
func TestTailArmsAndStreamsInProcess(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cbus")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/cmd/cbus").CombinedOutput(); err != nil {
		t.Fatalf("build cbus: %v\n%s", err, out)
	}
	root := t.TempDir()
	env := func(sid string) []string {
		e := []string{"CBUS_DIR=" + root, "CLAUDE_CODE_SESSION_ID=" + sid}
		for _, kv := range os.Environ() {
			switch strings.SplitN(kv, "=", 2)[0] {
			case "CBUS_DIR", "CLAUDE_CODE_SESSION_ID", "CBUS_UPDATE_CHECK":
			default:
				e = append(e, kv)
			}
		}
		return e
	}
	run := func(sid string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env(sid)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cbus %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("sid-listener", "join", "ch", "listener")
	run("sid-peer", "join", "ch", "peer")

	tail := exec.Command(bin, "tail", "ch/listener")
	tail.Env = env("sid-listener")
	stdout, err := tail.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := tail.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tail.Process.Kill()
		_, _ = tail.Process.Wait()
	})

	// the arm is asynchronous: wait for the meta to show a live listener
	metaPath := filepath.Join(root, "ch", "listener", "meta.json")
	deadline := time.Now().Add(10 * time.Second)
	var armed bool
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(metaPath); err == nil {
			var m struct {
				ListenerPid   int    `json:"listenerPid"`
				ListenerStart string `json:"listenerStart"`
			}
			if json.Unmarshal(b, &m) == nil && m.ListenerPid != 0 && m.ListenerStart != "" {
				// the in-process contract: the pid in the meta IS the process we spawned,
				// not a re-exec'd replacement and not a child of it
				if m.ListenerPid != tail.Process.Pid {
					t.Fatalf("listenerPid %d is not the tail process %d — something re-exec'd or forked",
						m.ListenerPid, tail.Process.Pid)
				}
				armed = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !armed {
		t.Fatal("tail never recorded a live listener with a structural witness")
	}

	// a peer must see it as listening, through the real verb
	if out := run("sid-peer", "list", "ch"); !strings.Contains(out, "listen") ||
		!strings.Contains(out, "ch/listener") {
		t.Errorf("list does not show the armed listener:\n%s", out)
	}

	run("sid-peer", "send", "ch/listener", "hello from the peer")

	// bounded read: a frame must arrive, and a wedged follower fails the test rather
	// than hanging it.
	type line struct {
		s   string
		err error
	}
	lines := make(chan line, 1)
	go func() {
		r := bufio.NewReader(stdout)
		for {
			s, err := r.ReadString('\n')
			if s != "" && strings.Contains(s, "hello from the peer") {
				lines <- line{s, nil}
				return
			}
			if err != nil {
				lines <- line{"", err}
				return
			}
		}
	}()
	select {
	case got := <-lines:
		if got.err != nil {
			t.Fatalf("follower stream ended before the message arrived: %v", got.err)
		}
		if !strings.Contains(got.s, "hello from the peer") {
			t.Errorf("streamed line = %q", got.s)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no frame streamed within the deadline — the in-process follower is not streaming")
	}
}

// TestTailRejectsTheRetiredFollowerFlag: `--inbox` was a hidden flag that turned this
// verb into the re-exec'd follower. It is gone, so the flag must now be treated as an
// ordinary (bad) target rather than silently doing something else. Exercised through
// the real CLI, since a hidden flag is only reachable that way.
func TestTailRejectsTheRetiredFollowerFlag(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cbus")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/cmd/cbus").CombinedOutput(); err != nil {
		t.Fatalf("build cbus: %v\n%s", err, out)
	}
	root := t.TempDir()
	cmd := exec.Command(bin, "tail", "--inbox", filepath.Join(root, "x", "y", "inbox.jsonl"))
	cmd.Env = []string{"CBUS_DIR=" + root, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("`tail --inbox <path>` succeeded; it must not still act as a follower:\n%s", out)
	}
}
