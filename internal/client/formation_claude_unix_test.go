//go:build darwin || linux

package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func formationClaudeFixture(t *testing.T) (*ConnectionState, string) {
	t.Helper()
	setupStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", claudeTestSession)
	q, _ := claudeQueueFixture(t)
	cfg := q.cfg
	cfg.Binding.UserHome = t.TempDir()
	cfg.Binding.ConfigHome = filepath.Join(cfg.Binding.UserHome, ".ccs", "instances", "worker")
	cfg.Binding.Cwd = filepath.Join(cfg.Binding.UserHome, "runtime-work")
	writeClaudeSessionRegistry(t, cfg.Binding)
	// Credentials deliberately do not exist in this daemon root: formation
	// inspection must never need authentication or send anything to the socket.
	c := &ConnectionState{ID: "claude-formation", Harness: "claude", Channel: "native", Alias: "worker",
		ThreadID: claudeTestSession, Claude: &cfg, State: "socket-ready", Consumer: &consumerObservation{State: "online"}}
	dir := filepath.Join(CBUSDir(), c.Channel, c.Alias)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	m := peerMeta{Alias: c.Alias, Channel: c.Channel, SessionID: c.ThreadID, Harness: c.Harness, ConnectionID: c.ID,
		Cwd: "/stale-metadata", Profile: "stale-profile", Origin: OriginFresh, Model: "sonnet", Host: thisHost(),
		ListenerPid: json.RawMessage(fmt.Sprint(os.Getpid())), ListenerStart: cfg.Binding.Endpoint.StartToken, OwnerPid: jsonNull}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	writeFormationClaudeJournal(t, c)
	return c, dir
}

func writeFormationClaudeJournal(t *testing.T, c *ConnectionState) {
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

func TestFormationClaudeCapturesBindingAndPreservesAuthoredRole(t *testing.T) {
	c, _ := formationClaudeFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), ".ccs", "instances", "saver"))
	f, _, err := SaveFormation("native", "native", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &f.Peers[0]
	if p.Harness != "claude" || p.CodexBackend != nil || p.Cwd != c.Claude.Binding.Cwd || p.Profile != "worker" || p.SessionID != c.ThreadID {
		t.Fatalf("captured incorrect native identity: %+v", p)
	}
	p.Role, p.Rolefile, p.Model, p.Origin = strPtr("Own the exact caller contract"), "roles/reviewer.md@recorded", "chosen-model", OriginJoined
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	// A known default config clears a stale profile; it is not an unknown
	// legacy blank. Hand-authored role and birth decisions remain untouched.
	c.Claude.Binding.ConfigHome = filepath.Join(c.Claude.Binding.UserHome, ".claude")
	writeClaudeSessionRegistry(t, c.Claude.Binding)
	writeFormationClaudeJournal(t, c)
	if _, _, err := SaveFormation("native", "native", nil); err != nil {
		t.Fatal(err)
	}
	back, err := LoadFormation("native")
	if err != nil {
		t.Fatal(err)
	}
	p = &back.Peers[0]
	if p.Profile != "" || p.Role == nil || *p.Role != "Own the exact caller contract" || p.Rolefile != "roles/reviewer.md@recorded" || p.Model != "chosen-model" || p.Origin != OriginJoined {
		t.Fatalf("refresh rewrote authored identity or retained stale profile: %+v", p)
	}
	if err := formationHarnessRefusal(p); err != nil {
		t.Fatalf("Claude save became a Codex launch refusal: %v", err)
	}
}

func TestFormationClaudeRejectsMismatchedManagedIdentity(t *testing.T) {
	c, _ := formationClaudeFixture(t)
	for name, mutate := range map[string]func(*ConnectionState){
		"session": func(v *ConnectionState) { v.ThreadID = "different" },
		"harness": func(v *ConnectionState) { v.Harness = "codex" },
		"binding": func(v *ConnectionState) { v.Claude.Binding.SessionID = "different" },
		"missing": func(v *ConnectionState) { v.Claude = nil },
		"relay":   func(v *ConnectionState) { v.Relay = &RelayConfig{Host: "other"} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := cloneConnection(c)
			mutate(bad)
			writeFormationClaudeJournal(t, bad)
			if _, _, err := SaveFormation("native", "native", nil); err == nil {
				t.Fatal("saved a different or incomplete managed identity")
			}
		})
	}
}

func TestFormationClaudeCustomConfigRefusesWithoutRewritingSavedIdentity(t *testing.T) {
	c, _ := formationClaudeFixture(t)
	if _, _, err := SaveFormation("native", "native", nil); err != nil {
		t.Fatal(err)
	}
	path, _ := FormationPath("native")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Claude.Binding.ConfigHome = filepath.Join(c.Claude.Binding.UserHome, "custom-config")
	writeFormationClaudeJournal(t, c)
	if _, _, err := SaveFormation("native", "native", nil); err == nil || !strings.Contains(err.Error(), "custom config root") {
		t.Fatalf("unrepresented config root must refuse: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("refusal rewrote the previously saved formation")
	}
	if _, err := GatherPlanWorld(c.Channel); err == nil {
		t.Fatal("automatic formation launch accepted unrepresented native config")
	}
}

func TestFormationClaudeLivenessIsTheConsumerNotDaemon(t *testing.T) {
	c, dir := formationClaudeFixture(t)
	check := func(listening, claimed bool) {
		t.Helper()
		rows, err := ChannelRoster(c.Channel)
		if err != nil || len(rows) != 1 || rows[0].Listening != listening {
			t.Fatalf("roster=%+v err=%v; want listening=%v", rows, err, listening)
		}
		_, got := liveSids()[c.ThreadID]
		if got != claimed {
			t.Fatalf("session claimed=%v; want %v", got, claimed)
		}
	}
	check(true, true)
	registry := writeClaudeSessionRegistry(t, c.Claude.Binding)
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	check(false, true) // Missing evidence is unknown, not permission to resume.
	other := c.Claude.Binding
	other.SessionID = "00000000-0000-4000-8000-000000000001"
	writeClaudeSessionRegistry(t, other)
	check(false, false) // /clear keeps the process, but releases the old session.
	writeClaudeSessionRegistry(t, c.Claude.Binding)
	c.State = "disconnected"
	writeFormationClaudeJournal(t, c)
	check(false, true) // Disconnecting receive does not stop the loaded model.
	c.State = "socket-ready"
	c.Claude.Binding.Endpoint.StartToken += "-old-incarnation"
	writeFormationClaudeJournal(t, c)
	if !MetaListenerAlive(filepath.Join(dir, "meta.json")) {
		t.Fatal("fixture daemon must remain alive after model exit")
	}
	check(false, false)
	// Corrupt state cannot prove the old transcript is free for another writer.
	if err := os.WriteFile(filepath.Join(DaemonDir(), "connections", c.ID+".json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := liveSids()[c.ThreadID]; !ok {
		t.Fatal("unreadable managed state freed a possibly active transcript")
	}
	if _, err := ChannelRoster(c.Channel); err == nil || !strings.Contains(err.Error(), "managed backend") {
		t.Fatalf("malformed state should block a save: %v", err)
	}
}
