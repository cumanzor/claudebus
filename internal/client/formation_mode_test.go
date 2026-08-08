package client

import (
	"strings"
	"testing"
)

// planUnderMode runs the per-run override exactly as Apply does — overrideMode against
// the in-memory formation, then BuildPlan — so a gate that fires here is a gate that
// fires under a real --mode run, not one a test-only shim arranged.
func planUnderMode(t *testing.T, mode string, w *PlanWorld, peers ...FormationPeer) *Plan {
	t.Helper()
	f := formationOf(peers...)
	if err := overrideMode(f, ApplyOptions{Mode: mode}); err != nil {
		t.Fatalf("overrideMode(%q): %v", mode, err)
	}
	return BuildPlan(f, w, nil)
}

func onePlanUnderMode(t *testing.T, mode string, p FormationPeer, w *PlanWorld) PeerPlan {
	t.Helper()
	plan := planUnderMode(t, mode, w, p)
	if len(plan.Peers) != 1 {
		t.Fatalf("want 1 peer plan, got %d", len(plan.Peers))
	}
	return plan.Peers[0]
}

func TestModeOverrideValidates(t *testing.T) {
	for _, mode := range []string{ModeResume, ModeFork, ModeTemplate} {
		f := formationOf(peer("coder", func(p *FormationPeer) { p.Mode = ModeTemplate }))
		if err := overrideMode(f, ApplyOptions{Mode: mode}); err != nil {
			t.Errorf("--mode %s must be accepted: %v", mode, err)
		}
		if f.Peers[0].Mode != mode {
			t.Errorf("--mode %s left the peer at %q", mode, f.Peers[0].Mode)
		}
	}
	f := formationOf(peer("coder", func(p *FormationPeer) { p.Mode = ModeResume }))
	err := overrideMode(f, ApplyOptions{Mode: "restore"})
	if err == nil {
		t.Fatal("an unknown --mode must be refused, not silently ignored")
	}
	for _, want := range []string{"resume", "fork", "template", "restore"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q so the user can fix it: %v", want, err)
		}
	}
	if f.Peers[0].Mode != ModeResume {
		t.Errorf("a refused override must leave the formation alone, got mode %q", f.Peers[0].Mode)
	}
	// no flag = no rewrite; the envelope's own per-peer modes stand
	if err := overrideMode(f, ApplyOptions{}); err != nil {
		t.Fatalf("an absent --mode is a no-op: %v", err)
	}
	if f.Peers[0].Mode != ModeResume {
		t.Errorf("an absent --mode rewrote the peer to %q", f.Peers[0].Mode)
	}
}

// TestModeOverrideRewritesPlannedMode is the feature in one assertion, in both
// directions. Each fixture records the mode the override must beat: the resume case
// starts at template (without the override it plans template — a different answer),
// the template case starts at resume (the "bring it back blank" half of the
// late-bound choice, which a resume-only override would silently fail).
func TestModeOverrideRewritesPlannedMode(t *testing.T) {
	cases := []struct {
		name     string
		recorded string
		override string
		want     PeerAction
	}{
		{"template recorded, resume asked", ModeTemplate, ModeResume, ActionResume},
		{"template recorded, fork asked", ModeTemplate, ModeFork, ActionFork},
		{"resume recorded, template asked", ModeResume, ModeTemplate, ActionTemplate},
		{"fork recorded, resume asked", ModeFork, ModeResume, ActionResume},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := peer("coder", func(p *FormationPeer) {
				p.Mode, p.Origin, p.SessionID = tc.recorded, OriginFresh, "sid-coder"
			})
			got := onePlanUnderMode(t, tc.override, p, withTranscripts(testWorld(), "sid-coder"))
			if got.Action != tc.want {
				t.Errorf("--mode %s over a %s peer = %s (%s), want %s",
					tc.override, tc.recorded, got.Action, orEmpty(got.Reason), tc.want)
			}
			if got.Degraded {
				t.Errorf("nothing degraded here — the transcript is present: %s", got.Reason)
			}
		})
	}
}

// TestModeOverrideStillGates is the safety claim the flag rests on: a blanket
// --mode resume is not a bypass. Every fixture records mode=template, so without the
// override each would plan a clean ActionTemplate — the mutant and the mechanism
// disagree on every row, and a row that answered "template" would be the flag
// smuggling a peer past its own gate.
func TestModeOverrideStillGates(t *testing.T) {
	cases := []struct {
		name       string
		mut        func(*FormationPeer)
		world      func() *PlanWorld
		wantAction PeerAction
		wantReason string
		degraded   bool
	}{
		{
			name: "fork-born peer refuses resume",
			mut:  func(p *FormationPeer) { p.Origin = OriginFork; p.SessionID = "sid-coder" },
			world: func() *PlanWorld {
				return withTranscripts(testWorld(), "sid-coder")
			},
			wantAction: ActionRefuse,
			wantReason: "origin=fork",
		},
		{
			name: "unrecorded origin refuses resume",
			mut:  func(p *FormationPeer) { p.Origin = ""; p.SessionID = "sid-coder" },
			world: func() *PlanWorld {
				return withTranscripts(testWorld(), "sid-coder")
			},
			wantAction: ActionRefuse,
			wantReason: "origin recorded",
		},
		{
			name: "live-armed sid refuses resume",
			mut:  func(p *FormationPeer) { p.Origin = OriginFresh; p.SessionID = "sid-coder" },
			world: func() *PlanWorld {
				w := withTranscripts(testWorld(), "sid-coder")
				w.LiveSids = map[string]string{"sid-coder": "ch/elsewhere"}
				return w
			},
			wantAction: ActionRefuse,
			wantReason: "live-armed at ch/elsewhere",
		},
		{
			name:       "no session recorded degrades",
			mut:        func(p *FormationPeer) { p.Origin = OriginFresh; p.SessionID = "" },
			world:      testWorld,
			wantAction: ActionTemplate,
			wantReason: "no session is recorded",
			degraded:   true,
		},
		{
			name:       "gone transcript degrades (onStale=template)",
			mut:        func(p *FormationPeer) { p.Origin = OriginFresh; p.SessionID = "sid-coder" },
			world:      testWorld,
			wantAction: ActionTemplate,
			wantReason: "no transcript found",
			degraded:   true,
		},
		{
			name: "gone transcript skips (onStale=skip)",
			mut: func(p *FormationPeer) {
				p.Origin, p.SessionID, p.OnStale = OriginFresh, "sid-coder", OnStaleSkip
			},
			world:      testWorld,
			wantAction: ActionSkip,
			wantReason: "no transcript found",
		},
		{
			name: "gone transcript refuses (onStale=fail)",
			mut: func(p *FormationPeer) {
				p.Origin, p.SessionID, p.OnStale = OriginFresh, "sid-coder", OnStaleFail
			},
			world:      testWorld,
			wantAction: ActionRefuse,
			wantReason: "no transcript found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := peer("coder", func(p *FormationPeer) { p.Mode = ModeTemplate }, tc.mut)
			// the discriminating half: recorded as template, this peer plans a plain
			// template launch with no override at all
			if base := planFor(t, p, tc.world()); base.Action != ActionTemplate || base.Degraded {
				t.Fatalf("fixture is not discriminating: without --mode it already plans %s (%s)",
					base.Action, orEmpty(base.Reason))
			}
			got := onePlanUnderMode(t, ModeResume, p, tc.world())
			if got.Action != tc.wantAction {
				t.Fatalf("--mode resume = %s (%s), want %s", got.Action, orEmpty(got.Reason), tc.wantAction)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("reason must name %q, got %q", tc.wantReason, got.Reason)
			}
			if got.Degraded != tc.degraded {
				t.Errorf("Degraded = %v, want %v — a peer that came back blank when resume was asked "+
					"for is a different peer, and the operator has to be told", got.Degraded, tc.degraded)
			}
		})
	}
}

// TestModeOverrideRefusesDuplicateSid needs two peers in one envelope, so it sits
// outside the single-peer table.
func TestModeOverrideRefusesDuplicateSid(t *testing.T) {
	mut := func(p *FormationPeer) { p.Mode = ModeTemplate; p.Origin = OriginFresh; p.SessionID = "sid-shared" }
	plan := planUnderMode(t, ModeResume, withTranscripts(testWorld(), "sid-shared"),
		peer("coder", mut), peer("reviewer", mut))
	for _, pp := range plan.Peers {
		if pp.Action != ActionRefuse {
			t.Fatalf("%s = %s, want refuse: one transcript claimed twice must not be resumed by a flag",
				pp.Peer.Alias, pp.Action)
		}
		if !strings.Contains(pp.Reason, "more than one alias") {
			t.Errorf("%s reason = %q", pp.Peer.Alias, pp.Reason)
		}
	}
}

// TestModeOverrideLeavesPresentPeers is reconcile-is-sacred under the flag. The
// fixture is chosen so the two answers cannot be confused: the live peer's sid is
// live-armed (it is armed right here), so a mode read that happened BEFORE the
// present check would refuse it. present and refused are different words.
func TestModeOverrideLeavesPresentPeers(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	t.Setenv("PATH", "/usr/bin:/bin")
	applierOn(t, "ch", "applier")
	plantPeer(t, "ch", "coder", "sid-coder")
	armPeer(t, "ch", "coder")

	// a missing peer rides along so the run is a real reconcile (a fleet with nothing
	// launchable fails by design, D13) — some live, some not, which is the case the
	// flag actually meets
	f := applyFixture(
		peer("coder", func(p *FormationPeer) {
			p.Mode, p.Origin, p.SessionID, p.Machine = ModeTemplate, OriginFresh, "sid-coder", ShortHostname()
		}),
		peer("documenter", func(p *FormationPeer) {
			p.Mode, p.Origin, p.SessionID, p.Machine = ModeTemplate, OriginFresh, "sid-doc", ShortHostname()
		}),
	)
	fk := &recForker{}
	rep, err := applyWith(t, f, ApplyOptions{Mode: ModeResume, Wait: 0}, fk,
		func(profile, sid string) bool { return true })
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := outcomeFor(t, rep, "coder"); got != OutcomePresent {
		t.Fatalf("a live peer must stay present under --mode, got %s", got)
	}
	if got := outcomeFor(t, rep, "documenter"); got != OutcomeResumed {
		t.Errorf("documenter = %s, want resumed — the override still steers the MISSING peers", got)
	}
	if _, launched := fk.specFor("coder"); launched {
		t.Errorf("--mode relaunched a peer that was already live: %v", fk.aliases())
	}
}

// TestModeOverrideComposesWithOnly is the per-peer steering the feature ships
// instead of a --mode alias=value syntax: steer one peer, then bring the rest back
// as the file says. The second run rebuilds the fixture because that is what the
// second process does — it re-reads an envelope the first run never wrote.
func TestModeOverrideComposesWithOnly(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	t.Setenv("PATH", "/usr/bin:/bin")
	applierOn(t, "ch", "applier")

	fixture := func() *Formation {
		mut := func(p *FormationPeer) {
			p.Mode, p.Origin, p.Machine = ModeTemplate, OriginFresh, ShortHostname()
		}
		return applyFixture(
			peer("documenter", mut, func(p *FormationPeer) { p.SessionID = "sid-doc" }),
			peer("coder", mut, func(p *FormationPeer) { p.SessionID = "sid-coder" }),
		)
	}
	has := func(profile, sid string) bool { return true }

	steer := &recForker{}
	rep, err := applyWith(t, fixture(), ApplyOptions{Mode: ModeResume, Only: []string{"documenter"}}, steer, has)
	if err != nil {
		t.Fatalf("steering apply: %v", err)
	}
	if got := outcomeFor(t, rep, "documenter"); got != OutcomeResumed {
		t.Errorf("documenter = %s, want resumed under --mode resume --only documenter", got)
	}
	if got := outcomeFor(t, rep, "coder"); got != OutcomeSkipped {
		t.Errorf("coder = %s, want skipped: --only did not hold under --mode", got)
	}
	if spec, ok := steer.specFor("documenter"); !ok {
		t.Fatal("documenter was never launched")
	} else if j := strings.Join(spec.Argv, " "); !strings.Contains(j, "--resume sid-doc") ||
		strings.Contains(j, "--fork-session") {
		t.Errorf("documenter argv = %v", spec.Argv)
	}
	if len(steer.specs) != 1 {
		t.Errorf("the steering run launched %v, want documenter alone", steer.aliases())
	}

	rest := &recForker{}
	rep2, err := applyWith(t, fixture(), ApplyOptions{}, rest, has)
	if err != nil {
		t.Fatalf("plain apply: %v", err)
	}
	for _, alias := range []string{"documenter", "coder"} {
		if got := outcomeFor(t, rep2, alias); got != OutcomeTemplated {
			t.Errorf("%s = %s on the plain second apply, want templated — the override was for one run", alias, got)
		}
	}
	if spec, ok := rest.specFor("coder"); !ok {
		t.Fatal("coder was never launched by the plain apply")
	} else if strings.Contains(strings.Join(spec.Argv, " "), "--resume") {
		t.Errorf("the plain apply carried a resume the file never asked for: %v", spec.Argv)
	}
}

// TestModeOverrideDryRun: the plan the operator previews is the plan the flag makes,
// and previewing it launches nothing.
func TestModeOverrideDryRun(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	t.Setenv("PATH", "/usr/bin:/bin")
	applierOn(t, "ch", "applier")

	f := applyFixture(
		peer("documenter", func(p *FormationPeer) {
			p.Mode, p.Origin, p.SessionID, p.Machine = ModeTemplate, OriginFresh, "sid-doc", ShortHostname()
		}),
		peer("ghost", func(p *FormationPeer) {
			p.Mode, p.Origin, p.SessionID, p.Machine = ModeTemplate, OriginFork, "sid-parent", ShortHostname()
		}),
	)
	fk := &recForker{}
	rep, err := applyWith(t, f, ApplyOptions{Mode: ModeResume, DryRun: true}, fk,
		func(profile, sid string) bool { return true })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !rep.DryRun {
		t.Error("the report must say it was a dry run")
	}
	if got := outcomeFor(t, rep, "documenter"); got != OutcomeResumed {
		t.Errorf("documenter = %s, want resumed — a dry run that planned differently from the "+
			"real thing would be a rehearsal of the wrong play", got)
	}
	if got := outcomeFor(t, rep, "ghost"); got != OutcomeRefused {
		t.Errorf("ghost = %s, want refused: the preview must show the gate too", got)
	}
	if len(fk.specs) != 0 {
		t.Errorf("--dry-run launched %v", fk.aliases())
	}
}

func outcomeFor(t *testing.T, rep *ApplyReport, alias string) Outcome {
	t.Helper()
	for _, r := range rep.Results {
		if r.Alias == alias {
			return r.Outcome
		}
	}
	t.Fatalf("no result for %q in %+v", alias, rep.Results)
	return ""
}
