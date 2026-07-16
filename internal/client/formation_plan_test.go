package client

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// armPeer makes a planted peer read as LIVE the way the real thing does: a running
// process whose argv carries the inbox path, which is exactly what the liveness
// predicate greps for (a bare pid would pass the kill -0 clause and fail the argv
// one). tail -f blocks and holds the path, standing in for the real follower.
func armPeer(t *testing.T, ch, alias string) {
	t.Helper()
	dir := filepath.Join(CBUSDir(), ch, alias)
	inbox := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("tail", "-f", metaInboxNeedle(filepath.Join(dir, "meta.json")))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m peerMeta
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.ListenerPid = json.RawMessage(strconv.Itoa(cmd.Process.Pid))
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
}

// testWorld is a world with nothing live and no transcripts: every case below opts
// into exactly the live state it needs, so a prohibition can never pass by accident.
func testWorld() *PlanWorld {
	return &PlanWorld{
		Host:          "test-host",
		GitHead:       "abc1234",
		Roster:        nil,
		LiveSids:      map[string]string{},
		HasTranscript: func(profile, sid string) bool { return false },
	}
}

func withTranscripts(w *PlanWorld, sids ...string) *PlanWorld {
	have := map[string]bool{}
	for _, s := range sids {
		have[s] = true
	}
	w.HasTranscript = func(profile, sid string) bool { return have[sid] }
	return w
}

func peer(alias string, mut ...func(*FormationPeer)) FormationPeer {
	p := FormationPeer{Alias: alias, Machine: "test-host", Target: "tab"}
	for _, m := range mut {
		m(&p)
	}
	return p
}

func formationOf(peers ...FormationPeer) *Formation {
	return &Formation{Schema: FormationSchema, Name: "f", Channel: "ch", Peers: peers}
}

// planFor is the single-peer helper the prohibition table uses.
func planFor(t *testing.T, p FormationPeer, w *PlanWorld) PeerPlan {
	t.Helper()
	plan := BuildPlan(formationOf(p), w, nil)
	if len(plan.Peers) != 1 {
		t.Fatalf("want 1 peer plan, got %d", len(plan.Peers))
	}
	return plan.Peers[0]
}

func TestPlanModeResolution(t *testing.T) {
	live := "aaaa-bbbb"
	for _, tc := range []struct {
		name   string
		peer   FormationPeer
		world  func() *PlanWorld
		want   PeerAction
		reason string // substring, "" = don't care
	}{
		{
			name: "no mode defaults to template",
			peer: peer("a"),
			want: ActionTemplate,
		},
		{
			name: "template needs no transcript",
			peer: peer("a", func(p *FormationPeer) { p.Mode = ModeTemplate }),
			want: ActionTemplate,
		},
		{
			name:  "resume with a transcript and a recorded origin",
			peer:  peer("a", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginFresh; p.SessionID = live }),
			world: func() *PlanWorld { return withTranscripts(testWorld(), live) },
			want:  ActionResume,
		},
		{
			name:  "fork with a transcript and a recorded origin",
			peer:  peer("a", func(p *FormationPeer) { p.Mode = ModeFork; p.Origin = OriginFresh; p.SessionID = live }),
			world: func() *PlanWorld { return withTranscripts(testWorld(), live) },
			want:  ActionFork,
		},
		{
			name:   "empty machine reads as local",
			peer:   peer("a", func(p *FormationPeer) { p.Machine = "" }),
			want:   ActionTemplate,
			reason: "",
		},
		{
			name:   "another machine is skipped, naming both values",
			peer:   peer("a", func(p *FormationPeer) { p.Machine = "mbp" }),
			want:   ActionSkip,
			reason: `recorded on "mbp", this host is "test-host"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := testWorld()
			if tc.world != nil {
				w = tc.world()
			}
			got := planFor(t, tc.peer, w)
			if got.Action != tc.want {
				t.Fatalf("action = %v (%s), want %v", got.Action, got.Reason, tc.want)
			}
			if tc.reason != "" && !strings.Contains(got.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to contain %q", got.Reason, tc.reason)
			}
		})
	}
}

// TestPlanRefusesForkBornResume is R1: the ghost-orchestrator prohibition. A
// fork-born peer's sid is its PARENT's transcript; resuming it re-runs the parent's
// intent under this peer's name, which is what the B31 restore actually did.
func TestPlanRefusesForkBornResume(t *testing.T) {
	sid := "parent-transcript"
	for _, mode := range []string{ModeResume, ModeFork} {
		t.Run(mode, func(t *testing.T) {
			p := peer("console", func(p *FormationPeer) {
				p.Mode = mode
				p.Origin = OriginFork
				p.SessionID = sid
			})
			got := planFor(t, p, withTranscripts(testWorld(), sid))
			if got.Action != ActionRefuse {
				t.Fatalf("action = %v, want refuse", got.Action)
			}
			for _, want := range []string{"origin=fork", "PARENT's transcript", "mode=template"} {
				if !strings.Contains(got.Reason, want) {
					t.Errorf("reason should carry %q: %s", want, got.Reason)
				}
			}
		})
	}
	// the same peer restored as a template is exactly what the design prescribes
	p := peer("console", func(p *FormationPeer) {
		p.Mode = ModeTemplate
		p.Origin = OriginFork
		p.SessionID = sid
	})
	if got := planFor(t, p, withTranscripts(testWorld(), sid)); got.Action != ActionTemplate {
		t.Errorf("a fork-born peer must still restore as a template, got %v", got.Action)
	}
}

// TestPlanRefusesUnrecordedOrigin is D12: an unrecorded origin is not evidence of a
// safe one, and save cannot record it — so this is the common shape, not an edge.
func TestPlanRefusesUnrecordedOrigin(t *testing.T) {
	sid := "some-transcript"
	for _, mode := range []string{ModeResume, ModeFork} {
		p := peer("coder", func(p *FormationPeer) { p.Mode = mode; p.SessionID = sid }) // origin ""
		got := planFor(t, p, withTranscripts(testWorld(), sid))
		if got.Action != ActionRefuse {
			t.Fatalf("%s with empty origin: action = %v, want refuse", mode, got.Action)
		}
		if !strings.Contains(got.Reason, "needs origin recorded") {
			t.Errorf("reason should name the fix: %s", got.Reason)
		}
		// and it must NOT read like the fork-born refusal — two different failures
		if strings.Contains(got.Reason, "PARENT's transcript") {
			t.Errorf("unrecorded origin must not be reported as fork-born: %s", got.Reason)
		}
	}
	// template is unaffected: the refusal only bites a deliberate transcript touch
	p := peer("coder", func(p *FormationPeer) { p.SessionID = sid })
	if got := planFor(t, p, withTranscripts(testWorld(), sid)); got.Action != ActionTemplate {
		t.Errorf("empty origin must not block template, got %v (%s)", got.Action, got.Reason)
	}
}

// TestPlanRefusesDuplicateSid is R2: one transcript claimed by two aliases.
//
// The claimants deliberately ALSO trip R1 (origin=fork) and D12 (origin unrecorded),
// which pins the precedence: R2 is checked first, so every claimant must report the
// shared-sid reason. With claimants that only trip R2, the checks could be reordered
// and no test would notice — the reviewer proved that by swapping them and watching
// the suite stay green.
//
// R2 leads on purpose: a duplicate sid means the FILE is wrong, and the file has to
// be fixed before any per-peer question about it means anything.
func TestPlanRefusesDuplicateSid(t *testing.T) {
	sid := "shared-transcript"
	f := formationOf(
		peer("orchestrator", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginJoined; p.SessionID = sid }),
		peer("console", func(p *FormationPeer) { p.Mode = ModeFork; p.Origin = OriginFork; p.SessionID = sid }),
		peer("nomode", func(p *FormationPeer) { p.Mode = ModeResume; p.SessionID = sid }), // origin ""
	)
	plan := BuildPlan(f, withTranscripts(testWorld(), sid), nil)
	if len(plan.Peers) != 3 {
		t.Fatalf("want 3 peer plans, got %d", len(plan.Peers))
	}
	for _, pp := range plan.Peers {
		if pp.Action != ActionRefuse {
			t.Errorf("%s: action = %v, want refuse", pp.Peer.Alias, pp.Action)
		}
		if !strings.Contains(pp.Reason, "more than one alias") {
			t.Errorf("%s: want the shared-sid reason (R2 precedes R1/D12), got %q", pp.Peer.Alias, pp.Reason)
		}
		// the peers that also trip R1/D12 must NOT report those instead
		if strings.Contains(pp.Reason, "PARENT's transcript") || strings.Contains(pp.Reason, "needs origin recorded") {
			t.Errorf("%s: R2 must win the precedence, got %q", pp.Peer.Alias, pp.Reason)
		}
	}
	if len(plan.Launchable()) != 0 {
		t.Error("a duplicate sid must leave nothing launchable")
	}
}

// TestPlanResumeGateOnLiveSid is D14: resume refuses when the original is alive
// elsewhere, and says how to proceed. fork is deliberately NOT gated — the design
// points at it for exactly this case.
func TestPlanResumeGateOnLiveSid(t *testing.T) {
	sid := "live-elsewhere"
	w := withTranscripts(testWorld(), sid)
	w.LiveSids[sid] = "un/orchestrator"

	p := peer("coder", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginFresh; p.SessionID = sid })
	got := planFor(t, p, w)
	if got.Action != ActionRefuse {
		t.Fatalf("resume on a live sid: action = %v, want refuse", got.Action)
	}
	for _, want := range []string{"live-armed at un/orchestrator", "mode=fork"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason should carry %q: %s", want, got.Reason)
		}
	}
	// fork of a live original is legitimate and documented: a deliberate copy
	pf := peer("coder", func(p *FormationPeer) { p.Mode = ModeFork; p.Origin = OriginFresh; p.SessionID = sid })
	if got := planFor(t, pf, w); got.Action != ActionFork {
		t.Errorf("fork must not be gated on a live original, got %v (%s)", got.Action, got.Reason)
	}
	// with the original dead, resume proceeds
	w2 := withTranscripts(testWorld(), sid)
	if got := planFor(t, p, w2); got.Action != ActionResume {
		t.Errorf("resume with a dead original: got %v (%s)", got.Action, got.Reason)
	}
}

func TestPlanOnStalePolicies(t *testing.T) {
	for _, tc := range []struct {
		policy   string
		want     PeerAction
		degraded bool
	}{
		{"", ActionTemplate, true}, // default
		{OnStaleTemplate, ActionTemplate, true},
		{OnStaleSkip, ActionSkip, false},
		{OnStaleFail, ActionRefuse, false},
	} {
		t.Run("onStale="+orEmpty(tc.policy), func(t *testing.T) {
			p := peer("coder", func(p *FormationPeer) {
				p.Mode = ModeResume
				p.Origin = OriginFresh
				p.SessionID = "gone"
				p.OnStale = tc.policy
			})
			got := planFor(t, p, testWorld()) // no transcripts anywhere
			if got.Action != tc.want {
				t.Fatalf("action = %v (%s), want %v", got.Action, got.Reason, tc.want)
			}
			if got.Degraded != tc.degraded {
				t.Errorf("degraded = %v want %v", got.Degraded, tc.degraded)
			}
			if !strings.Contains(got.Reason, "no transcript found") {
				t.Errorf("reason must say why: %s", got.Reason)
			}
		})
	}
	// a resume/fork peer with no sid recorded at all takes the same path
	p := peer("coder", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginFresh })
	got := planFor(t, p, testWorld())
	if got.Action != ActionTemplate || !got.Degraded {
		t.Errorf("no sid: got %v degraded=%v (%s)", got.Action, got.Degraded, got.Reason)
	}
}

func orEmpty(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

// TestPlanReconcileOnly: a peer already live on the channel is left alone; a dead
// one is relaunched. That is the whole reconcile contract.
func TestPlanReconcileOnly(t *testing.T) {
	f := formationOf(peer("coder"), peer("reviewer"))
	w := testWorld()
	w.Roster = []RosterPeer{
		{Alias: "coder", Listening: true},
		{Alias: "reviewer", Listening: false}, // registered but not armed = missing
	}
	plan := BuildPlan(f, w, nil)
	byAlias := map[string]PeerPlan{}
	for _, pp := range plan.Peers {
		byAlias[pp.Peer.Alias] = pp
	}
	if byAlias["coder"].Action != ActionPresent {
		t.Errorf("a live peer must be left alone, got %v", byAlias["coder"].Action)
	}
	if byAlias["reviewer"].Action != ActionTemplate {
		t.Errorf("a dead peer must be relaunched, got %v", byAlias["reviewer"].Action)
	}
	if len(plan.Launchable()) != 1 {
		t.Errorf("launchable = %d, want 1", len(plan.Launchable()))
	}
}

// TestPlanOnlyFilter: --only narrows the launch, and a dry run plans identically —
// same code path, so a rehearsal cannot diverge from the real thing.
func TestPlanOnlyFilter(t *testing.T) {
	f := formationOf(peer("coder"), peer("reviewer"), peer("documenter"))
	w := testWorld()

	plan := BuildPlan(f, w, []string{"coder"})
	if len(plan.Launchable()) != 1 || plan.Launchable()[0].Peer.Alias != "coder" {
		t.Fatalf("--only coder: launchable = %+v", plan.Launchable())
	}
	for _, pp := range plan.Peers {
		if pp.Peer.Alias == "coder" {
			continue
		}
		if pp.Action != ActionSkip || !strings.Contains(pp.Reason, "--only") {
			t.Errorf("%s: want a skip naming --only, got %v (%s)", pp.Peer.Alias, pp.Action, pp.Reason)
		}
	}
	// no filter = everything
	if len(BuildPlan(f, w, nil).Launchable()) != 3 {
		t.Error("no --only should launch every peer")
	}
	// a typo selects nothing and must be catchable
	if bad := UnknownAliases(f, []string{"codr", "coder"}); len(bad) != 1 || bad[0] != "codr" {
		t.Errorf("UnknownAliases = %v, want [codr]", bad)
	}
}

// TestPlanIsPure: same inputs, same plan. The prohibitions are decided here, so a
// plan that varied run to run would make every refusal a coin flip.
func TestPlanIsPure(t *testing.T) {
	f := formationOf(
		peer("coder", func(p *FormationPeer) { p.Mode = ModeResume; p.Origin = OriginFresh; p.SessionID = "s1" }),
		peer("ghost", func(p *FormationPeer) { p.Mode = ModeFork; p.Origin = OriginFork; p.SessionID = "s2" }),
		peer("far", func(p *FormationPeer) { p.Machine = "elsewhere" }),
	)
	w := withTranscripts(testWorld(), "s1", "s2")
	first := BuildPlan(f, w, nil)
	for i := 0; i < 5; i++ {
		again := BuildPlan(f, w, nil)
		for j := range first.Peers {
			if first.Peers[j].Action != again.Peers[j].Action || first.Peers[j].Reason != again.Peers[j].Reason {
				t.Fatalf("plan is not deterministic at peer %d", j)
			}
		}
	}
	// and it launched nothing / mutated nothing
	before, _ := json.Marshal(f)
	BuildPlan(f, w, nil)
	after, _ := json.Marshal(f)
	if string(before) != string(after) {
		t.Error("BuildPlan mutated the formation")
	}
}

func TestPlanDrift(t *testing.T) {
	f := formationOf(peer("coder"))
	f.DriftAnchors = map[string]json.RawMessage{
		"git_head": json.RawMessage(`"e844702"`),
		"notes":    json.RawMessage(`"bd is re-queried, never trusted"`),
	}
	w := testWorld() // GitHead abc1234
	plan := BuildPlan(f, w, nil)
	if len(plan.Drift) != 1 {
		t.Fatalf("drift = %+v, want one finding", plan.Drift)
	}
	if plan.Drift[0].Saved != "e844702" || plan.Drift[0].Now != "abc1234" {
		t.Errorf("drift = %+v", plan.Drift[0])
	}
	// drift never blocks: the snapshot is a cache, the ground is live
	if len(plan.Launchable()) != 1 {
		t.Error("drift must not stop a launch")
	}
	// matching head = no finding
	w.GitHead = "e844702"
	if d := BuildPlan(f, w, nil).Drift; len(d) != 0 {
		t.Errorf("no drift expected, got %+v", d)
	}
	// outside a repo there is nothing to compare
	w.GitHead = ""
	if d := BuildPlan(f, w, nil).Drift; len(d) != 0 {
		t.Errorf("no git head = no drift claim, got %+v", d)
	}
	// prose anchors are for humans, not diffs
	f2 := formationOf(peer("coder"))
	f2.DriftAnchors = map[string]json.RawMessage{"notes": json.RawMessage(`"whatever"`)}
	if d := BuildPlan(f2, testWorld(), nil).Drift; len(d) != 0 {
		t.Errorf("only git_head is diffed, got %+v", d)
	}
}

// TestLiveSidsReadsTheBus covers the I/O half of the resume gate: which session ids
// are held by a live-armed peer anywhere on this machine. A never-armed peer is not
// live, and a reservation holds no session at all.
func TestLiveSidsReadsTheBus(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantPeer(t, "un", "orchestrator", "sid-live")
	plantPeer(t, "roles", "coder", "sid-dead")
	plantPeer(t, "roles", "pending", "reserved")

	// nothing is armed yet: listenerPid is null on all three
	if got := liveSids(); len(got) != 0 {
		t.Fatalf("no armed listeners, want no live sids, got %v", got)
	}

	// arm one for real: this process IS alive, and its argv contains the inbox path
	// the liveness predicate greps for.
	armPeer(t, "un", "orchestrator")
	got := liveSids()
	if at, ok := got["sid-live"]; !ok || at != "un/orchestrator" {
		t.Errorf("live sid map = %v, want sid-live -> un/orchestrator", got)
	}
	if _, ok := got["sid-dead"]; ok {
		t.Error("an unarmed peer must not read as live")
	}
	if _, ok := got["reserved"]; ok {
		t.Error("a reservation holds no session and must never appear")
	}
}
