package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
)

// clearSessionEnv blanks the whole $*_SESSION_ID chain so a test drives identity purely
// through the --session-id flag (no ambient session leaks in from the dev/CI shell).
func clearSessionEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CBUS_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "GROK_SESSION_ID"} {
		t.Setenv(k, "")
	}
}

// TestSessionIDFlagJoinLeave exercises the flag through the real CLI entrypoints: with no
// session env, `join <ch> <alias> --session-id S9` writes sessionId S9 into the peer's
// meta, and `leave --session-id S9` resolves that registration and removes it. The
// override must not leak past either call.
func TestSessionIDFlagJoinLeave(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	clearSessionEnv(t)

	out := captureStdout(t, func() {
		if rc := runJoin([]string{"harnessch", "coder", "--session-id", "S9"}); rc != 0 {
			t.Fatalf("runJoin rc=%d", rc)
		}
	})
	if !strings.Contains(out, `joined channel "harnessch" as "coder" (session S9)`) {
		t.Errorf("join output should report session S9: %q", out)
	}

	var m struct {
		SessionID string `json:"sessionId"`
	}
	b, err := os.ReadFile(filepath.Join(root, "harnessch", "coder", "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.SessionID != "S9" {
		t.Fatalf("meta sessionId = %q, want S9 (flag not threaded to Join)", m.SessionID)
	}
	if client.SessionID() != "" {
		t.Errorf("override leaked after join: %q", client.SessionID())
	}

	lout := captureStdout(t, func() {
		if rc := runLeave([]string{"--session-id", "S9"}); rc != 0 {
			t.Fatalf("runLeave rc=%d", rc)
		}
	})
	if !strings.Contains(lout, "left harnessch/coder") {
		t.Errorf("leave --session-id should resolve+leave the S9 registration: %q", lout)
	}
	if _, err := os.Stat(filepath.Join(root, "harnessch", "coder")); !os.IsNotExist(err) {
		t.Errorf("registration dir should be gone after leave; stat err=%v", err)
	}
	if client.SessionID() != "" {
		t.Errorf("override leaked after leave: %q", client.SessionID())
	}
}

// TestSessionIDFlagRenameResolves: rename --session-id resolves the flag session's
// registration and moves it, with no ambient env session.
func TestSessionIDFlagRenameResolves(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	clearSessionEnv(t)

	if rc := runJoin([]string{"ch", "old", "--session-id", "S7"}); rc != 0 {
		t.Fatalf("setup join rc=%d", rc)
	}
	out := captureStdout(t, func() {
		if rc := runRename([]string{"newname", "--session-id", "S7"}); rc != 0 {
			t.Fatalf("runRename rc=%d", rc)
		}
	})
	if !strings.Contains(out, "renamed ch/old -> ch/newname") {
		t.Errorf("rename output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "ch", "newname", "meta.json")); err != nil {
		t.Errorf("renamed registration missing: %v", err)
	}
}

// TestSessionIDFlagMissingValue: a trailing --session-id with no value is an error, not a
// silent empty override.
func TestSessionIDFlagMissingValue(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearSessionEnv(t)
	if rc := runJoin([]string{"ch", "--session-id"}); rc == 0 {
		t.Error("join --session-id with no value must fail")
	}
}
