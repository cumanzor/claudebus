//go:build darwin || linux

package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("CBUS_TEST_ROLLOUT_WRITER") == "1" {
		TestCodexWriterProcessHelper(nil)
		os.Exit(0)
	}
}

func TestCodexWriterProcessHelper(t *testing.T) {
	if os.Getenv("CBUS_TEST_ROLLOUT_WRITER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 4 {
		os.Exit(2)
	}
	flags := os.O_RDWR | os.O_APPEND
	if args[3] == "reader" {
		flags = os.O_RDONLY
	}
	roll, err := os.OpenFile(args[1], flags, 0)
	if err != nil {
		os.Exit(3)
	}
	defer roll.Close()
	queue, err := os.OpenFile(args[2], os.O_RDWR, 0)
	if err != nil {
		os.Exit(4)
	}
	defer queue.Close()
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func startCodexWriterFixture(t *testing.T, binary, rollout, queue, mode string) (*exec.Cmd, func()) {
	t.Helper()
	args := []string{"--", rollout, queue, mode}
	if mode == "app-server" {
		args = append([]string{"app-server"}, args...)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "CBUS_TEST_ROLLOUT_WRITER=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = in.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(stop)
	ready := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(out).ReadString('\n'); ready <- s }()
	select {
	case s := <-ready:
		if strings.TrimSpace(s) != "READY" {
			t.Fatalf("writer fixture did not start: %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer fixture startup timeout")
	}
	return cmd, stop
}

func TestConsumerWriterDiscoveryExitResumeAndReadOnlyExclusion(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, bytes, 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-"+daemonTestThread+".jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"`+daemonTestThread+`"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	queue := filepath.Join(dir, "queue_1.sqlite")
	if err := os.WriteFile(queue, nil, 0600); err != nil {
		t.Fatal(err)
	}
	c := &ConnectionState{ThreadID: daemonTestThread, RolloutPath: rollout, Config: CodexQueueConfig{SQLiteHome: dir}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first, stopFirst := startCodexWriterFixture(t, binary, rollout, queue, "writer")
	_, stopReader := startCodexWriterFixture(t, binary, rollout, queue, "reader")
	p, err := observeCodexConsumer(ctx, c)
	if err != nil || p.State != "online" || p.PID != first.Process.Pid {
		t.Fatalf("positive exact writer: %+v %v", p, err)
	}
	c.Consumer = &consumerObservation{State: p.State, PID: p.PID, StartToken: p.StartToken, PresenceOnline: true}
	stopFirst()
	p, err = observeCodexConsumer(ctx, c)
	if err != nil || p.State != "exited" {
		t.Fatalf("read-only descriptor concealed exit: %+v %v", p, err)
	}
	stopReader()
	second, stopSecond := startCodexWriterFixture(t, binary, rollout, queue, "writer")
	p, err = observeCodexConsumer(ctx, c)
	if err != nil || p.State != "online" || p.PID != second.Process.Pid {
		t.Fatalf("exact writer resume: %+v %v", p, err)
	}
	_, stopDuplicate := startCodexWriterFixture(t, binary, rollout, queue, "writer")
	p, err = observeCodexConsumer(ctx, c)
	if err == nil || p.State != "unknown" {
		t.Fatalf("duplicate writers were accepted: %+v %v", p, err)
	}
	stopDuplicate()
	stopSecond()
	_, stopServer := startCodexWriterFixture(t, binary, rollout, queue, "app-server")
	p, err = observeCodexConsumer(ctx, c)
	if err != nil || p.State == "online" {
		t.Fatalf("app-server writer admitted as a CLI: %+v %v", p, err)
	}
	stopServer()
	otherQueue := filepath.Join(t.TempDir(), "queue_1.sqlite")
	if err := os.WriteFile(otherQueue, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, stopWrongStore := startCodexWriterFixture(t, binary, rollout, otherQueue, "writer")
	p, err = observeCodexConsumer(ctx, c)
	if err != nil || p.State == "online" {
		t.Fatalf("writer using another queue store admitted: %+v %v", p, err)
	}
	stopWrongStore()
}

func TestConsumerRolloutReplacementCannotBorrowValidatedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"`+daemonTestThread+`"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openConsumerRollout(path, daemonTestThread)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	identity, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"another-thread"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if sameOpenFile(identity, path) {
		t.Fatal("replacement borrowed the validated UUID")
	}
	if !sameOpenFile(identity, path+".old") {
		t.Fatal("open validated rollout identity was lost")
	}
}
