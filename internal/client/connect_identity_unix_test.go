//go:build darwin || linux

package client

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNativeConnectUsesNearestHarnessAndKeepsTokenPrivate(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "fixture-private-token")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "inherited-claude-session")
	t.Setenv("CODEX_THREAD_ID", "inherited-codex-session")
	codexCalls, claudeCalls := 0, 0
	codex := func(opts CodexConnectOptions) (CodexQueueConfig, string, error) {
		codexCalls++
		return CodexQueueConfig{SQLiteHome: opts.SQLiteHome}, connectIdentityThread, nil
	}
	claude := func() (ClaudeConnectBinding, error) {
		claudeCalls++
		return ClaudeConnectBinding{SessionID: daemonTestThread}, nil
	}
	for _, harness := range []string{"codex", "claude"} {
		req, token, err := nativeConnectIdentityForHarness(harness, CodexConnectOptions{}, codex, claude)
		if err != nil || req.Harness != harness || req.Protocol != DaemonProtocolVersion {
			t.Fatalf("dispatch: %+v %v", req, err)
		}
		if harness == "claude" && (token != "fixture-private-token" || req.Claude == nil || req.ThreadID != daemonTestThread) {
			t.Fatal("Claude proof lost")
		}
		if harness == "codex" && (token != "" || req.Claude != nil || req.ThreadID != connectIdentityThread) {
			t.Fatal("inherited Claude capability reached Codex")
		}
		encoded, _ := json.Marshal(req)
		if strings.Contains(string(encoded), "fixture-private-token") {
			t.Fatal("public request contains capability token")
		}
	}
	if codexCalls != 1 || claudeCalls != 1 {
		t.Fatal("dispatch invoked another harness resolver")
	}
	for _, harness := range []string{"", "opencode", "grok"} {
		if _, token, err := nativeConnectIdentityForHarness(harness, CodexConnectOptions{}, codex, claude); err == nil || token != "" {
			t.Fatal("unsupported harness fell back to inherited identity")
		}
	}
	if codexCalls != 1 || claudeCalls != 1 {
		t.Fatal("unsupported harness resolved identity")
	}
}

func TestNativeConnectFailsBeforeReturningInvalidCapability(t *testing.T) {
	codex := func(opts CodexConnectOptions) (CodexQueueConfig, string, error) {
		return CodexQueueConfig{SQLiteHome: opts.SQLiteHome}, connectIdentityThread, nil
	}
	claudeCalls := 0
	claude := func() (ClaudeConnectBinding, error) {
		claudeCalls++
		return ClaudeConnectBinding{SessionID: daemonTestThread}, nil
	}
	if _, _, err := nativeConnectIdentityForHarness("claude", CodexConnectOptions{SQLiteHome: "/codex-store"}, codex, claude); err == nil || claudeCalls != 0 {
		t.Fatal("Codex-only override reached Claude binding")
	}
	if req, _, err := nativeConnectIdentityForHarness("codex", CodexConnectOptions{SQLiteHome: "/codex-store"}, codex, claude); err != nil || req.Config.SQLiteHome != "/codex-store" {
		t.Fatal("Codex option changed")
	}
	t.Setenv("CODEX_THREAD_ID", connectIdentityThread)
	if _, _, err := nativeConnectIdentityForHarness("", CodexConnectOptions{}, codex, claude); err == nil {
		t.Fatal("inherited exact UUID alone selected an adapter")
	}
	if req, token, err := nativeConnectIdentityForHarness("", CodexConnectOptions{SQLiteHome: "/codex-store"}, codex, claude); err != nil || req.Harness != "codex" || req.Config.SQLiteHome != "/codex-store" || token != "" {
		t.Fatal("explicit detached Codex binding lost")
	}
	t.Setenv("CODEX_THREAD_ID", "not-a-thread-uuid")
	if _, _, err := nativeConnectIdentityForHarness("", CodexConnectOptions{SQLiteHome: "/codex-store"}, codex, claude); err == nil {
		t.Fatal("detached binding guessed an inexact thread")
	}
	for _, token := range []string{"", "private\ncapability", strings.Repeat("x", claudeCredentialMaxBytes+1)} {
		t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", token)
		if _, secret, err := nativeConnectIdentityForHarness("claude", CodexConnectOptions{}, codex, claude); err == nil || secret != "" {
			t.Fatal("invalid capability returned")
		}
	}
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "private-capability")
	if _, token, err := nativeConnectIdentityForHarness("claude", CodexConnectOptions{}, codex, func() (ClaudeConnectBinding, error) { return ClaudeConnectBinding{}, errors.New("identity failed") }); err == nil || token != "" {
		t.Fatal("capability escaped failed identity capture")
	}
}

func TestNativeClaudePathWinsBeforeOuterHarness(t *testing.T) {
	for _, first := range []procRecord{{PPid: 30, Comm: "/home/user/.local/share/claude/versions/2.1.277"}, {PPid: 30, Comm: "2.1.277", Argv: "/home/user/.local/share/claude/versions/2.1.277 --resume abc"}} {
		records := map[int]procRecord{10: {PPid: 20, Comm: "zsh"}, 20: first, 30: {PPid: 1, Comm: "codex"}}
		lookup := func(pid int) (procRecord, bool) { r, ok := records[pid]; return r, ok }
		if got := harnessWalk(10, 1, lookup); got != "claude" {
			t.Fatalf("native Claude crossed into outer harness: %q", got)
		}
		records[10] = procRecord{PPid: 20, Comm: "opencode"}
		if got := harnessWalk(10, 1, lookup); got != "opencode" {
			t.Fatalf("nearer harness skipped: %q", got)
		}
	}
}
