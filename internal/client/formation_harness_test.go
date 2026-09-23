package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormationCodexGatePrecedesMissingTranscriptFallback(t *testing.T) {
	for _, mode := range []string{ModeTemplate, ModeResume, ModeFork} {
		for _, sid := range []string{"", "gone-codex-thread"} {
			p := peer("advisor", func(p *FormationPeer) {
				p.Harness = "codex"
				p.Mode = mode
				p.SessionID = sid
				p.Origin = OriginJoined
				p.OnStale = OnStaleTemplate
			})
			w := &PlanWorld{Host: "test-host", HasTranscript: func(string, string) bool { t.Fatal("Codex gate queried Claude transcripts"); return false }}
			got := BuildPlan(formationOf(p), w, nil).Peers[0]
			if got.Action != ActionRefuse || !strings.Contains(got.Reason, "harness=codex") || got.Degraded {
				t.Fatalf("mode=%s sid=%s plan=%+v", mode, sid, got)
			}
		}
	}
}

func TestFormationCodexNeverLaunchesOrComposesClaudeBootstrap(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	p := peer("advisor", func(p *FormationPeer) {
		p.Harness = "codex"
		p.Mode = ModeResume
		p.Origin = OriginJoined
		p.SessionID = connectIdentityThread
	})
	f := formationOf(p)
	f.AnchorAlias = p.Alias
	rec := &recForker{}
	if _, err := launchPeer(f, PeerPlan{Peer: &p, Action: ActionTemplate}, "ch/other", "nonce", "", rec, "", false); err == nil || !strings.Contains(err.Error(), "harness=codex") {
		t.Fatalf("launch err=%v", err)
	}
	if len(rec.specs) != 0 || fileExists(filepath.Join(CBUSDir(), f.Channel, p.Alias, "meta.json")) {
		t.Fatal("refusal launched or reserved a Claude peer")
	}
	if argv := peerLaunchArgv(PeerPlan{Peer: &p, Action: ActionResume}, "prompt", ""); len(argv) != 0 {
		t.Fatalf("unsupported argv=%q", argv)
	}
	w := &PlanWorld{Host: "test-host", HasTranscript: func(string, string) bool { t.Fatal("Claude transcript lookup"); return false }}
	if _, _, err := resumeAnchorWorld(f, "", rec, w); err == nil || !strings.Contains(err.Error(), "harness=codex") {
		t.Fatalf("resume err=%v", err)
	}
	if _, err := BootstrapPeer(f, p.Alias, ""); err == nil || !strings.Contains(err.Error(), "harness=codex") {
		t.Fatalf("bootstrap err=%v", err)
	}
	if state, _ := p.SidState(); state != SidUnchecked {
		t.Fatalf("Codex transcript was classified with Claude store: %v", state)
	}
}

func TestFormationHarnessLegacyCompatibilityAndKnownConflict(t *testing.T) {
	legacy := peer("a", func(p *FormationPeer) { p.Mode = ModeTemplate })
	w := &PlanWorld{Host: "test-host"}
	if got := BuildPlan(formationOf(legacy), w, nil).Peers[0]; got.Action != ActionTemplate {
		t.Fatalf("legacy plan=%+v", got)
	}
	for _, listening := range []bool{false, true} {
		w.Roster = []RosterPeer{{Alias: "a", Harness: "codex", Listening: listening}}
		if got := BuildPlan(formationOf(legacy), w, nil).Peers[0]; got.Action != ActionRefuse {
			t.Fatalf("known Codex silently treated as Claude: %+v", got)
		}
	}
	backend := legacy
	backend.CodexBackend = &FormationCodexBackend{Home: "/codex"}
	w.Roster = nil
	if got := BuildPlan(formationOf(backend), w, nil).Peers[0]; got.Action != ActionRefuse {
		t.Fatalf("backend-only Codex bypassed gate: %+v", got)
	}
}

func TestSaveFormationCapturesCodexHarnessAndBackend(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", connectIdentityThread)
	plantPeer(t, "native", "advisor", connectIdentityThread)
	dir := filepath.Join(root, "native", "advisor")
	m := peerMeta{Alias: "advisor", Channel: "native", SessionID: connectIdentityThread, Harness: "codex", ConnectionID: "connection-1", ListenerPid: jsonNull, OwnerPid: jsonNull, Host: thisHost()}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	c := ConnectionState{ID: m.ConnectionID, Channel: m.Channel, Alias: m.Alias, ThreadID: m.SessionID, Config: CodexQueueConfig{Binary: "/bin/codex", Home: "/codex", SQLiteHome: "/storage/profile", UserHome: "/user", BindingSource: "runtime-open-queue"}, Consumer: &consumerObservation{State: "exited"}}
	if err := os.MkdirAll(filepath.Join(DaemonDir(), "connections"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	path := filepath.Join(DaemonDir(), "connections", c.ID+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _, err := SaveFormation("native", "native", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := f.Peers[0]
	if got.Harness != "codex" || got.CodexBackend == nil || got.CodexBackend.SQLiteHome != "/storage/profile" {
		t.Fatalf("captured=%+v", got)
	}
	back, err := LoadFormation("native")
	if err != nil {
		t.Fatal(err)
	}
	if back.Peers[0].CodexBackend == nil || back.Peers[0].CodexBackend.Home != "/codex" {
		t.Fatalf("backend lost on roundtrip: %+v", back.Peers[0])
	}
	c.ThreadID = "different-thread"
	b, _ = json.Marshal(c)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SaveFormation("native", "native", nil); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("mismatched backend saved: %v", err)
	}
}

func TestCaptureFormationHarnessRefreshCannotCarryWrongBackend(t *testing.T) {
	p := FormationPeer{Harness: "claude", SessionID: "old", CodexBackend: nil}
	capturePeer(&p, RosterPeer{Harness: "codex", SessionID: "new", CodexBackend: &FormationCodexBackend{Home: "/new"}}, &SaveReport{})
	if p.Harness != "codex" || p.CodexBackend.Home != "/new" {
		t.Fatalf("identity not refreshed: %+v", p)
	}
	capturePeer(&p, RosterPeer{Harness: "claude", SessionID: "third"}, &SaveReport{})
	if p.Harness != "claude" || p.CodexBackend != nil {
		t.Fatalf("stale backend retained: %+v", p)
	}
}
