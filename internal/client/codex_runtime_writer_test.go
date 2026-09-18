package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConsumerRolloutRequiresExactRecordedUUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-guessed-from-filename.jsonl")
	for _, tc := range []struct {
		input string
		ok    bool
	}{
		{`{"type":"session_meta","payload":{"id":"` + daemonTestThread + `"}}` + "\n", true},
		{`{"type":"session_meta","payload":{"id":"other"}}` + "\n", false},
		{`{"type":"event_msg","payload":{"id":"` + daemonTestThread + `"}}` + "\n", false},
		{`{"type":"session_meta","payload":{"id":"` + daemonTestThread + `"}}`, false},
	} {
		if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		f, err := openConsumerRollout(path, daemonTestThread)
		if f != nil {
			f.Close()
		}
		if (err == nil) != tc.ok {
			t.Fatalf("input=%s error=%v", tc.input, err)
		}
	}
}

func TestConsumerProbeDoesNotTrustCapturedPIDWithoutRollout(t *testing.T) {
	c := &ConnectionState{ThreadID: daemonTestThread, Config: CodexQueueConfig{RuntimePID: os.Getpid(), RuntimeStartToken: selfStart(t)}}
	p, err := observeCodexConsumer(context.Background(), c)
	if err == nil || p.State != "unknown" {
		t.Fatalf("unproven thread owner accepted: %+v %v", p, err)
	}
}

func TestCodexOwnerProcessWitness(t *testing.T) {
	start := selfStart(t)
	if exited, err := codexOwnerExited(os.Getpid(), start); err != nil || exited {
		t.Fatalf("current process=%v %v", exited, err)
	}
	if exited, err := codexOwnerExited(os.Getpid(), start+"-reused"); err != nil || !exited {
		t.Fatalf("PID reuse=%v %v", exited, err)
	}
}

func TestConsumerRejectsNonInteractiveCodexProcesses(t *testing.T) {
	for _, argv := range []string{"", "codex app-server --stdio", "codex exec prompt", "codex exec-server", "codex desktop", "codex -c model=probe app-server", "codex --profile app-server exec prompt", "codex -c", "codex --future-option value", "codex --remote unix:///tmp/a.sock", "codex review", "codex e prompt"} {
		if interactiveCodexProcess(argv) {
			t.Fatalf("accepted %q", argv)
		}
	}
	for _, argv := range []string{"codex", "codex resume " + daemonTestThread, "codex resume --last", "codex explain exec behavior", "codex --no-alt-screen explain app-server behavior", "codex --profile app-server explain exec behavior", "codex -c model=exec resume " + daemonTestThread, "codex --profile=exec fork --last", "codex -- exec app-server"} {
		if !interactiveCodexProcess(argv) {
			t.Fatalf("rejected %q", argv)
		}
	}
}
