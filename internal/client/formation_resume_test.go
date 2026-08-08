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
