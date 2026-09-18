package main

import (
	"slices"
	"strings"
	"testing"
)

func TestDaemonEnvironmentDropsSessionCapabilityAndIdentity(t *testing.T) {
	keep := []string{"PATH=/bin", "CBUS_DIR=/isolated/bus", "HOME=/isolated/home", "CODEX_HOME=/isolated/codex"}
	env := append([]string{}, keep...)
	for _, key := range []string{"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_FUTURE_KEY", "CLAUDE_CODE_SESSION_ID", "CLAUDE_PID", "CLAUDE_ENV_FILE", "CBUS_SESSION_ID", "CBUS_CHANNEL", "CBUS_ALIAS", "CBUS_HARNESS", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "GROK_SESSION_ID"} {
		env = append(env, key+"=fixture-private-value")
	}
	before := append([]string{}, env...)
	got := daemonEnvironment(env)
	if !slices.Equal(got, keep) || strings.Contains(strings.Join(got, "\n"), "fixture-private-value") {
		t.Fatal("detached daemon inherited a session capability or identity")
	}
	if !slices.Equal(env, before) {
		t.Fatal("scrubber mutated caller environment")
	}
}
