package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantPeer writes a peer's meta.json the way the store does, so save reads a real
// roster rather than a mock.
func plantPeer(t *testing.T, ch, alias, sid string) {
	t.Helper()
	dir := filepath.Join(CBUSDir(), ch, alias)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := peerMeta{
		Alias: alias, Channel: ch, SessionID: sid,
		Cwd: "/Users/dev/repos/AI/claudebus", ListenerPid: jsonNull, OwnerPid: jsonNull,
		Host: ShortHostname(), TS: Now(), LastActivity: Now(),
	}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
}

func TestChannelRoster(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantPeer(t, "roles", "coder", "sid-coder")
	plantPeer(t, "roles", "orchestrator", "sid-orch")
	// noise that must not become a peer
	if err := os.MkdirAll(filepath.Join(CBUSDir(), "roles", ".reap.123.x"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ChannelRoster("roles")
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	if len(got) != 2 || got[0].Alias != "coder" || got[1].Alias != "orchestrator" {
		t.Fatalf("roster = %+v (want coder, orchestrator, sorted)", got)
	}
	if got[0].SessionID != "sid-coder" || got[0].Machine != ShortHostname() {
		t.Errorf("captured facts wrong: %+v", got[0])
	}
	if _, err := ChannelRoster("ghost"); err == nil {
		t.Error("a channel that does not exist must error")
	}
	if _, err := ChannelRoster("bad/name"); err == nil {
		t.Error("a bad channel name must be refused")
	}
}

// TestChannelRosterKeepsDeadPeers is the property the whole feature rests on: a
// formation is saved when an effort pauses, i.e. exactly when its peers are dead.
// A roster that pruned them would empty the file it was asked to record.
func TestChannelRosterKeepsDeadPeers(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantPeer(t, "un", "coder", "sid-1") // listenerPid null => not listening
	got, err := ChannelRoster("un")
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a dead peer must still be captured, got %+v", got)
	}
	if got[0].Listening {
		t.Error("a never-armed peer should not read as listening")
	}
}

func TestSaveFormationNew(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "roles", "coder", "sid-coder")
	plantPeer(t, "roles", "orchestrator", "sid-orch")

	f, rep, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !rep.New || len(rep.Added) != 2 || len(rep.Updated) != 0 || len(rep.Kept) != 0 {
		t.Errorf("report = %+v", rep)
	}
	if f.Schema != FormationSchema || f.Channel != "roles" || f.Name != "roles" {
		t.Errorf("envelope = %+v", f)
	}
	if f.SavedAt == "" {
		t.Error("savedAt not stamped")
	}
	// the saving session is a peer here, so savedBy is its address and it anchors
	if f.SavedBy != "roles/orchestrator" {
		t.Errorf("savedBy = %q want roles/orchestrator", f.SavedBy)
	}
	if f.AnchorAlias != "orchestrator" {
		t.Errorf("anchorAlias = %q want orchestrator (the saving session)", f.AnchorAlias)
	}
	var coder *FormationPeer
	for i := range f.Peers {
		if f.Peers[i].Alias == "coder" {
			coder = &f.Peers[i]
		}
	}
	if coder == nil {
		t.Fatal("coder not captured")
	}
	// captured
	if coder.SessionID != "sid-coder" || coder.Machine != ShortHostname() || coder.Cwd == "" {
		t.Errorf("captured fields wrong: %+v", coder)
	}
	// declared defaults, not captured facts
	if coder.Mode != ModeTemplate || coder.OnStale != OnStaleTemplate || coder.Target != "tab" {
		t.Errorf("defaults wrong: mode=%q onStale=%q target=%q", coder.Mode, coder.OnStale, coder.Target)
	}
	// unknowable: left for the human, and flagged
	if coder.Model != "" || coder.Rolefile != "" || coder.Origin != "" || coder.Profile != "" {
		t.Errorf("save must not invent model/rolefile/origin/profile: %+v", coder)
	}
	if !coder.RoleTODO() {
		t.Error("a captured peer with no role must flag as TODO")
	}
	// and it round-trips through the store
	if _, err := LoadFormation("roles"); err != nil {
		t.Errorf("a saved formation must load back: %v", err)
	}
}

// TestSaveFormationRefreshPreservesHandEdits is the contract that makes a re-save
// at a milestone boundary safe: sid checkpoints move, the prose around them does not.
func TestSaveFormationRefreshPreservesHandEdits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "roles", "coder", "sid-coder-v1")
	plantPeer(t, "roles", "orchestrator", "sid-orch")

	f, _, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	// the human fills in everything save cannot know, and adds prose of their own
	for i := range f.Peers {
		if f.Peers[i].Alias != "coder" {
			continue
		}
		f.Peers[i].Model = "opus"
		f.Peers[i].Rolefile = "roles/coder.md@b3a806e"
		f.Peers[i].Role = nil
		f.Peers[i].Origin = OriginFresh
		f.Peers[i].Mode = ModeResume
		f.Peers[i].OnStale = OnStaleFail
		f.Peers[i].Profile = "personal"
		f.Peers[i].Target = "window"
		f.Peers[i].Extra = map[string]json.RawMessage{"notes": json.RawMessage(`"owned the tree"`)}
	}
	f.Extra = map[string]json.RawMessage{"_hand_note": json.RawMessage(`"keep me"`)}
	f.Payload = json.RawMessage(`{"work_state":"hand-authored, irreplaceable"}`)
	f.DriftAnchors = map[string]json.RawMessage{
		"git_head": json.RawMessage(`"0000000"`),
		"notes":    json.RawMessage(`"bd is re-queried, never trusted"`),
	}
	f.AnchorAlias = "coder"
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	// the coder's session is replaced by a newer one, and a peer joins
	plantPeer(t, "roles", "coder", "sid-coder-v2")
	plantPeer(t, "roles", "reviewer", "sid-rev")

	f2, rep, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rep.New {
		t.Error("a refresh is not a new formation")
	}
	if len(rep.Added) != 1 || rep.Added[0] != "reviewer" {
		t.Errorf("added = %v want [reviewer]", rep.Added)
	}
	var coder *FormationPeer
	for i := range f2.Peers {
		if f2.Peers[i].Alias == "coder" {
			coder = &f2.Peers[i]
		}
	}
	// the captured fact moved
	if coder.SessionID != "sid-coder-v2" {
		t.Errorf("sid not refreshed: %q", coder.SessionID)
	}
	// everything the human wrote survived
	if coder.Model != "opus" || coder.Rolefile != "roles/coder.md@b3a806e" || coder.Origin != OriginFresh ||
		coder.Mode != ModeResume || coder.OnStale != OnStaleFail || coder.Profile != "personal" || coder.Target != "window" {
		t.Errorf("a refresh overwrote hand-maintained fields: %+v", coder)
	}
	if coder.Role != nil {
		t.Errorf("a refresh re-added a TODO over a cleared role: %v", *coder.Role)
	}
	if string(coder.Extra["notes"]) != `"owned the tree"` {
		t.Errorf("per-peer hand-edit lost: %v", coder.Extra)
	}
	if string(f2.Extra["_hand_note"]) != `"keep me"` {
		t.Errorf("envelope hand-edit lost: %v", f2.Extra)
	}
	if !strings.Contains(string(f2.Payload), "irreplaceable") {
		t.Errorf("payload lost: %s", f2.Payload)
	}
	if string(f2.DriftAnchors["notes"]) != `"bd is re-queried, never trusted"` {
		t.Errorf("hand-written drift anchor lost: %v", f2.DriftAnchors)
	}
	if f2.AnchorAlias != "coder" {
		t.Errorf("anchorAlias overwritten: %q", f2.AnchorAlias)
	}
}

// TestSaveFormationKeepsPeersOffTheChannel: the paused-effort case. Peers that have
// died since the last save are the reason the file exists.
func TestSaveFormationKeepsPeersOffTheChannel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "un", "coder", "sid-coder")
	plantPeer(t, "un", "orchestrator", "sid-orch")
	if _, _, err := SaveFormation("b31", "un"); err != nil {
		t.Fatalf("save: %v", err)
	}
	// the coder's tab is closed and its registration reaped
	if err := os.RemoveAll(filepath.Join(dir, "un", "coder")); err != nil {
		t.Fatal(err)
	}

	f, rep, err := SaveFormation("b31", "un")
	if err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if len(rep.Kept) != 1 || rep.Kept[0] != "coder" {
		t.Errorf("kept = %v want [coder]", rep.Kept)
	}
	found := false
	for i := range f.Peers {
		if f.Peers[i].Alias == "coder" {
			found = true
			if f.Peers[i].SessionID != "sid-coder" {
				t.Errorf("a kept peer's checkpoint was altered: %q", f.Peers[i].SessionID)
			}
		}
	}
	if !found {
		t.Fatal("a peer that left the channel must NOT be dropped from the formation")
	}
}

// TestSaveFormationRefusesToClobber: an envelope that will not load may still hold
// hours of hand-authored prose. Not loading is a reason to stop, not to replace.
func TestSaveFormationRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	plantPeer(t, "roles", "coder", "sid-coder")
	fdir := filepath.Join(dir, formationsDir)
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := `{"schema":"cbus-formation/v1","name":"roles","channel":"roles","peers":[{"alias":"x`
	if err := os.WriteFile(filepath.Join(fdir, "roles.json"), []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SaveFormation("roles", "roles"); err == nil {
		t.Fatal("want a refusal, not an overwrite")
	} else if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("error should say it refused to overwrite: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(fdir, "roles.json"))
	if string(b) != corrupt {
		t.Error("the unreadable file was modified")
	}
}

// TestSaveFormationRefusesChannelRepoint: re-pointing a formation at another
// channel silently would rewrite its whole meaning.
func TestSaveFormationRefusesChannelRepoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "roles", "coder", "sid-coder")
	plantPeer(t, "other", "coder", "sid-other")
	if _, _, err := SaveFormation("roles", "roles"); err != nil {
		t.Fatal(err)
	}
	_, _, err := SaveFormation("roles", "other")
	if err == nil || !strings.Contains(err.Error(), "records channel") {
		t.Errorf("want a channel-repoint refusal, got %v", err)
	}
}

// TestSaveFormationGitHeadAnchor: save records the cheap anchor apply will diff, and
// leaves any hand-written anchor alone. The package dir is inside a repo, so HEAD
// resolves here.
func TestSaveFormationGitHeadAnchor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	plantPeer(t, "roles", "coder", "sid-coder")
	f, _, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatal(err)
	}
	head, ok := gitHead()
	if !ok {
		t.Skip("not in a git repo")
	}
	var got string
	if err := json.Unmarshal(f.DriftAnchors["git_head"], &got); err != nil {
		t.Fatalf("git_head not recorded: %v", err)
	}
	if got != head {
		t.Errorf("git_head = %q want %q", got, head)
	}
}

// TestSaveFormationSavedByOutsider: a formation can be saved by a session that is
// not itself on the channel; savedBy must say so rather than invent an address.
func TestSaveFormationSavedByOutsider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-nobody")
	plantPeer(t, "roles", "coder", "sid-coder")
	f, _, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.SavedBy, "not joined") {
		t.Errorf("savedBy = %q, want it to admit the saver is not a peer", f.SavedBy)
	}
	if f.AnchorAlias != "" {
		t.Errorf("anchorAlias = %q, want empty when the saver is not on the channel", f.AnchorAlias)
	}
}

// TestCapturePeerFillTable is G5: origin/model fill exactly once, blank-only, and a
// hand-set value always wins.
func TestCapturePeerFillTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		envOrigin  string
		envModel   string
		metaOrigin string
		metaModel  string
		wantOrigin string
		wantModel  string
	}{
		{"blank env, meta present -> fill", "", "", "fresh", "opus", "fresh", "opus"},
		{"set env wins over meta -> no overwrite", "fork", "sonnet", "fresh", "opus", "fork", "sonnet"},
		{"blank meta never clobbers a set field", "joined", "opus", "", "", "joined", "opus"},
		{"blank env, blank meta -> stays blank", "", "", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &FormationPeer{Alias: "a", Origin: tc.envOrigin, Model: tc.envModel}
			r := RosterPeer{Alias: "a", Origin: tc.metaOrigin, Model: tc.metaModel}
			rep := &SaveReport{}
			capturePeer(p, r, rep)
			if p.Origin != tc.wantOrigin || p.Model != tc.wantModel {
				t.Errorf("got (origin %q, model %q), want (%q, %q)", p.Origin, p.Model, tc.wantOrigin, tc.wantModel)
			}
			if len(rep.SkippedBirth) != 0 {
				t.Errorf("no skip expected: %v", rep.SkippedBirth)
			}
		})
	}
}

// TestCapturePeerHardening is G6: a hand-corrupted meta origin/model must NOT ride
// into the envelope silently — it is skipped and surfaced.
func TestCapturePeerHardening(t *testing.T) {
	p := &FormationPeer{Alias: "coder"}
	r := RosterPeer{Alias: "coder", Origin: "spawned-maybe", Model: "--dangerous"}
	rep := &SaveReport{}
	capturePeer(p, r, rep)
	if p.Origin != "" || p.Model != "" {
		t.Errorf("garbage birth-record must NOT be captured: origin=%q model=%q", p.Origin, p.Model)
	}
	if len(rep.SkippedBirth) != 2 {
		t.Fatalf("both skips must be surfaced, got %v", rep.SkippedBirth)
	}
	joined := strings.Join(rep.SkippedBirth, " ")
	if !strings.Contains(joined, "origin") || !strings.Contains(joined, "model") || !strings.Contains(joined, "coder") {
		t.Errorf("skip reasons must name the peer and the fields: %v", rep.SkippedBirth)
	}
	// and the envelope it produced still validates (the garbage never reached it)
	f := &Formation{Schema: FormationSchema, Name: "x", Channel: "x", Peers: []FormationPeer{*p}}
	if err := f.Validate(); err != nil {
		t.Errorf("a skipped-garbage save must still be valid: %v", err)
	}
}

// TestSaveEndToEndBirth is G8: real Spawn -> real Join -> real SaveFormation captures
// origin=fresh + model with ZERO hand-edit. This is exactly the smoke's step 8, gone.
func TestSaveEndToEndBirth(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-parent")

	// the launcher spawns a fresh child on opus (fake terminal, real Spawn -> real
	// ReserveAlias stamps the birth-record).
	if _, _, err := Spawn("tab", "roles", "opus", "coder", "", &fakeForker{}); err != nil {
		t.Fatal(err)
	}
	// the child boots and joins under its own sid, reclaiming the reservation.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-child")
	if _, _, err := Join("roles", "coder"); err != nil {
		t.Fatal(err)
	}
	// the saver captures the channel.
	f, _, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatal(err)
	}
	var coder *FormationPeer
	for i := range f.Peers {
		if f.Peers[i].Alias == "coder" {
			coder = &f.Peers[i]
		}
	}
	if coder == nil {
		t.Fatal("coder not captured")
	}
	if coder.Origin != OriginFresh {
		t.Errorf("origin = %q, want fresh captured with NO hand-edit", coder.Origin)
	}
	if coder.Model != "opus" {
		t.Errorf("model = %q, want opus captured", coder.Model)
	}
	// and a save-minted origin=fresh means apply would RESUME it (given a transcript),
	// not refuse it on D12 — the whole point of the birth-record.
	w := testWorld()
	w.Host = coder.Machine // the peer was born on this host; match it
	w = withTranscripts(w, coder.SessionID)
	coder.Mode = ModeResume
	pp := decidePeer(coder, f, w, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if pp.Action != ActionResume {
		t.Errorf("a fresh-born peer with a transcript should resume, got %v (%s)", pp.Action, pp.Reason)
	}
}

// TestSaveFormationPreservesSplitOnDisk: split is hand-maintained — save never
// derives it from the roster, and a refresh must carry it through untouched. The
// assertion is deliberately made against the FILE BYTES and a reload, not the
// returned struct: capturePeer already leaves the field alone, so the way this
// actually breaks is the value never reaching disk (a serializer that forgets the
// key), which a struct-only check would sail straight past.
func TestSaveFormationPreservesSplitOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "roles", "coder", "sid-coder")
	plantPeer(t, "roles", "orchestrator", "sid-orch")

	f, _, err := SaveFormation("roles", "roles")
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	for i := range f.Peers {
		if f.Peers[i].Alias == "coder" {
			f.Peers[i].Target = "pane"
			f.Peers[i].Split = "right"
		}
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	// a later save refreshes live facts; the hand-set direction must ride through
	plantPeer(t, "roles", "coder", "sid-coder-v2")
	if _, _, err := SaveFormation("roles", "roles"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, formationsDir, "roles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"split": "right"`) {
		t.Errorf("the file lost the hand-set split:\n%s", b)
	}
	reloaded, err := LoadFormation("roles")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reloaded.Peers {
		if p.Alias != "coder" {
			continue
		}
		if p.Split != "right" {
			t.Errorf("reloaded split = %q, want right", p.Split)
		}
		if p.SessionID != "sid-coder-v2" {
			t.Errorf("the live fact should still refresh alongside it, sid = %q", p.SessionID)
		}
	}
	// a formation written back out must still validate — a preserved value that the
	// envelope would reject on the next apply is not actually preserved
	if err := reloaded.Validate(); err != nil {
		t.Errorf("round-tripped formation no longer validates: %v", err)
	}
}
