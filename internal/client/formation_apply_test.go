package client

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// recForker records every launch instead of opening a window.
type recForker struct {
	specs []ForkSpec
	err   error
}

func (f *recForker) Fork(s ForkSpec) error {
	if f.err != nil {
		return f.err
	}
	f.specs = append(f.specs, s)
	return nil
}

func (f *recForker) aliases() []string {
	var out []string
	for _, s := range f.specs {
		for i, a := range s.Argv {
			if a == "--name" && i+1 < len(s.Argv) {
				out = append(out, s.Argv[i+1])
			}
		}
	}
	return out
}

func (f *recForker) specFor(alias string) (ForkSpec, bool) {
	for _, s := range f.specs {
		for i, a := range s.Argv {
			if a == "--name" && i+1 < len(s.Argv) && s.Argv[i+1] == alias {
				return s, true
			}
		}
	}
	return ForkSpec{}, false
}

var nonceRe = regexp.MustCompile(`cbus-ok-[A-Za-z0-9._-]+`)

// answeringForker stands in for peers that boot and answer: it pulls the nonce out
// of the prompt it was handed and appends the reply to the applier's inbox, exactly
// as a real `cbus send` would. That exercises the whole convergence loop — compose,
// launch, poll, match — without a terminal.
type answeringForker struct {
	recForker
	t     *testing.T
	inbox string
	quiet map[string]bool // aliases that stay silent
}

func (f *answeringForker) Fork(s ForkSpec) error {
	if err := f.recForker.Fork(s); err != nil {
		return err
	}
	prompt := s.Argv[len(s.Argv)-1]
	nonce := nonceRe.FindString(prompt)
	if nonce == "" {
		f.t.Fatalf("kickoff carries no nonce: %s", prompt)
	}
	for a := range f.quiet {
		if strings.Contains(nonce, "-"+a+"-") {
			return nil
		}
	}
	line := `{"from":"x/y","to":"z","ts":"2026-07-16T00:00:00Z","text":"` + nonce + ` — read the role; provenance: fresh spawn"}` + "\n"
	fh, err := os.OpenFile(f.inbox, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(line); err != nil {
		f.t.Fatal(err)
	}
	return nil
}

// applierOn joins this session to ch as alias so Apply has an address and an inbox.
func applierOn(t *testing.T, ch, alias string) string {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-applier")
	plantPeer(t, ch, alias, "sid-applier")
	inbox := filepath.Join(CBUSDir(), ch, alias, "inbox.jsonl")
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return inbox
}

func applyFixture(peers ...FormationPeer) *Formation {
	return &Formation{
		Schema: FormationSchema, Name: "f", Channel: "ch",
		AnchorAlias: "orchestrator", Peers: peers,
	}
}

// TestApplyPerModeArgv is the assertion the B31 restore needed and did not have:
// the three modes must produce three different launches, and resume must NOT carry
// --fork-session (that would checkpoint a peer that asked to continue as itself).
func TestApplyPerModeArgv(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	t.Setenv("PATH", "/usr/bin:/bin")
	applierOn(t, "ch", "applier")

	f := applyFixture(
		peer("orchestrator", func(p *FormationPeer) { p.Mode = ModeTemplate; p.Origin = OriginJoined; p.Model = "fable" }),
		peer("coder", func(p *FormationPeer) {
			p.Mode = ModeResume
			p.Origin = OriginFresh
			p.SessionID = "sid-coder"
			p.Model = "opus"
		}),
		peer("clone", func(p *FormationPeer) {
			p.Mode = ModeFork
			p.Origin = OriginFresh
			p.SessionID = "sid-clone"
			p.Model = "sonnet"
		}),
	)
	for i := range f.Peers {
		f.Peers[i].Machine = ShortHostname()
	}
	fk := &recForker{}
	hasTranscript := func(profile, sid string) bool { return sid == "sid-coder" || sid == "sid-clone" }
	rep, err := applyWith(t, f, ApplyOptions{}, fk, hasTranscript)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	_ = rep

	tmpl, ok := fk.specFor("orchestrator")
	if !ok {
		t.Fatal("template peer not launched")
	}
	if joined := strings.Join(tmpl.Argv, " "); strings.Contains(joined, "--resume") {
		t.Errorf("a template launch must be a FRESH session, got: %s", joined)
	}
	if !strings.HasPrefix(strings.Join(tmpl.Argv, " "), "ccs personal --model fable --name orchestrator") {
		t.Errorf("template argv = %v", tmpl.Argv)
	}

	res, ok := fk.specFor("coder")
	if !ok {
		t.Fatal("resume peer not launched")
	}
	joined := strings.Join(res.Argv, " ")
	if !strings.Contains(joined, "--resume sid-coder") {
		t.Errorf("resume argv must carry --resume <sid>: %v", res.Argv)
	}
	if strings.Contains(joined, "--fork-session") {
		t.Errorf("resume must NOT fork — the peer continues as itself: %v", res.Argv)
	}

	frk, ok := fk.specFor("clone")
	if !ok {
		t.Fatal("fork peer not launched")
	}
	if j := strings.Join(frk.Argv, " "); !strings.Contains(j, "--resume sid-clone --fork-session") {
		t.Errorf("fork argv must carry --resume <sid> --fork-session: %v", frk.Argv)
	}
}

// applyWith runs apply with the transcript predicate stubbed, so a launch table does
// not need real transcripts on disk. It goes through the same applyWorld the real
// Apply does — only the world is injected.
func applyWith(t *testing.T, f *Formation, opts ApplyOptions, forker TerminalForker,
	has func(profile, sid string) bool) (*ApplyReport, error) {
	t.Helper()
	self, err := applierAddress(f.Channel)
	if err != nil {
		return nil, err
	}
	if bad := UnknownAliases(f, opts.Only); len(bad) > 0 {
		return nil, fmt.Errorf("--only names no such peer: %s", strings.Join(bad, ", "))
	}
	w, err := GatherPlanWorld(f.Channel)
	if err != nil {
		return nil, err
	}
	if has != nil {
		w.HasTranscript = has
	}
	return applyWorld(f, opts, forker, w, self)
}

func TestApplyRequiresAJoinedApplier(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-nobody")
	f := applyFixture(peer("orchestrator"))
	_, err := Apply(f, ApplyOptions{}, &recForker{})
	if err == nil || !strings.Contains(err.Error(), "not on") {
		t.Fatalf("want a refusal naming the join, got %v", err)
	}
	if !strings.Contains(err.Error(), "cbus join ch") {
		t.Errorf("the error must name the fix: %v", err)
	}
}

// TestApplyAnchorFirst: peers whose members expect an orchestrator to be listening
// need one first, so the anchor leads regardless of file order.
func TestApplyAnchorFirst(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(
		peer("coder", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("reviewer", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }),
	)
	fk := &recForker{}
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatal(err)
	}
	got := fk.aliases()
	if len(got) != 3 || got[0] != "orchestrator" {
		t.Errorf("launch order = %v, want the anchor first", got)
	}
}

// TestApplyReconcileSkipsPresentAndRefused: apply launches the MISSING, and never
// what a prohibition stopped.
func TestApplyReconcileSkipsPresentAndRefused(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "orchestrator")
	armPeer(t, "ch", "orchestrator") // the applier is genuinely live

	f := applyFixture(
		peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("ghost", func(p *FormationPeer) { // fork-born: R1 refuses
			p.Mode = ModeFork
			p.Origin = OriginFork
			p.SessionID = "sid-parent"
			p.Machine = ShortHostname()
		}),
		peer("coder", func(p *FormationPeer) { p.Machine = ShortHostname() }),
	)
	fk := &recForker{}
	rep, err := applyWith(t, f, ApplyOptions{}, fk, func(_, sid string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if got := fk.aliases(); len(got) != 1 || got[0] != "coder" {
		t.Fatalf("launched %v, want only the missing peer", got)
	}
	byAlias := map[string]PeerResult{}
	for _, r := range rep.Results {
		byAlias[r.Alias] = r
	}
	if byAlias["orchestrator"].Outcome != OutcomePresent {
		t.Errorf("a live peer must read present, got %v", byAlias["orchestrator"].Outcome)
	}
	if byAlias["ghost"].Outcome != OutcomeRefused {
		t.Errorf("a fork-born peer must read refused, got %v", byAlias["ghost"].Outcome)
	}
	if byAlias["ghost"].Detail == "" {
		t.Error("a refusal must carry its reason into the report")
	}
}

// TestApplyZeroLaunchableFails is D13: an empty fleet is not a success.
func TestApplyZeroLaunchableFails(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("orchestrator", func(p *FormationPeer) { p.Machine = "some-other-host" }))
	fk := &recForker{}
	rep, err := applyWith(t, f, ApplyOptions{}, fk, nil)
	if err == nil {
		t.Fatal("want an error when nothing is launchable")
	}
	if !strings.Contains(err.Error(), "nothing to launch") {
		t.Errorf("error = %v", err)
	}
	if len(fk.specs) != 0 {
		t.Error("nothing may be launched")
	}
	if len(rep.Results) != 1 || rep.Results[0].Outcome != OutcomeSkipped {
		t.Errorf("the report must still explain every peer: %+v", rep.Results)
	}
	// but a dry run of the same thing is not an error — it is the answer
	if _, err := applyWith(t, f, ApplyOptions{DryRun: true}, &recForker{}, nil); err != nil {
		t.Errorf("--dry-run must report, not fail: %v", err)
	}
}

// TestApplyDryRunLaunchesNothing: the rehearsal must use the same plan as the play.
func TestApplyDryRunLaunchesNothing(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }))
	fk := &recForker{}
	rep, err := applyWith(t, f, ApplyOptions{DryRun: true, Wait: time.Second}, fk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fk.specs) != 0 {
		t.Fatalf("--dry-run launched %d session(s)", len(fk.specs))
	}
	if !rep.DryRun || len(rep.Results) != 1 || rep.Results[0].Outcome != OutcomeTemplated {
		t.Errorf("dry run should still report the intended outcome: %+v", rep.Results)
	}
	// no alias was claimed either
	if dirExists(filepath.Join(CBUSDir(), "ch", "orchestrator")) {
		t.Error("--dry-run must not reserve an alias")
	}
}

func TestApplyOnlyFilter(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(
		peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("coder", func(p *FormationPeer) { p.Machine = ShortHostname() }),
	)
	fk := &recForker{}
	if _, err := applyWith(t, f, ApplyOptions{Only: []string{"coder"}}, fk, nil); err != nil {
		t.Fatal(err)
	}
	if got := fk.aliases(); len(got) != 1 || got[0] != "coder" {
		t.Errorf("--only launched %v", got)
	}
	// a typo must fail loudly rather than select nothing
	_, err := applyWith(t, f, ApplyOptions{Only: []string{"codr"}}, &recForker{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no such peer") {
		t.Errorf("want an unknown-alias error, got %v", err)
	}
}

// TestApplyConvergenceRoundTrip: a peer counts as converged only when it answers.
// The applier's Monitor is not consulted and the inbox is only ever read.
func TestApplyConvergenceRoundTrip(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	inbox := applierOn(t, "ch", "applier")
	f := applyFixture(
		peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("coder", func(p *FormationPeer) { p.Machine = ShortHostname() }),
	)
	fk := &answeringForker{t: t, inbox: inbox}
	rep, err := applyWith(t, f, ApplyOptions{Wait: 5 * time.Second}, fk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Converged() {
		t.Fatalf("both peers answered; report = %+v", rep.Results)
	}
	for _, r := range rep.Results {
		if !r.Answered {
			t.Errorf("%s: want answered", r.Alias)
		}
	}
	// the inbox is intact: reading must never consume what the Monitor also reads
	b, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "cbus-ok-") != 2 {
		t.Errorf("apply consumed or rewrote its own inbox:\n%s", b)
	}
}

// TestApplySilentPeerFails: launched is not converged. A peer that never answers is
// reported failed, and apply says so rather than assuming the fleet is up.
func TestApplySilentPeerFails(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	inbox := applierOn(t, "ch", "applier")
	f := applyFixture(
		peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("mute", func(p *FormationPeer) { p.Machine = ShortHostname() }),
	)
	fk := &answeringForker{t: t, inbox: inbox, quiet: map[string]bool{"mute": true}}
	rep, err := applyWith(t, f, ApplyOptions{Wait: 300 * time.Millisecond}, fk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Converged() {
		t.Fatal("a silent peer must not count as converged")
	}
	for _, r := range rep.Results {
		switch r.Alias {
		case "mute":
			if r.Outcome != OutcomeFailed || !strings.Contains(r.Detail, "did not answer") {
				t.Errorf("mute: outcome=%v detail=%q", r.Outcome, r.Detail)
			}
		case "orchestrator":
			if !r.Answered {
				t.Errorf("orchestrator answered and should be marked so: %+v", r)
			}
		}
	}
}

// TestApplyWaitZeroDoesNotPoll: --wait 0 launches and returns; peers are not marked
// failed for not having answered a question nobody waited for.
func TestApplyWaitZeroDoesNotPoll(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }))
	rep, err := applyWith(t, f, ApplyOptions{Wait: 0}, &recForker{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Results[0].Outcome != OutcomeTemplated || rep.Results[0].Answered {
		t.Errorf("--wait 0: %+v", rep.Results[0])
	}
	if !rep.Converged() {
		t.Error("--wait 0 must not report failure for an answer it never waited for")
	}
}

// TestApplyLaunchFailureIsReported: a forker error is a failed peer, not a crash.
func TestApplyLaunchFailureIsReported(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }))
	fk := &recForker{err: os.ErrPermission}
	rep, err := applyWith(t, f, ApplyOptions{}, fk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Results[0].Outcome != OutcomeFailed || !strings.Contains(rep.Results[0].Detail, "launch failed") {
		t.Errorf("result = %+v", rep.Results[0])
	}
	// the reservation must not survive a failed launch
	if dirExists(filepath.Join(CBUSDir(), "ch", "orchestrator")) {
		t.Error("a failed launch must release the alias it reserved")
	}
}

// TestApplyReservesTemplateAlias: a fresh peer's name is claimed before it boots, so
// its window title and its alias agree and two applies cannot race for it.
func TestApplyReservesTemplateAlias(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }))
	if _, err := applyWith(t, f, ApplyOptions{}, &recForker{}, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(CBUSDir(), "ch", "orchestrator", "meta.json"))
	if err != nil {
		t.Fatalf("template peer's alias was not reserved: %v", err)
	}
	if !strings.Contains(string(b), `"sessionId": "reserved"`) {
		t.Errorf("reservation meta = %s", b)
	}
}

// TestApplyPeerProfile: a peer is relaunched under the profile it was recorded with
// — the same derivation used to FIND its transcript, so the two cannot disagree.
func TestApplyPeerProfile(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("orchestrator", func(p *FormationPeer) {
		p.Machine = ShortHostname()
		p.Profile = "work"
	}))
	fk := &recForker{}
	if _, err := applyWith(t, f, ApplyOptions{}, fk, nil); err != nil {
		t.Fatal(err)
	}
	s := fk.specs[0]
	if s.Argv[0] != "ccs" || s.Argv[1] != "work" {
		t.Errorf("argv must launch under the peer's profile: %v", s.Argv)
	}
	if got := s.Env["CLAUDE_CONFIG_DIR"]; got != "/Users/x/.ccs/instances/work" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want the peer's instance dir", got)
	}
	// a blank profile keeps the applier's
	f2 := applyFixture(peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }))
	fk2 := &recForker{}
	if _, err := applyWith(t, f2, ApplyOptions{}, fk2, nil); err != nil {
		t.Fatal(err)
	}
	if fk2.specs[0].Argv[1] != "personal" {
		t.Errorf("blank profile should keep the applier's: %v", fk2.specs[0].Argv)
	}
}

// TestApplyNeverLaunchesItself: the orchestrator that saved a formation is normally
// IN it, and it is the one running apply. It cannot be missing — it is here. Without
// this, an applier whose Monitor had died would plan a relaunch of its own alias and
// then fail on its own reservation.
func TestApplyNeverLaunchesItself(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "orchestrator") // joined, deliberately NOT armed
	f := applyFixture(
		peer("orchestrator", func(p *FormationPeer) { p.Machine = ShortHostname() }),
		peer("coder", func(p *FormationPeer) { p.Machine = ShortHostname() }),
	)
	fk := &recForker{}
	rep, err := applyWith(t, f, ApplyOptions{}, fk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := fk.aliases(); len(got) != 1 || got[0] != "coder" {
		t.Fatalf("launched %v — the applier must never launch itself", got)
	}
	for _, r := range rep.Results {
		if r.Alias != "orchestrator" {
			continue
		}
		if r.Outcome != OutcomePresent {
			t.Errorf("the applier must read present, got %v", r.Outcome)
		}
		if !strings.Contains(r.Detail, "running apply") {
			t.Errorf("detail should say why: %q", r.Detail)
		}
	}
	// and --dry-run must agree, or the rehearsal is not the play
	dry, err := applyWith(t, f, ApplyOptions{DryRun: true}, &recForker{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range dry.Results {
		if dry.Results[i].Outcome != rep.Results[i].Outcome {
			t.Errorf("%s: dry-run says %v, apply says %v", dry.Results[i].Alias,
				dry.Results[i].Outcome, rep.Results[i].Outcome)
		}
	}
}
