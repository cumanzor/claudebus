//go:build darwin || linux

package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
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
		control := filepath.Join(filepath.Dir(os.Getenv("CBUS_TEST_MANAGED_QUEUE")), "app-server-control")
		if os.MkdirAll(control, 0700) != nil {
			os.Exit(5)
		}
		listener, err := net.Listen("unix", filepath.Join(control, "app-server-control.sock"))
		if err != nil {
			os.Exit(6)
		}
		defer listener.Close()
		fake := &fakeCodex{requireInit: true, handle: func(s *fakeSrv, req map[string]any) {
			if req["method"] == "initialize" {
				s.reply(req["id"], map[string]any{})
				return
			}
			if req["method"] == "initialized" {
				return
			}
			if req["method"] != "mcpServerStatus/list" {
				s.replyErr(req["id"], -32601, "read-only fixture")
				return
			}
			params, _ := req["params"].(map[string]any)
			if params["threadId"] != daemonTestThread || params["serverName"] != "codex_tui" {
				s.replyErr(req["id"], -32602, "exact thread and TUI server required")
				return
			}
			origin, _ := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("CBUS_TEST_MANAGED_QUEUE")), "frontend-origin"))
			s.reply(req["id"], map[string]any{"data": []any{map[string]any{"name": "codex_tui", "httpOrigin": string(origin)}}, "nextCursor": nil})
		}}
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				go func() { defer conn.Close(); fake.serve(conn) }()
			}
		}()
		fmt.Println(os.Getpid())
		select {}
	}
	address := os.Getenv("CBUS_TEST_FRONTEND_ADDR")
	if address == "" {
		address = "127.0.0.1:0"
	}
	frontend, err := net.Listen("tcp4", address)
	if err != nil {
		os.Exit(7)
	}
	defer frontend.Close()
	if err := os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("CBUS_TEST_MANAGED_QUEUE")), "frontend-origin"), []byte("http://"+frontend.Addr().String()), 0600); err != nil {
		os.Exit(8)
	}
	if mode == "frontend" {
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
	dir, err := os.MkdirTemp("", "cbus-managed-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
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
	c := &ConnectionState{ThreadID: daemonTestThread, RolloutPath: rollout, Config: CodexQueueConfig{Binary: binary, Home: dir, SQLiteHome: dir, Cwd: dir, UserHome: dir, RuntimePID: childPID, RuntimeStartToken: startTokenOf(t, childPID)}}
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
			if tc.want && err != nil || (p.State == "online") != tc.want || tc.want && p.PID != pid {
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
				c.Consumer = &consumerObservation{State: p.State, PID: p.PID, StartToken: p.StartToken, Managed: p.Managed, Frontend: p.Frontend}
				if _, err := resolveNativeConsumer(c, os.Getpid()); err == nil || !strings.Contains(err.Error(), "not an interactive codex CLI") {
					t.Fatalf("managed backend did not hit the signal exclusion: %v", err)
				}
				_, stopDuplicate := startCodexWriterFixture(t, filepath.Join(c.Config.SQLiteHome, "codex"), c.RolloutPath, filepath.Join(c.Config.SQLiteHome, "queue_1.sqlite"), "writer")
				if p, err := observeCodexConsumer(ctx, c); err == nil || p.State != "unknown" {
					t.Fatalf("ambiguous managed/interactive writers accepted: %+v %v", p, err)
				}
				stopDuplicate()
				stopParent()
				if p, err := observeCodexConsumer(ctx, c); err != nil || p.State != "exited" {
					t.Fatalf("orphaned managed backend accepted: %+v %v", p, err)
				}
			}
		})
	}
}

func TestManagedLifecycleFrontendExitKeepsMailInBus(t *testing.T) {
	fixture, backend, stopFrontend := managedConsumerFixture(t, nil, "managed", false)
	d, q, req := daemonFixture(t)
	req.Config = fixture.Config
	q.thread.Path = fixture.RolloutPath
	d.probeConsumer = observeCodexConsumer
	observer := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	stopFrontend()
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.Consumer.State != "exited" || c.Consumer.PresenceOnline {
		t.Fatalf("CLI exit hidden by surviving backend %d: %+v", backend, c.Consumer)
	}
	if got := strings.Join(presenceEvents(t, observer), ","); got != "dev/worker join,dev/worker departed" {
		t.Fatalf("presence: %s", got)
	}
	appendDaemonMessage(t, c, "", "retain until exact CLI resumes")
	before := c.Offset
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 0 || c.Offset != before || c.Pending != nil || c.Accepted != 0 {
		t.Fatalf("offline message submitted: %+v, calls=%v", c, q.calls)
	}
	if exited, err := codexOwnerExited(backend, fixture.Config.RuntimeStartToken); err != nil || exited {
		t.Fatalf("shared backend changed: exited=%v error=%v", exited, err)
	}
}

func TestManagedLifecycleSubmissionRechecksFrontend(t *testing.T) {
	fixture, _, stopFrontend := managedConsumerFixture(t, nil, "managed", false)
	d, q, req := daemonFixture(t)
	req.Config = fixture.Config
	q.thread.Path = fixture.RolloutPath
	d.probeConsumer = observeCodexConsumer
	c := mustDaemonConnect(t, d, req)
	stopFrontend()
	appendDaemonMessage(t, c, "", "arrived before scheduled presence refresh")
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 0 || c.Pending != nil || c.Accepted != 0 {
		t.Fatalf("cached online state allowed offline delivery: calls=%v connection=%+v", q.calls, c)
	}
}

func replacementManagedFrontend(t *testing.T, c *ConnectionState, address string) int {
	t.Helper()
	cmd := exec.Command(c.Config.Binary, "resume", c.ThreadID)
	cmd.Env = append(os.Environ(), "CBUS_TEST_MANAGED_CODEX=frontend", "CBUS_TEST_MANAGED_QUEUE="+filepath.Join(c.Config.SQLiteHome, "queue_1.sqlite"), "CBUS_TEST_FRONTEND_ADDR="+address)
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
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid != cmd.Process.Pid {
			t.Fatalf("frontend failed: %q", line)
		}
		return pid
	case <-time.After(5 * time.Second):
		t.Fatal("frontend startup timed out")
	}
	return 0
}

func TestManagedLifecycleResumeRetainsBackendAndDeliversOnce(t *testing.T) {
	fixture, backend, stopFrontend := managedConsumerFixture(t, nil, "managed", false)
	d, q, req := daemonFixture(t)
	req.Config, q.thread.Path = fixture.Config, fixture.RolloutPath
	d.probeConsumer = observeCodexConsumer
	observer := presencePeer(t, req.Channel, "observer")
	c := mustDaemonConnect(t, d, req)
	stopFrontend()
	appendDaemonMessage(t, c, "", "offline mail")
	if err := d.deliver(c); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 0 {
		t.Fatal("offline mail left bus inbox")
	}
	d = reloadDaemonFixture(t, q)
	d.probeConsumer = observeCodexConsumer
	c = d.connections[c.ID]
	if !c.Consumer.Managed || c.Consumer.Frontend == nil {
		t.Fatal("restart lost frontend binding")
	}
	if err := d.deliver(c); err != nil || len(q.calls) != 0 {
		t.Fatalf("restart released offline mail: %v", err)
	}
	resumed := replacementManagedFrontend(t, fixture, "")
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.Consumer.State != "online" || c.Consumer.PID != backend || c.Consumer.Frontend.PID != resumed {
		t.Fatalf("resume binding: %+v", c.Consumer)
	}
	if got := strings.Join(presenceEvents(t, observer), ","); got != "dev/worker join,dev/worker departed,dev/worker join" {
		t.Fatalf("presence: %s", got)
	}
	for range 2 {
		if err := d.deliver(c); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.calls) != 1 || c.Accepted != 1 || c.Pending != nil {
		t.Fatalf("resume replay: calls=%v connection=%+v", q.calls, c)
	}
}

func TestManagedLifecycleRejectsReusedFrontendPort(t *testing.T) {
	fixture, _, stopFrontend := managedConsumerFixture(t, nil, "managed", false)
	p, err := observeCodexConsumer(context.Background(), fixture)
	if err != nil || p.Frontend == nil {
		t.Fatalf("initial frontend: %+v %v", p, err)
	}
	fixture.Consumer = &consumerObservation{State: p.State, PID: p.PID, StartToken: p.StartToken, Managed: true, Frontend: p.Frontend}
	stopFrontend()
	replacementManagedFrontend(t, fixture, strings.TrimPrefix(p.Frontend.Origin, "http://"))
	if p, err := observeCodexConsumer(context.Background(), fixture); err == nil || p.State != "unknown" {
		t.Fatalf("reused endpoint accepted: %+v %v", p, err)
	}
}
