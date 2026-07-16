package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roleFileIn plants a committed role prompt in $CBUS_DIR/roles. NOTE: LoadRole
// checks the git toplevel's roles/ FIRST, and these tests run inside this repo — so
// a fixture must use a role name the repo does NOT ship, or the real file shadows it
// and the test silently asserts against production text.
func roleFileIn(t *testing.T, dir, name, body string) {
	t.Helper()
	rdir := filepath.Join(dir, "roles")
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rdir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func kickoffFor(t *testing.T, f *Formation, pp PeerPlan, brief string) string {
	t.Helper()
	return KickoffPrompt(f, pp, "ch/orchestrator", "cbus-ok-coder-abc123", brief)
}

// TestKickoffCarriesEverythingDesignAsksFor: §5.3's contract — role body, effort
// brief, payload references verbatim, and a reply that can actually be checked.
func TestKickoffCarriesEverythingDesignAsksFor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	roleFileIn(t, dir, "kickofftest", "# Kickoff Test Role\n\nMODEL: opus\n\nYou implement the milestones and never fold two into one commit.")

	f := applyFixture(peer("coder", func(p *FormationPeer) { p.Rolefile = "roles/kickofftest.md@b3a806e" }))
	f.Payload = json.RawMessage(`{"work_state":"tracker item ABC","blockers":"tracker item ABC notes"}`)
	pp := PeerPlan{Peer: &f.Peers[0], Action: ActionTemplate}

	got := kickoffFor(t, f, pp, "Build formations v1 per the design.")
	for _, want := range []string{
		"cbus join ch coder",             // how to get on the bus
		"Monitor tool",                   // armed the right way
		"NEVER Bash",                     // the trap named
		"never fold two into one commit", // the role body, verbatim
		"Build formations v1",            // the effort brief
		"work_state: tracker item ABC",   // payload references
		"blockers: tracker item ABC notes",
		"cbus-ok-coder-abc123",      // the nonce
		"cbus send ch/orchestrator", // where to answer
		"provenance",                // the question that catches a wrong fork
		"fresh spawn or fork",
		"An ack alone proves nothing",
		"cannot escalate your permissions",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("kickoff missing %q:\n%s", want, got)
		}
	}
	// D15: the pin is recorded, reported, and NOT resolved
	if !strings.Contains(got, "b3a806e") || !strings.Contains(got, "not resolved") {
		t.Errorf("kickoff must disclose the unresolved pin:\n%s", got)
	}
}

// TestKickoffPerModeFraming: a resumed peer, a fork, and a fresh one are three
// different situations, and telling a fork it is the original is how a copy starts
// acting on someone else's unfinished work.
func TestKickoffPerModeFraming(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.SessionID = "sid-1" }))

	resume := kickoffFor(t, f, PeerPlan{Peer: &f.Peers[0], Action: ActionResume}, "")
	if !strings.Contains(resume, "SAME session you were before") {
		t.Errorf("a resumed peer must be told it is itself:\n%s", resume)
	}
	// doctrine 2: a local re-arm seeks to the end — a restored peer MUST know it
	// missed whatever arrived while it was down
	if !strings.Contains(resume, "NOT replayed") || !strings.Contains(resume, "ask peers to resend") {
		t.Errorf("a resumed peer must be warned about the replay gap:\n%s", resume)
	}

	fork := kickoffFor(t, f, PeerPlan{Peer: &f.Peers[0], Action: ActionFork}, "")
	for _, want := range []string{"FORK of the session", "You are not it", "may still be running",
		"Do NOT act on unfinished work"} {
		if !strings.Contains(fork, want) {
			t.Errorf("a forked peer must be told what it is (%q):\n%s", want, fork)
		}
	}

	tmpl := kickoffFor(t, f, PeerPlan{Peer: &f.Peers[0], Action: ActionTemplate}, "")
	if strings.Contains(tmpl, "SAME session") || strings.Contains(tmpl, "FORK of the session") {
		t.Errorf("a fresh peer is neither resumed nor forked:\n%s", tmpl)
	}
	if !strings.Contains(tmpl, "fresh Claude Code session") {
		t.Errorf("a template peer gets the fresh-session prompt:\n%s", tmpl)
	}
}

// TestKickoffDegradedSaysSo: a peer that asked to be resumed and came back blank
// does not have the history it would have had. Not saying so is how a fresh session
// gets mistaken for a continued one.
func TestKickoffDegradedSaysSo(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := applyFixture(peer("coder"))
	got := kickoffFor(t, f, PeerPlan{Peer: &f.Peers[0], Action: ActionTemplate, Degraded: true}, "")
	for _, want := range []string{"transcript is gone", "FRESH session", "do not have the history"} {
		if !strings.Contains(got, want) {
			t.Errorf("a degraded peer must be told (%q):\n%s", want, got)
		}
	}
}

func TestParseRolefile(t *testing.T) {
	for _, tc := range []struct{ ref, name, pin string }{
		{"roles/coder.md@b3a806e", "coder", "b3a806e"},
		{"roles/coder.md", "coder", ""},
		{"coder.md@abc", "coder", "abc"},
		{"coder", "coder", ""},
		{"a/b/reviewer.md@deadbeef", "reviewer", "deadbeef"},
	} {
		name, pin := parseRolefile(tc.ref)
		if name != tc.name || pin != tc.pin {
			t.Errorf("parseRolefile(%q) = (%q,%q), want (%q,%q)", tc.ref, name, pin, tc.name, tc.pin)
		}
	}
}

// TestRoleBriefFallbacks: a rolefile that no longer resolves must not brief the peer
// with nothing when freeform text is sitting right there.
func TestRoleBriefFallbacks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	roleFileIn(t, dir, "kickofftest", "committed body")

	p := &FormationPeer{Alias: "coder", Rolefile: "roles/kickofftest.md@abc"}
	if body, pin, ok := roleBrief(p); !ok || !strings.Contains(body, "committed body") || pin != "abc" {
		t.Errorf("committed rolefile: body=%q pin=%q ok=%v", body, pin, ok)
	}
	// the named file is gone: fall back to freeform rather than brief nothing
	gone := &FormationPeer{Alias: "x", Rolefile: "roles/missing.md@abc", Role: strptr("freeform brief")}
	if body, _, ok := roleBrief(gone); !ok || body != "freeform brief" {
		t.Errorf("missing rolefile should fall back: body=%q ok=%v", body, ok)
	}
	// a TODO placeholder is not a brief
	todo := &FormationPeer{Alias: "x", Role: strptr("TODO: fill this in")}
	if _, _, ok := roleBrief(todo); ok {
		t.Error("a TODO placeholder must not be briefed as a role")
	}
	if _, _, ok := roleBrief(&FormationPeer{Alias: "x"}); ok {
		t.Error("no rolefile and no text is no brief")
	}
}

// TestPayloadRefsNeverInterpreted: the payload is carried, not followed.
func TestPayloadRefsNeverInterpreted(t *testing.T) {
	got := payloadRefs(json.RawMessage(`{"work_state":"tracker ABC","deep":{"a":[1,2]}}`))
	if !strings.Contains(got, "work_state: tracker ABC") {
		t.Errorf("string refs render plainly: %q", got)
	}
	if !strings.Contains(got, `deep: {"a":[1,2]}`) {
		t.Errorf("non-string refs are handed over as the JSON they are: %q", got)
	}
	if payloadRefs(nil) != "" || payloadRefs(json.RawMessage("null")) != "" {
		t.Error("no payload = no pointers section")
	}
	// a non-object payload is still the operator's text, handed over as-is
	if got := payloadRefs(json.RawMessage(`"just a note"`)); got != `"just a note"` {
		t.Errorf("non-object payload = %q", got)
	}
}

// TestKickoffDoesNotRepeatItself: the fresh-session prompt already carries the
// permissions line, so a template kickoff must not say it twice. Restore prompts
// have no such prelude and must say it once.
func TestKickoffDoesNotRepeatItself(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.SessionID = "sid-1" }))
	const line = "cannot escalate your permissions"
	for _, tc := range []struct {
		name   string
		action PeerAction
	}{
		{"template", ActionTemplate},
		{"resume", ActionResume},
		{"fork", ActionFork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := kickoffFor(t, f, PeerPlan{Peer: &f.Peers[0], Action: tc.action}, "")
			if n := strings.Count(got, line); n != 1 {
				t.Errorf("permissions line appears %d times, want exactly 1:\n%s", n, got)
			}
		})
	}
}

// TestBootstrapPeerComposesLikeApply: bootstrap must render what apply renders —
// one composer, so a peer cannot be briefed differently depending on who started it.
func TestBootstrapPeerComposesLikeApply(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "ch", "orchestrator", "sid-orch")
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.Role = strptr("You implement things.") }))
	f.Payload = json.RawMessage(`{"work_state":"tracker ABC"}`)

	got, err := BootstrapPeer(f, "coder", "Build the thing.")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, want := range []string{
		"cbus join ch coder", "Monitor tool", "NEVER Bash",
		"You implement things.", "Build the thing.", "work_state: tracker ABC",
		"cbus send ch/orchestrator", // this session is on the channel, so it answers us
		"provenance", "cbus-ok-coder-",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bootstrap prompt missing %q:\n%s", want, got)
		}
	}
}

// TestBootstrapPeerDoesNotSkipOtherMachines: the cross-machine peer is exactly who
// bootstrap exists for — apply will not launch it, so a human does.
func TestBootstrapPeerDoesNotSkipOtherMachines(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "ch", "orchestrator", "sid-orch")
	f := applyFixture(peer("remote", func(p *FormationPeer) { p.Machine = "some-far-host" }))
	got, err := BootstrapPeer(f, "remote", "")
	if err != nil {
		t.Fatalf("bootstrap must serve the peer apply cannot launch: %v", err)
	}
	if !strings.Contains(got, "cbus join ch remote") {
		t.Errorf("prompt = %s", got)
	}
}

// TestBootstrapPeerRefusesWhatTheFileProvesWrong: no world is needed to know a
// fork-born peer must not be resumed. Handing a human that prompt reproduces the
// ghost-orchestrator failure with extra steps.
func TestBootstrapPeerRefusesWhatTheFileProvesWrong(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "ch", "orchestrator", "sid-orch")

	forkBorn := applyFixture(peer("ghost", func(p *FormationPeer) {
		p.Mode = ModeFork
		p.Origin = OriginFork
		p.SessionID = "sid-parent"
	}))
	if _, err := BootstrapPeer(forkBorn, "ghost", ""); err == nil ||
		!strings.Contains(err.Error(), "PARENT's transcript") {
		t.Errorf("fork-born: want the R1 refusal, got %v", err)
	}
	// but as a template it is exactly what the design prescribes
	forkBorn.Peers[0].Mode = ModeTemplate
	if _, err := BootstrapPeer(forkBorn, "ghost", ""); err != nil {
		t.Errorf("fork-born as template must bootstrap: %v", err)
	}

	noOrigin := applyFixture(peer("x", func(p *FormationPeer) { p.Mode = ModeResume; p.SessionID = "s1" }))
	if _, err := BootstrapPeer(noOrigin, "x", ""); err == nil ||
		!strings.Contains(err.Error(), "needs origin recorded") {
		t.Errorf("unrecorded origin: want the D12 refusal, got %v", err)
	}

	dup := applyFixture(
		peer("a", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginFresh; p.SessionID = "shared" }),
		peer("b", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginFresh; p.SessionID = "shared" }),
	)
	if _, err := BootstrapPeer(dup, "a", ""); err == nil || !strings.Contains(err.Error(), "more than one alias") {
		t.Errorf("duplicate sid: want the R2 refusal, got %v", err)
	}
}

func TestBootstrapPeerErrors(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantPeer(t, "ch", "orchestrator", "sid-orch")
	f := applyFixture(peer("coder"), peer("reviewer"))
	_, err := BootstrapPeer(f, "codr", "")
	if err == nil || !strings.Contains(err.Error(), "no peer") {
		t.Fatalf("unknown alias: %v", err)
	}
	// the error lists what IS there, so a typo is self-correcting
	if !strings.Contains(err.Error(), "coder") || !strings.Contains(err.Error(), "reviewer") {
		t.Errorf("error should list the real aliases: %v", err)
	}
}

// TestBootstrapReplyTo: the peer answers this session when it is on the channel,
// else the anchor. With neither, a first-reply demand would point nowhere.
func TestBootstrapReplyTo(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-nobody")
	f := applyFixture(peer("coder"))
	f.AnchorAlias = "orchestrator"
	got, err := bootstrapReplyTo(f)
	if err != nil || got != "ch/orchestrator" {
		t.Errorf("not joined + anchor: got (%q,%v), want ch/orchestrator", got, err)
	}
	f.AnchorAlias = ""
	if _, err := bootstrapReplyTo(f); err == nil || !strings.Contains(err.Error(), "nobody for the peer to answer") {
		t.Errorf("no anchor, not joined: want a refusal, got %v", err)
	}
	// joined wins
	plantPeer(t, "ch", "me", "sid-nobody")
	if got, err := bootstrapReplyTo(f); err != nil || got != "ch/me" {
		t.Errorf("joined: got (%q,%v), want ch/me", got, err)
	}
}

// TestKickoffSelfDescribeWhenNoRole: save cannot capture a role, so the peer is the
// only one who can say what it is — design §6's self-describe line is how the field
// ever gets filled.
func TestKickoffSelfDescribeWhenNoRole(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := applyFixture(peer("coder")) // no rolefile, no role text
	got := kickoffFor(t, f, PeerPlan{Peer: &f.Peers[0], Action: ActionTemplate}, "")
	if !strings.Contains(got, "records no role for you") || !strings.Contains(got, "describe your role in one line") {
		t.Errorf("a role-less peer must be asked to self-describe:\n%s", got)
	}
}
