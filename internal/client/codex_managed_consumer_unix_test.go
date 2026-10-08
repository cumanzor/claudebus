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
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	if mode := os.Getenv("CBUS_TEST_MANAGED_CODEX"); mode != "" {
		runManagedCodexFixture(mode)
		os.Exit(0)
	}
}

func runManagedCodexFixture(mode string) {
	if mode == "writer" {
		queue, err := os.OpenFile(os.Getenv("CBUS_TEST_MANAGED_QUEUE"), os.O_RDWR, 0)
		if err != nil {
			os.Exit(2)
		}
		defer queue.Close()
		flags := os.O_RDWR
		if os.Getenv("CBUS_TEST_MANAGED_READONLY") == "1" {
			flags = os.O_RDONLY
		}
		rollout, err := os.OpenFile(os.Getenv("CBUS_TEST_MANAGED_ROLLOUT"), flags, 0)
		if err != nil {
			os.Exit(3)
		}
		defer rollout.Close()
		fmt.Println(os.Getpid())
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	self, _ := os.Executable()
	args := []string{"app-server", "--listen", "unix://", "--analytics-default-enabled", "--managed-daemon"}
	if mode == "plain" {
		args = []string{"app-server", "--listen", "unix://"}
	}
	childMode := "writer"
	if mode == "desktop" {
		self, args = os.Getenv("CBUS_TEST_MANAGED_BINARY"), nil
		childMode = "managed"
	}
	child := exec.Command(self, args...)
	child.Env = append(os.Environ(), "CBUS_TEST_MANAGED_CODEX="+childMode)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Run(); err != nil {
		os.Exit(4)
	}
}

func managedConsumerFixture(t *testing.T, parentArgs []string, mode string, readOnly bool) (*ConnectionState, int, func()) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout.jsonl")
	queue := filepath.Join(dir, "queue_1.sqlite")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"`+daemonTestThread+`"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queue, nil, 0600); err != nil {
		t.Fatal(err)
	}
	launcher := binary
	if mode == "desktop" {
		launcher = filepath.Join(dir, "Codex.app", "Contents", "MacOS", "Codex")
		if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(launcher, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(launcher, parentArgs...)
	cmd.Env = append(os.Environ(), "CBUS_TEST_MANAGED_CODEX="+mode, "CBUS_TEST_MANAGED_QUEUE="+queue, "CBUS_TEST_MANAGED_ROLLOUT="+rollout, "CBUS_TEST_MANAGED_BINARY="+binary)
	if readOnly {
		cmd.Env = append(cmd.Env, "CBUS_TEST_MANAGED_READONLY=1")
	}
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
	childPID := 0
	stopped := false
	stopParent := func() {
		if !stopped {
			stopped = true
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(func() {
		_ = in.Close()
		if childPID > 0 {
			child, _ := os.FindProcess(childPID)
			_ = child.Kill()
		}
		stopParent()
	})
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		childPID, err = strconv.Atoi(strings.TrimSpace(line))
		if err != nil || childPID <= 1 {
			t.Fatalf("managed fixture did not start: %q", line)
		}
	case <-time.After(5 * time.Second):
		stopParent()
		t.Fatal("managed fixture startup timed out")
	}
	c := &ConnectionState{ThreadID: daemonTestThread, RolloutPath: rollout, Config: CodexQueueConfig{SQLiteHome: dir, RuntimePID: childPID, RuntimeStartToken: startTokenOf(t, childPID)}}
	return c, childPID, stopParent
}

func TestManagedCodexConsumer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parent     []string
		mode       string
		readOnly   bool
		wrongStore bool
		want       bool
	}{
		{name: "CLI managed writer", mode: "managed", want: true},
		{name: "plain server under CLI", mode: "plain"},
		{name: "exec parent", mode: "managed", parent: []string{"exec", "prompt"}},
		{name: "server parent", mode: "managed", parent: []string{"app-server"}},
		{name: "desktop ancestor", mode: "desktop"},
		{name: "no CLI parent", mode: "writer", parent: []string{"app-server", "--listen", "unix://", "--managed-daemon"}},
		{name: "read-only rollout", mode: "managed", readOnly: true},
		{name: "wrong queue store", mode: "managed", wrongStore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, pid, stopParent := managedConsumerFixture(t, tc.parent, tc.mode, tc.readOnly)
			if tc.wrongStore {
				c.Config.SQLiteHome = t.TempDir()
				if err := os.WriteFile(filepath.Join(c.Config.SQLiteHome, "queue_1.sqlite"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p, err := observeCodexConsumer(ctx, c)
			if err != nil || (p.State == "online") != tc.want || tc.want && p.PID != pid {
				t.Fatalf("managed consumer: %+v, error=%v; want online=%v pid=%d", p, err, tc.want, pid)
			}
			if tc.want {
				d, _, _ := daemonFixture(t)
				d.probeConsumer = observeCodexConsumer
				if err := d.requireCLIConsumer(c); err != nil {
					t.Fatalf("managed runtime failed admission: %v", err)
				}
				c.Config.RuntimeStartToken += "-stale"
				if err := d.requireCLIConsumer(c); err == nil {
					t.Fatal("managed runtime admitted with a stale caller witness")
				}
				c.Config.RuntimeStartToken = p.StartToken
				// admitting a shared backend must not authorize killing it
				c.Consumer = &consumerObservation{State: p.State, PID: p.PID, StartToken: p.StartToken}
				if _, err := resolveNativeConsumer(c, os.Getpid()); err == nil || !strings.Contains(err.Error(), "not an interactive codex CLI") {
					t.Fatalf("managed backend did not hit the signal exclusion: %v", err)
				}
				_, stopDuplicate := startCodexWriterFixture(t, filepath.Join(c.Config.SQLiteHome, "codex"), c.RolloutPath, filepath.Join(c.Config.SQLiteHome, "queue_1.sqlite"), "writer")
				if p, err := observeCodexConsumer(ctx, c); err == nil || p.State != "unknown" {
					t.Fatalf("ambiguous managed/interactive writers accepted: %+v %v", p, err)
				}
				stopDuplicate()
				stopParent()
				if p, err := observeCodexConsumer(ctx, c); err != nil || p.State != "unknown" {
					t.Fatalf("orphaned managed backend accepted: %+v %v", p, err)
				}
			}
		})
	}
}
