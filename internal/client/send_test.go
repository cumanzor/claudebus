package client

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/core"
)

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
	inboxPath := filepath.Join(root, "dev", "target", "inbox.jsonl")
	live := exec.Command("tail", "-f", inboxPath) // a real live process to arm against
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Process.Kill() }()
	seedPeerArmed(t, root, "dev", "target", "OTHER", live.Process.Pid)
	if _, _, warn, err := LocalSend("dev/target", "x", false, "hi"); err != nil || warn {
		t.Errorf("a live listener should accept without a warning: warn=%v err=%v", warn, err)
	}
}

func TestLocalSendFromFallbackUnroutable(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "target", "OTHER") // this session has no reg -> host-pid fallback
	_, from, _, err := LocalSend("dev/target", "", false, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(from, "/") {
		t.Errorf("fallback from should be the unroutable <host>-<pid>: %q", from)
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
