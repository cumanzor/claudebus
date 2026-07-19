package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"claudebus/internal/core"
)

// Outcome is what actually happened to a peer, as opposed to what was planned.
type Outcome string

const (
	OutcomePresent   Outcome = "present"   // already live; not touched
	OutcomeResumed   Outcome = "resumed"   // relaunched as itself
	OutcomeForked    Outcome = "forked"    // relaunched from a checkpoint
	OutcomeTemplated Outcome = "templated" // fresh session + brief
	OutcomeDegraded  Outcome = "degraded"  // templated when resume/fork was asked for
	OutcomeSkipped   Outcome = "skipped"
	OutcomeRefused   Outcome = "refused"
	OutcomeFailed    Outcome = "failed" // launched, never answered (or the launch errored)
)

// PeerResult is one peer's fate after apply ran.
type PeerResult struct {
	Alias    string
	Outcome  Outcome
	Detail   string
	Nonce    string
	Answered bool
}

// ApplyReport is the whole run.
type ApplyReport struct {
	Results []PeerResult
	Drift   []DriftFinding
	DryRun  bool
}

// Converged reports whether every peer apply launched answered its kickoff.
func (r *ApplyReport) Converged() bool {
	for _, res := range r.Results {
		if res.Outcome == OutcomeFailed {
			return false
		}
	}
	return true
}

// ApplyOptions are the CLI's knobs.
type ApplyOptions struct {
	Only    []string
	DryRun  bool
	Wait    time.Duration // how long to wait for kickoff answers; 0 = do not wait
	Brief   string        // effort brief, verbatim into every kickoff
	Channel string        // per-run channel override (a template serves any effort); "" keeps the envelope's
}

// overrideChannel applies the per-run --channel override to the IN-MEMORY formation,
// once, before anything reads f.Channel. Every downstream read — the applier-presence
// check, the roster/liveSids gather, reconcile, the kickoff join lines and reply-to
// address, the alias reservations — then targets the override, with no straggler on
// the envelope's own channel (H6). The envelope file is never written, so the
// override lasts exactly this run.
func overrideChannel(f *Formation, opts ApplyOptions) error {
	if opts.Channel == "" {
		return nil
	}
	if !core.ValidName(opts.Channel) {
		return fmt.Errorf("--channel must be [A-Za-z0-9._-], got %q", opts.Channel)
	}
	f.Channel = opts.Channel
	return nil
}

// Apply reconciles a formation against the live channel: it launches the peers that
// are missing, briefs each one, and verifies convergence by round-trip.
//
// Sequential and anchor-first: peers come up one at a time, the anchor first, so a
// formation whose members expect an orchestrator to already be listening gets one.
// Nothing is launched until the whole plan is decided (BuildPlan) — a refusal must
// not arrive halfway through a fleet.
//
// Convergence is a round-trip and nothing else. Each kickoff carries a nonce and
// asks for it back; apply then reads its OWN inbox for the answers. The roster's
// listen/off marker is never consulted for this: it has lied in both directions in
// the field, which is the entire reason this rule exists.
func Apply(f *Formation, opts ApplyOptions, forker TerminalForker) (*ApplyReport, error) {
	if err := overrideChannel(f, opts); err != nil {
		return nil, err
	}
	// A dry-run sends no kickoffs, so it needs no reply-to and no joined applier: a
	// new user can preview a template before joining anything. A real apply briefs
	// peers to answer THIS session, so it must be a peer first.
	var self string
	if !opts.DryRun {
		var err error
		if self, err = applierAddress(f.Channel); err != nil {
			return nil, err
		}
	}
	if bad := UnknownAliases(f, opts.Only); len(bad) > 0 {
		return nil, fmt.Errorf("--only names no such peer: %s", strings.Join(bad, ", "))
	}
	world, err := GatherPlanWorld(f.Channel)
	if err != nil {
		return nil, err
	}
	return applyWorld(f, opts, forker, world, self)
}

// applyWorld is Apply once the live state is in hand — the seam tests inject a
// world through, so a launch table does not need real transcripts on disk.
func applyWorld(f *Formation, opts ApplyOptions, forker TerminalForker, world *PlanWorld, self string) (*ApplyReport, error) {
	plan := BuildPlan(f, world, opts.Only)
	rep := &ApplyReport{Drift: plan.Drift, DryRun: opts.DryRun}

	if len(plan.Launchable()) == 0 {
		// D13: an empty fleet is not a success. Report every peer's reason, then fail —
		// "converged 0 peers" reads like it worked, and that is how a silent skip
		// becomes a restore nobody notices did nothing.
		for _, pp := range plan.Peers {
			rep.Results = append(rep.Results, PeerResult{Alias: pp.Peer.Alias, Outcome: outcomeOf(pp), Detail: pp.Reason})
		}
		if opts.DryRun {
			return rep, nil
		}
		return rep, fmt.Errorf("nothing to launch: no peer in %q is startable on this host — see the reasons above", f.Name)
	}

	// Run-level layout facts: a file declaring ANY right/down suppresses the tmux
	// normalize for every pane fork this run (see ForkSpec.NoNormalize), and each
	// pane fork anchors on the largest-area pane among the applier's own surface
	// and the panes created so far (ties newest-first, so the applier stays big).
	declared := fileDeclaresSplit(f)
	var created []string
	for _, pp := range order(plan, f.AnchorAlias) {
		switch pp.Action {
		case ActionPresent, ActionSkip, ActionRefuse:
			rep.Results = append(rep.Results, PeerResult{Alias: pp.Peer.Alias, Outcome: outcomeOf(pp), Detail: pp.Reason})
			continue
		}
		nonce := kickoffNonce(pp.Peer.Alias)
		res := PeerResult{Alias: pp.Peer.Alias, Outcome: outcomeOf(pp), Detail: pp.Reason, Nonce: nonce}
		if opts.DryRun {
			rep.Results = append(rep.Results, res)
			continue
		}
		anchor := ""
		if launchTarget(pp.Peer.Target) == "pane" {
			anchor = PaneAnchor(created) // "" degrades to splitting the applier, never fails the launch
		}
		cid, err := launchPeer(f, pp, self, nonce, opts.Brief, forker, anchor, declared)
		if err != nil {
			res.Outcome, res.Detail = OutcomeFailed, "launch failed: "+err.Error()
		} else if cid != "" {
			created = append(created, cid)
		}
		rep.Results = append(rep.Results, res)
	}
	if opts.DryRun || opts.Wait <= 0 {
		return rep, nil
	}
	awaitConvergence(rep, self, opts.Wait)
	return rep, nil
}

// order returns the plan's peers with the anchor first. Everything else keeps the
// envelope's order, so a hand-authored file's sequence is honored.
func order(plan *Plan, anchor string) []PeerPlan {
	if anchor == "" {
		return plan.Peers
	}
	out := make([]PeerPlan, 0, len(plan.Peers))
	for _, pp := range plan.Peers {
		if pp.Peer.Alias == anchor {
			out = append(out, pp)
		}
	}
	for _, pp := range plan.Peers {
		if pp.Peer.Alias != anchor {
			out = append(out, pp)
		}
	}
	return out
}

func outcomeOf(pp PeerPlan) Outcome {
	switch pp.Action {
	case ActionPresent:
		return OutcomePresent
	case ActionResume:
		return OutcomeResumed
	case ActionFork:
		return OutcomeForked
	case ActionSkip:
		return OutcomeSkipped
	case ActionRefuse:
		return OutcomeRefused
	default:
		if pp.Degraded {
			return OutcomeDegraded
		}
		return OutcomeTemplated
	}
}

// applierAddress is this session's own address on the channel. apply cannot run from
// outside it: design §5 has the applier join and arm before any launch, and the
// convergence poll reads this session's own inbox — without one there is nothing to
// answer, and nowhere for an answer to land.
func applierAddress(ch string) (string, error) {
	for _, reg := range ResolveSelf() {
		if reg.Channel == ch {
			return reg.Channel + "/" + reg.Alias, nil
		}
	}
	return "", fmt.Errorf("this session is not on %q — apply briefs peers to answer IT, so it must be a peer first: cbus join %s <alias>", ch, ch)
}

// fileDeclaresSplit reports whether ANY peer in the envelope declares an explicit
// split direction — the run-level fact that suppresses the tmux normalize (a
// per-window reflow one auto sibling could otherwise use to stomp the layout).
func fileDeclaresSplit(f *Formation) bool {
	for _, p := range f.Peers {
		if p.Split == "right" || p.Split == "down" {
			return true
		}
	}
	return false
}

// launchPeer places one peer's session in a terminal, returning the created
// surface id ("" when the backend cannot name one). The alias is reserved before
// the fork for a fresh spawn (the child reclaims it on join), exactly as spawn does.
func launchPeer(f *Formation, pp PeerPlan, self, nonce, brief string, forker TerminalForker, anchor string, noNormalize bool) (string, error) {
	p := pp.Peer
	// A template and a fork both launch a NOT-YET-EXISTENT session, so claim the alias
	// before it boots — the title and alias agree, and two applies cannot race for it —
	// and stamp the birth-record the reclaim will carry (cbus-m9l, D19). A template is
	// fresh; a fork is fork-born, so recording origin=fork is what makes a LATER restore
	// refuse to fork it again (R1) and template it instead. Both mint a new session id,
	// so there is no preserved birth-record at that id to overwrite.
	//
	// resume does NOT reserve, and must not: it reuses the SAME session id, and a
	// reservation placeholder would win over that id in birthForJoin and clobber the
	// resumed peer's preserved origin. TestApplyResumeNeverReserves pins this.
	model := peerModel(p) // resolved once: explicit, else the role file's MODEL: line
	reserved := false
	switch pp.Action {
	case ActionTemplate:
		if _, err := ReserveAlias(f.Channel, p.Alias, OriginFresh, model); err != nil {
			return "", err
		}
		reserved = true
	case ActionFork:
		if _, err := ReserveAlias(f.Channel, p.Alias, OriginFork, model); err != nil {
			return "", err
		}
		reserved = true
	}
	prompt := KickoffPrompt(f, pp, self, nonce, brief)
	spec := ForkSpec{
		Target:      launchTarget(p.Target),
		Argv:        peerLaunchArgv(pp, prompt, model),
		Env:         peerEnv(p.Profile),
		Dir:         launchDir(p.Cwd),
		Anchor:      anchor,
		Split:       p.Split,
		NoNormalize: noNormalize,
	}
	created, err := forker.Fork(spec)
	if err != nil {
		if reserved {
			Unreserve(f.Channel, p.Alias)
		}
		return "", err
	}
	return created, nil
}

func launchTarget(t string) string {
	if t == "" {
		return defaultTarget
	}
	return t
}

// launchDir is the peer's recorded cwd, falling back to the applier's when the file
// has none or the directory is gone (a moved repo should not stop a restore).
func launchDir(recorded string) string {
	if recorded != "" && dirExists(recorded) {
		return recorded
	}
	return cwd()
}

// peerModel resolves the model apply launches a peer on: an explicit peer.model
// wins, else the committed role file's MODEL: line (the deferral that lets a template
// carry no models and inherit them from roles/*.md, D21), else empty — the CLI's own
// default. Same MODEL:-line defaulting spawn --role applies (cbus-ijx.9).
func peerModel(p *FormationPeer) string {
	if p.Model != "" {
		return p.Model
	}
	if p.Rolefile != "" {
		name, _ := parseRolefile(p.Rolefile)
		if _, model, err := LoadRole(name); err == nil {
			return model
		}
	}
	return ""
}

// peerLaunchArgv builds the launch per mode. The distinction IS the feature:
// resume continues the session as itself, fork checkpoints it into a copy, template
// starts blank. --name fixes alias and window title at the only moment the tool
// controls identity (spawn time); model is the resolved peerModel, never a guess.
func peerLaunchArgv(pp PeerPlan, prompt, model string) []string {
	p := pp.Peer
	argv := launchPrefix(p.Profile)
	switch pp.Action {
	case ActionResume:
		argv = append(argv, "--resume", p.SessionID)
	case ActionFork:
		argv = append(argv, "--resume", p.SessionID, "--fork-session")
	}
	if model != "" {
		argv = append(argv, "--model", model)
	}
	argv = append(argv, "--name", p.Alias)
	if prompt != "" {
		argv = append(argv, prompt)
	}
	return argv
}

// peerEnv replicates PATH and points CLAUDE_CONFIG_DIR at the peer's own profile
// instance — the same derivation transcriptRoots uses to FIND that peer's
// transcript, so the session we resume and the profile we relaunch it under cannot
// disagree. A blank profile keeps the applier's.
func peerEnv(profile string) map[string]string {
	env := forkReplicatedEnv()
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	if profile != "" && core.ValidName(profile) && strings.Contains(cfg, "/.ccs/instances/") {
		env["CLAUDE_CONFIG_DIR"] = filepath.Join(filepath.Dir(cfg), profile)
	}
	return env
}

// kickoffNonce is the token a peer must echo back for its launch to count as
// converged. It is per-peer and carries the alias, so an answer from the wrong
// session cannot satisfy another peer's round-trip.
func kickoffNonce(alias string) string {
	return "cbus-ok-" + alias + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// awaitConvergence waits for each launched peer to answer its nonce, then marks the
// silent ones failed.
//
// It is a bounded read of a file with a deadline, NEVER an exec of the tail
// follower: `cbus tail` blocks in the follower loop forever, so running one here
// would hang apply until the process was killed.
// Reading is non-destructive — the applier's Monitor still receives every frame,
// because a tail follows the file and this only ever opens it for reading.
func awaitConvergence(rep *ApplyReport, self string, wait time.Duration) {
	pending := map[string]int{} // nonce -> index into Results
	for i, res := range rep.Results {
		if res.Nonce != "" && res.Outcome != OutcomeFailed {
			pending[res.Nonce] = i
		}
	}
	if len(pending) == 0 {
		return
	}
	ch, alias, _ := strings.Cut(self, "/")
	inbox := filepath.Join(CBUSDir(), ch, alias, "inbox.jsonl")
	deadline := time.Now().Add(wait)
	for len(pending) > 0 && time.Now().Before(deadline) {
		for _, text := range readInbox(inbox) {
			for nonce, i := range pending {
				if strings.Contains(text, nonce) {
					rep.Results[i].Answered = true
					delete(pending, nonce)
				}
			}
		}
		if len(pending) == 0 {
			break
		}
		sleep := pollInterval
		if rem := time.Until(deadline); rem < sleep { // never overshoot the deadline
			sleep = rem
		}
		if sleep <= 0 {
			break
		}
		time.Sleep(sleep)
	}
	for nonce, i := range pending {
		rep.Results[i].Outcome = OutcomeFailed
		rep.Results[i].Detail = fmt.Sprintf("launched, but did not answer within %s "+
			"(the window may still be booting; check it, then re-run apply — it reconciles)", wait)
		_ = nonce
	}
}

// pollInterval is how often the inbox is re-read while waiting. A local file read is
// cheap; a session takes tens of seconds to boot, so this is not a hot loop.
const pollInterval = time.Second

// readInbox reads every line's text field, tolerating a torn last line (the writer
// appends, so a partial line is normal and transient).
func readInbox(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // a brief can be long
	for sc.Scan() {
		var m core.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		out = append(out, m.Text)
	}
	return out
}
