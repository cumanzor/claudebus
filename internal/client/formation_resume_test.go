package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resumeFixture is a one-anchor formation in the state the verb expects to find
// after a reboot: sid recorded, fresh-born, transcript present, nobody armed.
func resumeFixture() *Formation {
	return &Formation{
		Schema: FormationSchema, Name: "dd", Channel: "dd", AnchorAlias: "orchestrator",
		Peers: []FormationPeer{{
			Alias: "orchestrator", SessionID: "sid-anchor", Origin: OriginJoined,
			Mode: ModeTemplate, Machine: "host-a", Profile: "work",
			Cwd: "/nonexistent/recorded/cwd",
		}},
	}
}

func resumeWorld() *PlanWorld {
	return &PlanWorld{
		Host:          "host-a",
		LiveSids:      map[string]string{},
		HasTranscript: func(profile, sid string) bool { return true },
	}
}

func TestResumeAnchorLaunchShape(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := resumeFixture()
	fk := &recForker{ids: []string{"surface-1"}}
	created, err := resumeAnchorWorld(f, "finish the rollout", fk, resumeWorld())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if created != "surface-1" || len(fk.specs) != 1 {
		t.Fatalf("created=%q specs=%d", created, len(fk.specs))
	}
	argv := fk.specs[0].Argv
	// the recorded profile must win even from a bare shell: ccs <profile>, never a
	// bare claude that would resume against the wrong config dir
	if len(argv) < 5 || argv[0] != "ccs" || argv[1] != "work" {
		t.Fatalf("argv prefix = %v, want ccs work", argv[:2])
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--resume sid-anchor", "--name orchestrator"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %v", want, argv)
		}
	}
	prompt := argv[len(argv)-1]
	for _, want := range []string{
		"SAME session",              // restored framing, not a fresh brief
		"cbus formation apply dd",   // the reconcile instruction
		"finish the rollout",        // the brief rode along
		"cbus join dd orchestrator", // re-join instruction with real names
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("kickoff missing %q", want)
		}
	}
	// launcher-authored restore: with no claim anywhere (this store is empty), the
	// derived run is blank — never invented
	b, err := os.ReadFile(ledgerPath("dd"))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	line := string(b)
	if !strings.Contains(line, `"restore"`) || !strings.Contains(line, "sid-anchor") {
		t.Errorf("ledger missing restore event: %s", line)
	}
	if strings.Contains(line, "run_") {
		t.Errorf("restore with no surviving claim must carry a blank run, got: %s", line)
	}
}

func TestResumeAnchorRestoreAdoptsOwnSurvivingClaim(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	// the discriminating case for run attribution: the anchor's own claim SURVIVED the
	// reboot (claims outlive processes by design), so the restore event must carry
	// that run — the acting alias's own claim, never blank and never a sibling's
	dir := filepath.Join(CBUSDir(), "dd", "orchestrator")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".run"), []byte("run_20260807T000000Z_aaaaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	fk := &recForker{}
	if _, err := resumeAnchorWorld(resumeFixture(), "", fk, resumeWorld()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	b, err := os.ReadFile(ledgerPath("dd"))
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if !strings.Contains(string(b), "run_20260807T000000Z_aaaaaa") {
		t.Errorf("restore must adopt the anchor's own surviving claim, got: %s", b)
	}
}

func TestResumeAnchorBlankMachineMeansHere(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	// house semantic pinned deliberately (review finding DECLINED): an empty machine
	// means "here", exactly as apply's decidePeer documents for hand-authored files —
	// the resume verb must not be stricter than apply about the same field
	f := resumeFixture()
	f.Peers[0].Machine = ""
	fk := &recForker{}
	if _, err := resumeAnchorWorld(f, "", fk, resumeWorld()); err != nil {
		t.Fatalf("blank machine must mean here, got refusal: %v", err)
	}
	if len(fk.specs) != 1 {
		t.Fatal("blank-machine anchor was not launched")
	}
}

func TestResumeAnchorBareProfileFallsBack(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "") // bare shell, no CCS
	f := resumeFixture()
	f.Peers[0].Profile = ""
	fk := &recForker{}
	if _, err := resumeAnchorWorld(f, "", fk, resumeWorld()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if fk.specs[0].Argv[0] != "claude" {
		t.Errorf("blank profile from a bare shell should launch claude, got %v", fk.specs[0].Argv[:1])
	}
}

func TestResumeAnchorRefusals(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	for _, tc := range []struct {
		name string
		mut  func(f *Formation, w *PlanWorld)
		want string
	}{
		{"no anchor alias", func(f *Formation, w *PlanWorld) { f.AnchorAlias = "" }, "no anchorAlias"},
		{"anchor not a peer", func(f *Formation, w *PlanWorld) { f.AnchorAlias = "ghost" }, "names no peer"},
		{"wrong machine", func(f *Formation, w *PlanWorld) { w.Host = "host-b" }, "run this there"},
		{"no sid", func(f *Formation, w *PlanWorld) { f.Peers[0].SessionID = "" }, "no session recorded"},
		{"reserved sid", func(f *Formation, w *PlanWorld) { f.Peers[0].SessionID = "reserved" }, "no session recorded"},
		{"duplicate sid", func(f *Formation, w *PlanWorld) {
			f.Peers = append(f.Peers, FormationPeer{Alias: "other", SessionID: "sid-anchor", Origin: OriginJoined})
		}, "more than one alias"},
		{"fork-born", func(f *Formation, w *PlanWorld) { f.Peers[0].Origin = OriginFork }, "PARENT's transcript"},
		{"origin unknown", func(f *Formation, w *PlanWorld) { f.Peers[0].Origin = "" }, "origin recorded"},
		{"transcript gone", func(f *Formation, w *PlanWorld) {
			w.HasTranscript = func(string, string) bool { return false }
		}, "no transcript found"},
		{"live-armed", func(f *Formation, w *PlanWorld) {
			w.LiveSids["sid-anchor"] = "dd/orchestrator"
			// the discriminating input: transcript ALSO unfindable, which is the real
			// cross-profile shape — live-armed must still win (gate order pinned by the
			// first real-store smoke, where the wrong refusal fired)
			w.HasTranscript = func(string, string) bool { return false }
		}, "live-armed"},
	} {
		f, w := resumeFixture(), resumeWorld()
		tc.mut(f, w)
		fk := &recForker{}
		_, err := resumeAnchorWorld(f, "", fk, w)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.want)
		}
		if len(fk.specs) != 0 {
			t.Errorf("%s: a refusal must launch NOTHING, forked %d", tc.name, len(fk.specs))
		}
	}
}

// fleetFixture is the decision brief's subject: an anchor plus one peer in each state
// a roster row can report — warm, gone, recorded elsewhere, never recorded.
func fleetFixture() *Formation {
	return &Formation{
		Schema: FormationSchema, Name: "dd", Channel: "dd", AnchorAlias: "orchestrator",
		Peers: []FormationPeer{
			{Alias: "orchestrator", SessionID: "sid-anchor", Origin: OriginJoined,
				Mode: ModeTemplate, Machine: "host-a", Profile: "work", Cwd: "/nonexistent/recorded/cwd"},
			{Alias: "documenter", SessionID: "sid-warm", Origin: OriginFresh,
				Mode: ModeResume, Machine: "host-a", Profile: "work"},
			{Alias: "reviewer", SessionID: "sid-gone", Origin: OriginFresh,
				Mode: ModeResume, Machine: "host-a", Profile: "work"},
			{Alias: "tester", SessionID: "sid-remote", Origin: OriginFresh,
				Mode: ModeResume, Machine: "host-b", Profile: "work"},
			{Alias: "planner", Origin: OriginFresh, Mode: ModeTemplate, Machine: "host-a"},
		},
	}
}

func fleetWorld() *PlanWorld {
	warm := map[string]bool{"sid-anchor": true, "sid-warm": true}
	return &PlanWorld{
		Host:          "host-a",
		LiveSids:      map[string]string{},
		HasTranscript: func(_, sid string) bool { return warm[sid] },
	}
}

func rosterLine(t *testing.T, prompt, alias string) string {
	t.Helper()
	for _, ln := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), alias+" ") {
			return ln
		}
	}
	t.Fatalf("no roster row for %q in:\n%s", alias, prompt)
	return ""
}

func anchorPrompt(t *testing.T, f *Formation, w *PlanWorld) string {
	t.Helper()
	fk := &recForker{}
	if _, err := resumeAnchorWorld(f, "", fk, w); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(fk.specs) != 1 {
		t.Fatalf("forked %d times", len(fk.specs))
	}
	argv := fk.specs[0].Argv
	return argv[len(argv)-1]
}

// TestAnchorKickoffRosterRendersEveryState: the brief is only a decision brief if the
// rows say different things about different peers. The warm-and-gone pair is the
// discriminating input — a mutant that prints one verdict for the whole fleet passes
// any single-peer fixture and dies here.
func TestAnchorKickoffRosterRendersEveryState(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	prompt := anchorPrompt(t, fleetFixture(), fleetWorld())

	warm := rosterLine(t, prompt, "documenter")
	for _, want := range []string{"mode=resume", "origin=fresh", "transcript=present", "machine=host-a"} {
		if !strings.Contains(warm, want) {
			t.Errorf("warm peer row missing %q: %s", want, warm)
		}
	}
	if gone := rosterLine(t, prompt, "reviewer"); !strings.Contains(gone, "transcript=GONE") {
		t.Errorf("a peer whose transcript is missing must render GONE: %s", gone)
	}
	// recorded on another machine: this host cannot see its transcripts, so the brief
	// must not claim they are gone
	remote := rosterLine(t, prompt, "tester")
	if !strings.Contains(remote, "unchecked (recorded on host-b)") || strings.Contains(remote, "GONE") {
		t.Errorf("cross-machine peer must render unchecked, not GONE: %s", remote)
	}
	if none := rosterLine(t, prompt, "planner"); !strings.Contains(none, "transcript=none recorded") {
		t.Errorf("a peer with no sid must say so: %s", none)
	}
}

// TestAnchorKickoffAnchorIsTheDecidingSeat is B8: the anchor appears in its own
// roster as the seat making the call, never as a peer awaiting one. Its transcript was
// proved by the gates before this prompt existed, so re-asking would be a decision the
// anchor cannot make about itself.
func TestAnchorKickoffAnchorIsTheDecidingSeat(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	prompt := anchorPrompt(t, fleetFixture(), fleetWorld())

	row := rosterLine(t, prompt, "orchestrator")
	if !strings.Contains(row, "this session, restored") {
		t.Errorf("the anchor row must name itself as the restored session: %s", row)
	}
	for _, forbidden := range []string{"transcript=", "mode=", "origin="} {
		if strings.Contains(row, forbidden) {
			t.Errorf("the anchor row must not render %q as if it were awaiting a decision: %s", forbidden, row)
		}
	}
}

// TestAnchorKickoffSpeaksInFlagsAndReconvenes: the decision has to be expressible.
// The examples name the formation's OWN aliases (a placeholder is one more thing to
// translate before acting), the liveness handoff points at the dry-run rather than at
// this snapshot, and the checkpoint gets refreshed once the fleet answers.
func TestAnchorKickoffSpeaksInFlagsAndReconvenes(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	prompt := anchorPrompt(t, fleetFixture(), fleetWorld())

	for _, want := range []string{
		"cbus formation apply dd --mode resume --only documenter", // a REAL alias, and a resumable one
		"cbus formation apply dd --wait 90s",
		"--dry-run",
		"RIGHT NOW", // liveness comes from the dry-run, not from this composed-once snapshot
		"cbus formation save dd dd",
		"recreate it fresh",
		"skip it",
		"confirm the plan with the operator",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("decision brief missing %q:\n%s", want, prompt)
		}
	}
	// the --only example must not offer a peer that cannot be resumed
	for _, ln := range strings.Split(prompt, "\n") {
		if strings.Contains(ln, "--only") && strings.Contains(ln, "reviewer") {
			t.Errorf("the resume example names a peer whose transcript is gone: %s", ln)
		}
	}
}

// TestResumeAnchorArgvShapeUnchanged pins M2's diff surface: the brief changed the
// PROMPT and nothing about how the anchor is launched. A gate, flag or ordering move
// reds here instead of asking a reader to trust the diff.
func TestResumeAnchorArgvShapeUnchanged(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fk := &recForker{}
	if _, err := resumeAnchorWorld(fleetFixture(), "", fk, fleetWorld()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	argv := fk.specs[0].Argv
	want := []string{"ccs", "work", "--resume", "sid-anchor", "--name", "orchestrator"}
	if len(argv) != len(want)+1 {
		t.Fatalf("argv = %v, want the recorded prefix plus exactly one prompt", argv)
	}
	for i, w := range want {
		if argv[i] != w {
			t.Errorf("argv[%d] = %q want %q", i, argv[i], w)
		}
	}
	if !strings.Contains(argv[len(argv)-1], "you are the anchor") {
		t.Errorf("the prompt must be the last argument: %v", argv)
	}
}
