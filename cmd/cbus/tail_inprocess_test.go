package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
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

// TestStealDisplacesThroughTheRealCLI drives the displacement gate and --steal through
// the actual binary. In-process coverage would not have exercised the flag parser, the
// refusal's exit code, or what a displaced follower's Monitor actually sees — which is
// the class of miss the review doctrine exists for.
func TestStealDisplacesThroughTheRealCLI(t *testing.T) {
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
	run := func(sid string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env(sid)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("sid-a", "join", "ch", "al"); err != nil {
		t.Fatalf("join: %v\n%s", err, out)
	}

	// the incumbent tail
	first := exec.Command(bin, "tail", "ch/al")
	first.Env = env("sid-a")
	firstOut, err := first.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Process.Kill(); _, _ = first.Process.Wait() })

	metaPath := filepath.Join(root, "ch", "al", "meta.json")
	waitUntil(t, 10*time.Second, func() bool {
		b, err := os.ReadFile(metaPath)
		return err == nil && strings.Contains(string(b), `"listenerStart"`) &&
			!strings.Contains(string(b), `"listenerPid": null`)
	}, "the first tail to arm")

	// The gate: a second plain arm must be REFUSED, and must say how to proceed.
	//
	// BOUNDED on purpose. If the gate regresses, the second arm does not error — it
	// becomes a follower and blocks forever, so an unbounded CombinedOutput() here would
	// HANG rather than fail. A test that wedges on regression is worse than one that
	// fails: it burns a CI slot and reports a timeout instead of a cause. (Found the
	// hard way: this is exactly how my own gate-removal mutation run wedged.)
	gateCtx, cancelGate := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelGate()
	refuse := exec.CommandContext(gateCtx, bin, "tail", "ch/al")
	refuse.Env = env("sid-b")
	out, err := refuse.CombinedOutput()
	if gateCtx.Err() != nil {
		t.Fatalf("the second arm never exited — the gate let it become a live follower:\n%s", out)
	}
	if err == nil {
		t.Fatalf("a second tail armed over a live one:\n%s", out)
	}
	if !strings.Contains(string(out), "--steal") {
		t.Errorf("the refusal does not name the escape hatch:\n%s", out)
	}

	// --steal takes over
	second := exec.Command(bin, "tail", "--steal", "ch/al")
	second.Env = env("sid-b")
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Process.Kill(); _, _ = second.Process.Wait() })

	// the displaced follower must END, and say why in words that are TRUE
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(firstOut)
		done <- string(b)
	}()
	select {
	case tail := <-done:
		if !strings.Contains(tail, "displaced by another listener") {
			t.Errorf("displaced follower's last words were %q; want the displacement marker", tail)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the displaced follower never exited — --steal did not displace it")
	}
	if err := first.Wait(); err != nil {
		t.Errorf("displaced follower exited %v; displacement is a deliberate outcome, not a failure", err)
	}
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
