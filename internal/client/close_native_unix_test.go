//go:build darwin || linux

package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const closeTestSession = "22222222-2222-4222-8222-222222222222"

type disconnectCall struct {
	target        string
	fence         disconnectFence
	consumerAlive bool
}

type nativeCloseFixture struct {
	daemon, consumer int
	calls            []disconnectCall
	binding          ClaudeConnectBinding
	state            *ConnectionState
}

// seedNativeClaude registers dev/worker as a daemon-managed Claude peer whose
// bound consumer is pid, recorded with start, and whose listener is the daemon.
func seedNativeClaude(t *testing.T, consumer int, start string) *nativeCloseFixture {
	t.Helper()
	root := setupStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	// behind the fake tree's ppid==1 barrier: no ancestry walk from the daemon pid
	// can reach a real session
	daemon, _ := fakeSessionTree(t, "cbusd", "fixture-daemon")
	f := &nativeCloseFixture{daemon: daemon, consumer: consumer}
	config := filepath.Join(t.TempDir(), "claude-config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	f.binding = ClaudeConnectBinding{SessionID: closeTestSession, ConfigHome: config,
		Endpoint: claudeEndpoint{Socket: filepath.Join(config, "messaging.sock"), PID: consumer, StartToken: start}}
	writeClaudeSessionRegistry(t, f.binding)
	c := &ConnectionState{ID: "closeconn01", Harness: daemonHarnessClaude, Channel: "dev", Alias: "worker", ThreadID: closeTestSession,
		Claude: &ClaudeConnectionConfig{Binding: f.binding}, State: "socket-ready"}
	f.state = c
	writeCloseJournal(t, c)
	dir := filepath.Join(root, "dev", "worker")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inbox.jsonl"), []byte("{\"text\":\"unread\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := peerMeta{Alias: "worker", Channel: "dev", SessionID: closeTestSession, Harness: daemonHarnessClaude, ConnectionID: c.ID, Host: thisHost(),
		ListenerPid: json.RawMessage(strconv.Itoa(f.daemon)), ListenerStart: startTokenOf(t, f.daemon), OwnerPid: jsonNull}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	prev, prevFences := nativeDisconnect, nativeDaemonFences
	nativeDisconnect = func(target string, fence disconnectFence) (string, error) {
		f.calls = append(f.calls, disconnectCall{target, fence, pidAlive(f.consumer) && !procZombie(f.consumer)})
		return fence.ConnectionID, nil
	}
	nativeDaemonFences = func() error { return nil }
	t.Cleanup(func() { nativeDisconnect, nativeDaemonFences = prev, prevFences })
	return f
}

func writeCloseJournal(t *testing.T, c *ConnectionState) {
	t.Helper()
	dir := filepath.Join(DaemonDir(), "connections")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, c.ID+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func fakeClaude(t *testing.T) int {
	t.Helper()
	owner, _ := fakeSessionTree(t, "claude", "native-consumer")
	return owner
}

// processRunning also asks for a start time: a killed but unreaped test child still
// answers kill -0, and darwin's zombie check does not see it.
func processRunning(pid int) bool {
	_, err := procStartTime(pid)
	return err == nil && pidAlive(pid) && !procZombie(pid)
}

func TestNativeCloseSignalsBoundClaudeNotTheDaemon(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	rep := ClosePeer("dev", "worker", false)
	if !rep.Ok || !strings.Contains(rep.Detail, "process ended") {
		t.Fatalf("close did not end the bound consumer: %+v", rep)
	}
	if processRunning(consumer) {
		t.Fatal("bound consumer survived the close")
	}
	if !processRunning(f.daemon) {
		t.Fatal("close signalled the daemon")
	}
	if len(f.calls) != 1 || !f.calls[0].consumerAlive {
		t.Fatalf("disconnect must run once, before the signal: %+v", f.calls)
	}
	want := disconnectFence{ConnectionID: "closeconn01", ThreadID: closeTestSession, ConsumerPID: consumer, ConsumerStart: f.binding.Endpoint.StartToken}
	if f.calls[0].fence != want || f.calls[0].target != "dev/worker" {
		t.Fatalf("disconnect was not fenced to this registration and incarnation: %+v", f.calls[0])
	}
	if b, err := os.ReadFile(filepath.Join(CBUSDir(), "dev", "worker", "inbox.jsonl")); err != nil || !strings.Contains(string(b), "unread") {
		t.Fatal("close touched the inbox")
	}
}

func TestNativeCloseChangedIncarnationIsGoneWithoutSignal(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, "an-earlier-process")
	rep := ClosePeer("dev", "worker", true)
	if !rep.Ok || !strings.Contains(rep.Detail, "already gone") || !processRunning(consumer) {
		t.Fatalf("a reused pid was signalled or not reported gone: %+v", rep)
	}
	if len(f.calls) != 1 {
		t.Fatalf("the exact registration must still be disconnected: %+v", f.calls)
	}
}

func TestNativeCloseRefusesWithoutProof(t *testing.T) {
	for name, tc := range map[string]struct {
		kind  string
		setup func(t *testing.T) (*nativeCloseFixture, int)
	}{
		"not a claude process": {"changed incarnation", func(t *testing.T) (*nativeCloseFixture, int) {
			pid := liveProc(t) // a real child whose argv is sleep
			return seedNativeClaude(t, pid, startTokenOf(t, pid)), pid
		}},
		"different loaded session": {"different loaded session", func(t *testing.T) (*nativeCloseFixture, int) {
			pid := fakeClaude(t)
			f := seedNativeClaude(t, pid, startTokenOf(t, pid))
			other := f.binding
			other.SessionID = "33333333-3333-4333-8333-333333333333"
			writeClaudeSessionRegistry(t, other)
			return f, pid
		}},
		"journal of another thread": {"cannot inspect", func(t *testing.T) (*nativeCloseFixture, int) {
			pid := fakeClaude(t)
			f := seedNativeClaude(t, pid, startTokenOf(t, pid))
			f.state.ThreadID = "44444444-4444-4444-8444-444444444444"
			writeCloseJournal(t, f.state)
			return f, pid
		}},
		"no journal": {"cannot inspect", func(t *testing.T) (*nativeCloseFixture, int) {
			pid := fakeClaude(t)
			f := seedNativeClaude(t, pid, startTokenOf(t, pid))
			if err := os.Remove(filepath.Join(DaemonDir(), "connections", f.state.ID+".json")); err != nil {
				t.Fatal(err)
			}
			return f, pid
		}},
		"no recorded consumer": {"no recorded consumer process", func(t *testing.T) (*nativeCloseFixture, int) {
			pid := fakeClaude(t)
			f := seedNativeClaude(t, pid, "")
			return f, pid
		}},
		"consumer is the daemon": {"shared daemon", func(t *testing.T) (*nativeCloseFixture, int) {
			// the daemon pid passes every other check, so only the daemon guard can refuse
			pid := fakeClaude(t)
			f := seedNativeClaude(t, pid, startTokenOf(t, pid))
			m, err := readStartupMeta(filepath.Join(CBUSDir(), "dev", "worker", "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			m.ListenerPid, m.ListenerStart = json.RawMessage(strconv.Itoa(pid)), startTokenOf(t, pid)
			if err := writeMeta(filepath.Join(CBUSDir(), "dev", "worker"), m); err != nil {
				t.Fatal(err)
			}
			return f, pid
		}},
		"consumer is this process's parent": {"this session", func(t *testing.T) (*nativeCloseFixture, int) {
			parent := os.Getppid()
			return seedNativeClaude(t, parent, startTokenOf(t, parent)), parent
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f, pid := tc.setup(t)
			rep := ClosePeer("dev", "worker", true)
			if rep.Ok || !strings.Contains(rep.Detail, tc.kind) || !strings.Contains(rep.Detail, "refusing to signal") || !strings.Contains(rep.Detail, "cbus connection disconnect dev/worker") {
				t.Fatalf("unproven consumer was not refused as %q: %+v", tc.kind, rep)
			}
			if len(f.calls) != 0 || !processRunning(pid) || !processRunning(f.daemon) {
				t.Fatalf("a refusal disconnected or signalled: calls=%d", len(f.calls))
			}
		})
	}
}

func TestNativeCloseRechecksTheIncarnationBeforeTerm(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	prev := preSignalCheck
	preSignalCheck = func(nativeConsumer) (bool, error) { return true, nil }
	t.Cleanup(func() { preSignalCheck = prev })
	if rep := ClosePeer("dev", "worker", true); !strings.Contains(rep.Detail, "already gone") || !processRunning(consumer) || len(f.calls) != 1 {
		t.Fatalf("TERM was sent after the incarnation changed: %+v", rep)
	}
}

func TestNativeCloseRefusesThisSession(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	t.Setenv("CLAUDE_CODE_SESSION_ID", closeTestSession)
	if rep := ClosePeer("dev", "worker", true); rep.Ok || !strings.Contains(rep.Detail, "THIS session") || len(f.calls) != 0 || !processRunning(consumer) {
		t.Fatalf("close of this session was not refused: %+v", rep)
	}
}

func TestNativeCloseDisconnectFailureSendsNoSignal(t *testing.T) {
	consumer := fakeClaude(t)
	seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	nativeDisconnect = func(string, disconnectFence) (string, error) {
		return "", errors.New("dev/worker is now a different registration")
	}
	if rep := ClosePeer("dev", "worker", true); rep.Ok || !strings.Contains(rep.Detail, "no signal sent") || !processRunning(consumer) {
		t.Fatalf("a failed disconnect still signalled: %+v", rep)
	}
	nativeDisconnect = func(string, disconnectFence) (string, error) { return "", nil }
	if rep := ClosePeer("dev", "worker", true); rep.Ok || !processRunning(consumer) {
		t.Fatalf("an unconfirmed disconnect (a daemon that ignores the fence) still signalled: %+v", rep)
	}
}

func TestNativeCloseRevalidatesAfterDisconnect(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	nativeDisconnect = func(_ string, fence disconnectFence) (string, error) {
		// the session reloads while the disconnect is in flight
		other := f.binding
		other.SessionID = "33333333-3333-4333-8333-333333333333"
		writeClaudeSessionRegistry(t, other)
		return fence.ConnectionID, nil
	}
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.HasPrefix(rep.Detail, "disconnected, no signal sent") || !processRunning(consumer) {
		t.Fatalf("a consumer that changed after the disconnect was signalled: %+v", rep)
	}
}

// The legacy walk from ListenerPid would find a live claude owner here; a native
// peer must never reach it.
func TestNativeCloseNeverWalksTheListenerTree(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	needle := filepath.Join(root, "dev", "worker", "inbox.jsonl")
	owner, child := fakeSessionTree(t, "claude", needle)
	seedClosePeerWithStart(t, root, "dev", "worker", closeTestSession, "null", strconv.Itoa(child), startTokenOf(t, child))
	metaPath := filepath.Join(root, "dev", "worker", "meta.json")
	m, err := readStartupMeta(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	m.ConnectionID = "no-such-journal"
	if err := writeMeta(filepath.Dir(metaPath), m); err != nil {
		t.Fatal(err)
	}
	if rep := ClosePeer("dev", "worker", true); rep.Ok || !processRunning(owner) || !processRunning(child) {
		t.Fatalf("a native close signalled through the listener's ancestry: %+v", rep)
	}
}

func TestNativeCloseCodexNeedsTheFreshlyObservedConsumer(t *testing.T) {
	pid := liveProc(t)
	f := seedNativeClaude(t, pid, startTokenOf(t, pid))
	f.state.Harness, f.state.Claude = daemonHarnessCodex, nil
	f.state.RolloutPath = filepath.Join(t.TempDir(), "rollout.jsonl")
	f.state.Consumer = &consumerObservation{State: "online", PID: pid, StartToken: startTokenOf(t, pid)}
	writeCloseJournal(t, f.state)
	if rep := ClosePeer("dev", "worker", true); rep.Ok || !strings.Contains(rep.Detail, "cannot inspect") || len(f.calls) != 0 || !processRunning(pid) {
		t.Fatalf("a cached Codex consumer was signalled without a fresh observation: %+v", rep)
	}
	f.state.Consumer = nil
	writeCloseJournal(t, f.state)
	if rep := ClosePeer("dev", "worker", true); rep.Ok || len(f.calls) != 0 {
		t.Fatalf("a Codex peer with no pinned consumer was not refused: %+v", rep)
	}
}

func fakeCodexCLI(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	// a copy, not a symlink: the kernel names a process after the binary it runs
	if err := exec.Command("cp", "/bin/sleep", bin).Run(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		// a copied platform binary is killed on launch unless it is re-signed
		if err := exec.Command("codesign", "--force", "-s", "-", bin).Run(); err != nil {
			t.Skip("codesign unavailable")
		}
	}
	return startTracked(t, exec.Command(bin, "300"))
}

func seedNativeCodex(t *testing.T, pinned int) *nativeCloseFixture {
	t.Helper()
	f := seedNativeClaude(t, pinned, "")
	f.state.Harness, f.state.Claude = daemonHarnessCodex, nil
	f.state.Consumer = &consumerObservation{State: "online", PID: pinned, StartToken: startTokenOf(t, pinned)}
	writeCloseJournal(t, f.state)
	return f
}

func observeCodexAs(t *testing.T, p consumerProbe) {
	t.Helper()
	prev := closeObserveCodex
	closeObserveCodex = func(context.Context, *ConnectionState) (consumerProbe, error) { return p, nil }
	t.Cleanup(func() { closeObserveCodex = prev })
}

func TestNativeCloseCodexSignalsTheObservedPinnedConsumer(t *testing.T) {
	codex := fakeCodexCLI(t)
	f := seedNativeCodex(t, codex)
	observeCodexAs(t, consumerProbe{State: "online", PID: codex, StartToken: startTokenOf(t, codex)})
	rep := ClosePeer("dev", "worker", false)
	if !rep.Ok || processRunning(codex) || !processRunning(f.daemon) || len(f.calls) != 1 || !f.calls[0].consumerAlive {
		t.Fatalf("the pinned Codex consumer was not closed after a fenced disconnect: %+v calls=%+v", rep, f.calls)
	}
	if f.calls[0].fence.ConsumerPID != codex || f.calls[0].fence.ConsumerStart == "" {
		t.Fatalf("Codex disconnect was not fenced to the consumer: %+v", f.calls[0].fence)
	}
}

func TestNativeCloseCodexRefusesADifferentObservedProcess(t *testing.T) {
	pinned, other := fakeCodexCLI(t), fakeCodexCLI(t)
	f := seedNativeCodex(t, pinned)
	observeCodexAs(t, consumerProbe{State: "online", PID: other, StartToken: startTokenOf(t, other)})
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "changed incarnation") || len(f.calls) != 0 || !processRunning(pinned) || !processRunning(other) {
		t.Fatalf("a replacement Codex process was selected: %+v", rep)
	}
}

func TestNativeCloseCodexShapeIsChecked(t *testing.T) {
	pid := liveProc(t) // real child, argv is sleep
	f := seedNativeCodex(t, pid)
	observeCodexAs(t, consumerProbe{State: "online", PID: pid, StartToken: startTokenOf(t, pid)})
	if rep := ClosePeer("dev", "worker", true); rep.Ok || len(f.calls) != 0 || syscall.Kill(pid, 0) != nil {
		t.Fatalf("a non-codex process was signalled: %+v", rep)
	}
}

// fakeStubbornClaude ignores TERM, so only --force ends it.
func fakeStubbornClaude(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.Symlink("/bin/sh", bin); err != nil {
		t.Fatal(err)
	}
	return startTracked(t, exec.Command(bin, "-c", "trap '' TERM; while :; do sleep 1; done"))
}

func shortTermGrace(t *testing.T) {
	prev := nativeTermGrace
	nativeTermGrace = 300 * time.Millisecond
	t.Cleanup(func() { nativeTermGrace = prev })
}

func TestNativeCloseTermTimeoutNeedsForce(t *testing.T) {
	shortTermGrace(t)
	consumer := fakeStubbornClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	rep := ClosePeer("dev", "worker", false)
	if rep.Ok || !strings.Contains(rep.Detail, "use --force") || strings.Contains(rep.Detail, "surface") || !processRunning(consumer) {
		t.Fatalf("a TERM survivor was escalated or swept without --force: %+v", rep)
	}
	rep = ClosePeer("dev", "worker", true)
	if !rep.Ok || processRunning(consumer) || len(f.calls) != 2 {
		t.Fatalf("--force did not end the pinned consumer: %+v running=%v calls=%d", rep, processRunning(consumer), len(f.calls))
	}
}

func TestNativeCloseForceNeverKillsAChangedIncarnation(t *testing.T) {
	shortTermGrace(t)
	consumer := fakeStubbornClaude(t)
	seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	checks := 0
	prev := preSignalCheck
	preSignalCheck = func(c nativeConsumer) (bool, error) {
		checks++
		if checks == 1 {
			return prev(c)
		}
		return true, nil
	}
	t.Cleanup(func() { preSignalCheck = prev })
	rep := ClosePeer("dev", "worker", true)
	if !strings.Contains(rep.Detail, "not killed") || !processRunning(consumer) || checks != 2 {
		t.Fatalf("--force signalled after the incarnation check failed: %+v (checks %d)", rep, checks)
	}
}

func TestNativeCloseWithoutTTYReportsSurfaceUnknown(t *testing.T) {
	consumer := fakeClaude(t)
	seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	if rep := ClosePeer("dev", "worker", false); !rep.Ok || !strings.HasSuffix(rep.Detail, "surface unknown (no tty)") {
		t.Fatalf("a consumer with no tty did not report an unknown surface: %+v", rep)
	}
}

func TestNativeCloseRefusesThisSessionUnderAnotherAlias(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	dir := filepath.Join(CBUSDir(), "dev", "second")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := readStartupMeta(filepath.Join(CBUSDir(), "dev", "worker", "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Alias = "second"
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", closeTestSession)
	if rep := ClosePeer("dev", "second", true); rep.Ok || !strings.Contains(rep.Detail, "THIS session") || len(f.calls) != 0 || !processRunning(consumer) {
		t.Fatalf("this session was closed through another alias: %+v", rep)
	}
}

func TestNativeCloseCodexWithoutAPinnedConsumerRefuses(t *testing.T) {
	pid := fakeCodexCLI(t)
	f := seedNativeCodex(t, pid)
	f.state.Consumer = nil
	writeCloseJournal(t, f.state)
	observeCodexAs(t, consumerProbe{State: "exited", Detail: "previous CLI process exited"})
	if rep := ClosePeer("dev", "worker", true); rep.Ok || !strings.Contains(rep.Detail, "no recorded consumer process") || len(f.calls) != 0 {
		t.Fatalf("a Codex peer with no pinned consumer was treated as gone: %+v", rep)
	}
}

func TestNativeCloseCodexReprobesTheThreadAfterDisconnect(t *testing.T) {
	codex := fakeCodexCLI(t)
	f := seedNativeCodex(t, codex)
	start := startTokenOf(t, codex)
	disconnected := false
	prevDisconnect := nativeDisconnect
	nativeDisconnect = func(target string, fence disconnectFence) (string, error) {
		disconnected = true
		return prevDisconnect(target, fence)
	}
	prevObserve := closeObserveCodex
	closeObserveCodex = func(context.Context, *ConnectionState) (consumerProbe, error) {
		if disconnected {
			// same pid and start token, but it no longer writes this thread
			return consumerProbe{State: "unknown", Detail: "no exact CLI rollout writer observed"}, nil
		}
		return consumerProbe{State: "online", PID: codex, StartToken: start}, nil
	}
	t.Cleanup(func() { closeObserveCodex = prevObserve })
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.HasPrefix(rep.Detail, "disconnected, no signal sent") || !processRunning(codex) || len(f.calls) != 1 {
		t.Fatalf("a Codex process that left this thread during the disconnect was signalled: %+v", rep)
	}
}

func TestNativeCloseTrulyDeadConsumerIsGoneAndDisconnected(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	start := startTokenOf(t, pid)
	_ = cmd.Process.Kill() // through the handle this test holds
	_, _ = cmd.Process.Wait()
	f := seedNativeClaude(t, pid, start)
	rep := ClosePeer("dev", "worker", false)
	if !rep.Ok || !strings.Contains(rep.Detail, "already gone") || len(f.calls) != 1 {
		t.Fatalf("a dead consumer was not reported gone with its connection disconnected: %+v calls=%d", rep, len(f.calls))
	}
}

func failInspection(t *testing.T, when func() bool) {
	t.Helper()
	prev := inspectIncarnation
	inspectIncarnation = func(c nativeConsumer) (bool, error) {
		if when() {
			return false, errors.New("process table unreadable")
		}
		return prev(c)
	}
	t.Cleanup(func() { inspectIncarnation = prev })
}

func TestNativeCloseInspectionErrorBeforeDisconnectRefuses(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	failInspection(t, func() bool { return true })
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "cannot inspect") || len(f.calls) != 0 || !processRunning(consumer) {
		t.Fatalf("an inspection error was taken as proof of exit: %+v", rep)
	}
}

func TestNativeCloseInspectionErrorAfterTermIsUnconfirmed(t *testing.T) {
	shortTermGrace(t)
	consumer := fakeStubbornClaude(t)
	seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	termSent := false
	prevSignal := signalProcess
	signalProcess = func(pid int, sig syscall.Signal) error {
		termSent = true
		return prevSignal(pid, sig)
	}
	t.Cleanup(func() { signalProcess = prevSignal })
	failInspection(t, func() bool { return termSent })
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "SIGTERM sent to pid") || !strings.Contains(rep.Detail, "outcome unconfirmed") || strings.Contains(rep.Detail, "surface") {
		t.Fatalf("an unconfirmed exit was reported as ended or swept: %+v", rep)
	}
}

func TestNativeSweepNeedsPositiveIdleProof(t *testing.T) {
	consumer := fakeClaude(t)
	seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	prevTTY, prevProbe := nativeTTYOf, nativeTTYProbe
	prevClose := nativeCloseSurface
	nativeTTYOf = func(int) string { return "ttys999" }
	nativeTTYProbe = func(context.Context, string) (string, string, int, error) {
		return "", "", 0, errors.New("ps: permission denied")
	}
	// never a real terminal backend, even when a mutant reaches the close step
	nativeCloseSurface = func(context.Context, string) string { return "surface closed by the test stub" }
	t.Cleanup(func() { nativeTTYOf, nativeTTYProbe, nativeCloseSurface = prevTTY, prevProbe, prevClose })
	if rep := ClosePeer("dev", "worker", false); !rep.Ok || !strings.HasSuffix(rep.Detail, "surface left open (could not confirm idle)") {
		t.Fatalf("a surface was swept without proof that its tty is idle: %+v", rep)
	}
}

func TestTTYIdleState(t *testing.T) {
	const self = 4242
	for _, tc := range []struct {
		name, stdout, stderr string
		code                 int
		err                  error
		want                 string
	}{
		{"proven idle", " 4242 ??\n", "", 0, nil, "idle"},
		{"busy", " 4242 ??\n 777 ttys003\n", "", 0, nil, "busy"},
		{"sysctl failure still exits 0", "", "ps: Failure calling sysctl: Cannot allocate memory", 0, nil, "unknown"},
		{"no self row", "", "", 0, nil, "unknown"},
		{"stderr beside a self row", " 4242 ??\n", "ps: warning", 0, nil, "unknown"},
		{"device gone", "", "ps: /dev/ttys003: No such file or directory", 1, nil, "unknown"},
		{"hangup exit looks like an empty selection", "", "", 1, nil, "unknown"},
		{"nonzero exit", " 4242 ??\n", "", 1, nil, "unknown"},
		{"exec failure", "", "", 0, errors.New("exec: ps not found"), "unknown"},
		{"unparsable row", " 4242 ??\n garbage\n", "", 0, nil, "unknown"},
		{"unrequested row", " 4242 ??\n 777 ttys009\n", "", 0, nil, "unknown"},
	} {
		if got := ttyIdleState(tc.stdout, tc.stderr, tc.code, tc.err, "ttys003", self); got != tc.want {
			t.Errorf("%s: ttyIdleState = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNativeCloseRefusesWhenItsAncestryIsUnreadable(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	failAncestryAtDepth(t)
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "cannot inspect") || len(f.calls) != 0 || !processRunning(consumer) {
		t.Fatalf("close went ahead without proof the consumer is not its own ancestor: %+v", rep)
	}
}

func TestNativeCloseRefusesADaemonThatCannotFence(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	nativeDaemonFences = func() error {
		return errors.New("the running cbus daemon predates fenced disconnects; run cbus daemon restart")
	}
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "cbus daemon restart") || len(f.calls) != 0 || !processRunning(consumer) {
		t.Fatalf("close disconnected through a daemon that cannot fence: %+v", rep)
	}
}

// An older daemon disconnects the alias unfenced and returns no connection ID.
func TestNativeCloseTreatsAnUnechoedFenceAsUnhonoured(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	nativeDisconnect = func(target string, fence disconnectFence) (string, error) {
		f.calls = append(f.calls, disconnectCall{target, fence, true})
		return "", nil
	}
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "may have been disconnected") || strings.Contains(rep.Detail, "surface") || !processRunning(consumer) || len(f.calls) != 1 {
		t.Fatalf("an unhonoured fence was reported wrongly or still signalled: %+v", rep)
	}
}

// The real liveness check, not a stub: an unreadable process is not an exit.
func TestNativeCloseRealInspectionErrorIsNotGone(t *testing.T) {
	consumer := fakeClaude(t)
	f := seedNativeClaude(t, consumer, startTokenOf(t, consumer))
	prev := ownerStartTime
	ownerStartTime = func(int) (string, error) { return "", errors.New("sysctl: cannot allocate memory") }
	t.Cleanup(func() { ownerStartTime = prev })
	if gone, err := incarnationGone(nativeConsumer{consumer, f.binding.Endpoint.StartToken}); gone || err == nil {
		t.Fatalf("an inspection error read as gone=%v err=%v", gone, err)
	}
	rep := ClosePeer("dev", "worker", true)
	if rep.Ok || !strings.Contains(rep.Detail, "cannot inspect") || len(f.calls) != 0 || !processRunning(consumer) {
		t.Fatalf("an unreadable consumer was disconnected or signalled: %+v", rep)
	}
}
