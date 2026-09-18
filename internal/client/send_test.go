package client

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"claudebus/internal/core"
)

func TestLocalSendInboxOpenFailure(t *testing.T) {
	for _, tt := range []struct {
		name  string
		pid   string
		force bool
	}{
		{name: "never armed", pid: "null"},
		{name: "forced dead listener", pid: "999999", force: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := setupStore(t)
			seedPeerPid(t, root, "dev", "target", "OTHER", tt.pid)
			path := filepath.Join(root, "dev", "target", "inbox.jsonl")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}

			resolved, from, warn, err := LocalSend("dev/target", "dev/sender", tt.force, "hello")
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Op != "open" || pathErr.Path != path {
				t.Fatalf("LocalSend must preserve the inbox open failure: %v", err)
			}
			if resolved != "" || from != "" || warn {
				t.Errorf("failed enqueue returned success details: %q, %q, %v", resolved, from, warn)
			}
		})
	}
}

func TestLocalSendInboxWriteFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /dev/full to exercise a real write failure")
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skipf("/dev/full unavailable: %v", err)
	}
	root := setupStore(t)
	seedPeer(t, root, "dev", "target", "OTHER")
	path := filepath.Join(root, "dev", "target", "inbox.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/full", path); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := LocalSend("dev/target", "dev/sender", false, "hello")
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "write" || pathErr.Path != path {
		t.Fatalf("LocalSend must preserve the inbox write failure: %v", err)
	}
}

func TestLocalSendNeverArmedAccepted(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "target", "OTHER") // never-armed -> accepted
	seedPeer(t, root, "dev", "me", "SID")       // this session's reg in the target channel
	tgt, from, warn, err := LocalSend("dev/target", "", false, "hello")
	if err != nil || warn || tgt != "dev/target" {
		t.Fatalf("LocalSend = %q,%q,%v,%v", tgt, from, warn, err)
	}
	if from != "dev/me" {
		t.Errorf("from = %q, want dev/me (own reg in the target channel)", from)
	}
	m, _ := core.DecodeMessage([]byte(strings.TrimSpace(inbox(t, root, "dev", "target"))))
	if m.From != "dev/me" || m.To != "dev/target" || m.Text != "hello" {
		t.Errorf("appended line = %+v", m)
	}
}

func TestLocalSendGate(t *testing.T) {
	root := setupStore(t)
	seedPeerPid(t, root, "dev", "target", "OTHER", "999999") // armed + dead
	if _, _, _, err := LocalSend("dev/target", "x", false, "hi"); err == nil {
		t.Error("a dead ex-listener must be refused without --force")
	}
	_, _, warn, err := LocalSend("dev/target", "x", true, "hi")
	if err != nil || !warn {
		t.Errorf("--force should queue past a dead listener with a warning: warn=%v err=%v", warn, err)
	}
}

func TestLocalSendLiveAccepted(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "target", "OTHER")
	seedPeerArmed(t, root, "dev", "target", "OTHER", liveProc(t)) // a real live process to arm against
	if _, _, warn, err := LocalSend("dev/target", "x", false, "hi"); err != nil || warn {
		t.Errorf("a live listener should accept without a warning: warn=%v err=%v", warn, err)
	}
}

func TestLocalSendFromFallbackUnroutable(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CBUS_ALIAS", "")
	t.Setenv("CBUS_CHANNEL", "")
	seedPeer(t, root, "dev", "target", "OTHER") // this session has no reg -> host-pid fallback
	_, from, _, err := LocalSend("dev/target", "", false, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(from, "/") {
		t.Errorf("fallback from should be the unroutable <host>-<pid>: %q", from)
	}
}

func TestLocalSendWrapperSender(t *testing.T) {
	for _, test := range []struct {
		name, channel, alias, explicit, want string
	}{
		{name: "wrapper reply", channel: "source", alias: "codex", want: "source/codex"},
		{name: "explicit sender", channel: "source", alias: "codex", explicit: "override/peer", want: "override/peer"},
		{name: "legacy qualified alias", channel: "source", alias: "legacy/peer", want: "legacy/peer"},
		{name: "legacy bare alias", alias: "legacy", want: "legacy"},
		{name: "invalid channel preserves alias", channel: "../bad", alias: "legacy", want: "legacy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := setupStore(t)
			for _, key := range []string{"CLAUDE_CODE_SESSION_ID", "CBUS_SESSION_ID", "GROK_SESSION_ID", "CODEX_THREAD_ID"} {
				t.Setenv(key, "")
			}
			t.Setenv("CBUS_CHANNEL", test.channel)
			t.Setenv("CBUS_ALIAS", test.alias)
			seedPeer(t, root, "target", "receiver", "OTHER")
			_, from, _, err := LocalSend("target/receiver", test.explicit, false, "reply")
			if err != nil || from != test.want {
				t.Fatalf("sender = %q, %v; want %q", from, err, test.want)
			}
			message, err := core.DecodeMessage([]byte(strings.TrimSpace(inbox(t, root, "target", "receiver"))))
			if err != nil || message.From != test.want {
				t.Fatalf("inbox sender = %+v, %v", message, err)
			}
		})
	}
}

func TestLocalSendSessionIdentityPrecedesWrapperFallback(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CBUS_CHANNEL", "wrapper")
	t.Setenv("CBUS_ALIAS", "fallback")
	seedPeer(t, root, "dev", "target", "OTHER")
	seedPeer(t, root, "dev", "registered", "SID")
	_, from, _, err := LocalSend("dev/target", "", false, "hello")
	if err != nil || from != "dev/registered" {
		t.Fatalf("registered sender must take precedence: %q, %v", from, err)
	}
}

func TestLocalSendMaxLineRejectNotTruncate(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "target", "OTHER")
	big := strings.Repeat("z", core.MaxMessageBytes) // 1MiB text -> line exceeds the cap
	if _, _, _, err := LocalSend("dev/target", "x", false, big); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("an oversize message must be rejected: %v", err)
	}
	if inbox(t, root, "dev", "target") != "" {
		t.Error("a rejected message must NOT be appended (reject, never truncate)")
	}
}
