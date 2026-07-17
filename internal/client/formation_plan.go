package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PeerAction is what apply will do with one peer.
type PeerAction int

const (
	ActionPresent  PeerAction = iota // already live on the channel — reconcile leaves it alone
	ActionResume                     // --resume <sid>
	ActionFork                       // --resume <sid> --fork-session
	ActionTemplate                   // fresh spawn + brief
	ActionSkip                       // deliberately not launched (--only, another machine, onStale=skip)
	ActionRefuse                     // a prohibition fired; apply launches nothing for this peer
)

func (a PeerAction) String() string {
	switch a {
	case ActionPresent:
		return "present"
	case ActionResume:
		return "resume"
	case ActionFork:
		return "fork"
	case ActionTemplate:
		return "template"
	case ActionSkip:
		return "skip"
	default:
		return "refuse"
	}
}

// PeerPlan is one peer's decided fate. Reason is populated for everything except a
// plain launch: a skip or a refusal that cannot say why is how a silent no-op gets
// mistaken for success.
type PeerPlan struct {
	Peer     *FormationPeer
	Action   PeerAction
	Reason   string
	Degraded bool // wanted resume/fork; onStale sent it to template instead
}

// DriftFinding is one anchor that moved between save and apply. Reported loudly,
// never blocking: the snapshot is a cache, the ground is live, and which one is
// "right" is the operator's call, not the tool's.
type DriftFinding struct {
	Anchor string
	Saved  string
	Now    string
}

// Plan is the whole decision, made before anything is launched.
type Plan struct {
	Formation *Formation
	Peers     []PeerPlan
	Drift     []DriftFinding
}

// Launchable reports the peers apply would actually start.
func (p *Plan) Launchable() []PeerPlan {
	var out []PeerPlan
	for _, pp := range p.Peers {
		switch pp.Action {
		case ActionResume, ActionFork, ActionTemplate:
			out = append(out, pp)
		}
	}
	return out
}

// Refusals reports the peers a prohibition stopped.
func (p *Plan) Refusals() []PeerPlan {
	var out []PeerPlan
	for _, pp := range p.Peers {
		if pp.Action == ActionRefuse {
			out = append(out, pp)
		}
	}
	return out
}

// PlanWorld is the live state a plan reasons over, gathered once up front so the
// decisions themselves are a pure function — testable without a bus, a repo, a
// transcript store, or a terminal. Every prohibition in this file is therefore a
// table test rather than a live rehearsal, which is the point: the B31 restore
// failed on a DECISION (fork the wrong transcript), not on a terminal.
type PlanWorld struct {
	Host          string            // ShortHostname()
	GitHead       string            // current short HEAD; "" outside a repo
	Roster        []RosterPeer      // who is on the channel right now
	LiveSids      map[string]string // sid -> "channel/alias" of a LIVE-ARMED holder
	Self          string            // this session's alias on the channel, if it is a peer
	HasTranscript func(profile, sid string) bool
}

// GatherPlanWorld collects the live state. This is the I/O half; BuildPlan is the
// decision half. An ABSENT channel is an empty roster, not an error: applying a
// template to a channel nobody has joined yet (the first thing a new user does) must
// plan every peer as missing, not fail. ChannelRoster stays strict for save, which
// genuinely cannot snapshot a channel that does not exist.
func GatherPlanWorld(ch string) (*PlanWorld, error) {
	var roster []RosterPeer
	if dirExists(filepath.Join(CBUSDir(), ch)) {
		var err error
		if roster, err = ChannelRoster(ch); err != nil {
			return nil, err
		}
	}
	head, _ := gitHead()
	self := ""
	for _, reg := range ResolveSelf() {
		if reg.Channel == ch {
			self = reg.Alias
			break
		}
	}
	return &PlanWorld{
		Host:          ShortHostname(),
		GitHead:       head,
		Roster:        roster,
		LiveSids:      liveSids(),
		Self:          self,
		HasTranscript: func(profile, sid string) bool { _, ok := TranscriptPath(profile, sid); return ok },
	}, nil
}

// liveSids maps every session id currently held by a LIVE-ARMED peer, anywhere on
// this machine's bus, to the address holding it. It is the structural-liveness input
// to the resume gate: a sid that is armed somewhere else is a session that is alive
// right now, and resuming it would attach a second process to one transcript.
//
// The limit, named rather than hidden: this sees sessions ON THE BUS. A live session
// that never joined, or left, is invisible here — so the gate proves "not alive on
// the bus", never "not alive".
func liveSids() map[string]string {
	out := map[string]string{}
	root := CBUSDir()
	channels, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, ch := range channels {
		if !ch.IsDir() || strings.HasPrefix(ch.Name(), ".") {
			continue
		}
		aliases, err := os.ReadDir(filepath.Join(root, ch.Name()))
		if err != nil {
			continue
		}
		for _, al := range aliases {
			if !al.IsDir() || strings.HasPrefix(al.Name(), ".") {
				continue
			}
			metaPath := filepath.Join(root, ch.Name(), al.Name(), "meta.json")
			m, ok := ReadPeerMeta(metaPath)
			if !ok || m.SessionID == "" || m.SessionID == "reserved" {
				continue
			}
			if MetaListenerAlive(metaPath) {
				out[m.SessionID] = ch.Name() + "/" + al.Name()
			}
		}
	}
	return out
}

// BuildPlan decides every peer's fate against the live world. Pure: same formation
// plus same world always yields the same plan, and it launches nothing.
//
// only (empty = everything) filters by alias, so --only and --dry-run share one code
// path with a real apply — a dry run that planned differently from the real thing
// would be a rehearsal of the wrong play.
func BuildPlan(f *Formation, w *PlanWorld, only []string) *Plan {
	plan := &Plan{Formation: f, Drift: driftFindings(f, w)}
	live := liveAliases(w.Roster)
	selected := map[string]bool{}
	for _, a := range only {
		selected[a] = true
	}
	dupSids := duplicateSids(f)

	for i := range f.Peers {
		p := &f.Peers[i]
		plan.Peers = append(plan.Peers, decidePeer(p, f, w, live, selected, dupSids))
	}
	return plan
}

// decidePeer is the whole decision for one peer, in the order the prohibitions must
// fire: selection, then machine, then the identity refusals, then liveness.
func decidePeer(p *FormationPeer, f *Formation, w *PlanWorld, live map[string]bool,
	selected map[string]bool, dupSids map[string]bool) PeerPlan {

	if len(selected) > 0 && !selected[p.Alias] {
		return PeerPlan{Peer: p, Action: ActionSkip, Reason: "not selected by --only"}
	}
	// The applier is never a peer to launch. It is running this code, so it is alive
	// by definition — no marker needed, and this is the one peer whose liveness cannot
	// lie. Without it, an applier that joined but never armed would plan a launch of
	// its OWN alias: the reservation refuses (it is this session's name), and a
	// formation whose orchestrator is in it could not be applied by that orchestrator,
	// which is the normal case.
	if p.Alias == w.Self {
		return PeerPlan{Peer: p, Action: ActionPresent, Reason: "this session — it is running apply"}
	}
	// Reconcile only: a peer already on the channel is left alone. This trusts the
	// listener marker, which is the only cheap signal available before launch — and
	// the marker has lied in the field, both ways. That is why convergence is proven
	// by round-trip AFTER launch and never by this flag.
	if live[p.Alias] {
		return PeerPlan{Peer: p, Action: ActionPresent, Reason: "already live on the channel"}
	}
	// Strict equality, never a fuzzy match: an empty machine means "here" (a
	// hand-authored file that omits it), anything else must equal this host exactly.
	if p.Machine != "" && p.Machine != w.Host {
		return PeerPlan{Peer: p, Action: ActionSkip,
			Reason: fmt.Sprintf("recorded on %q, this host is %q (cross-machine launch is not in v1)", p.Machine, w.Host)}
	}

	mode := p.Mode
	if mode == "" {
		mode = defaultMode
	}
	if mode == ModeTemplate {
		return PeerPlan{Peer: p, Action: ActionTemplate}
	}

	// --- everything below touches a transcript, so the identity checks gate it ---

	if p.SessionID == "" || p.SessionID == "reserved" {
		return onStaleFallback(p, mode, "mode is "+mode+" but no session is recorded")
	}
	// R2: one transcript claimed by two aliases. Whichever is wrong, launching both
	// would put two peers on one conversation.
	if dupSids[p.SessionID] {
		return PeerPlan{Peer: p, Action: ActionRefuse,
			Reason: fmt.Sprintf("session %s is recorded under more than one alias in this formation — "+
				"one of them is wrong; fix the file", p.SessionID)}
	}
	// R1: a fork-born peer's sid is its PARENT's transcript, not its own. Resuming or
	// forking it re-executes the parent's intent under this peer's name — the
	// ghost-orchestrator failure, where a fork of the orchestrator replayed dispatches
	// from an address nobody had joined.
	if p.Origin == OriginFork {
		return PeerPlan{Peer: p, Action: ActionRefuse,
			Reason: fmt.Sprintf("origin=fork means session %s is the PARENT's transcript, not this peer's; "+
				"%s would re-run the parent's intent as %q — restore fork-born peers with mode=template",
				p.SessionID, mode, p.Alias)}
	}
	// D12: an unrecorded origin is not evidence of a safe one. The origin/mode split
	// exists so the born-fact gets consulted; empty means nobody recorded it, and
	// resuming a transcript you cannot attribute is exactly how B31 got a ghost.
	if p.Origin == "" {
		return PeerPlan{Peer: p, Action: ActionRefuse,
			Reason: fmt.Sprintf("mode=%s needs origin recorded (fresh|fork|joined) — the tool cannot know "+
				"how %q was born, and a fork-born peer must never be resumed", mode, p.Alias)}
	}
	if !w.HasTranscript(p.Profile, p.SessionID) {
		return onStaleFallback(p, mode, "no transcript found for session "+p.SessionID)
	}
	// D14: the alive-check guards RESUME only. resume means "the peer continues as
	// itself", so a live original would put two processes on one transcript. fork is
	// the mode the design points at for exactly this case ("whenever the original may
	// still be alive") — a deliberate copy is legitimate, so the refusal names it as
	// the remedy. The sin is a silent conversion, not the fork.
	if mode == ModeResume {
		if at, ok := w.LiveSids[p.SessionID]; ok {
			return PeerPlan{Peer: p, Action: ActionRefuse,
				Reason: fmt.Sprintf("session %s is live-armed at %s — resume would attach a second process to "+
					"one transcript; stand the original down or re-point it, or set mode=fork if a copy of it "+
					"is what you want", p.SessionID, at)}
		}
		return PeerPlan{Peer: p, Action: ActionResume}
	}
	return PeerPlan{Peer: p, Action: ActionFork}
}

// onStaleFallback applies the peer's onStale policy when its transcript is gone.
// Degradation is always reported: a peer that came back as a blank template when
// resume was asked for is a different peer, and the operator has to know.
func onStaleFallback(p *FormationPeer, mode, why string) PeerPlan {
	policy := p.OnStale
	if policy == "" {
		policy = defaultOnStale
	}
	switch policy {
	case OnStaleSkip:
		return PeerPlan{Peer: p, Action: ActionSkip, Reason: why + " (onStale=skip)"}
	case OnStaleFail:
		return PeerPlan{Peer: p, Action: ActionRefuse, Reason: why + " (onStale=fail)"}
	default:
		return PeerPlan{Peer: p, Action: ActionTemplate, Degraded: true,
			Reason: why + " — degraded from " + mode + " to template (onStale=template)"}
	}
}

// liveAliases is the set of aliases currently armed on the channel.
func liveAliases(roster []RosterPeer) map[string]bool {
	out := make(map[string]bool, len(roster))
	for _, r := range roster {
		if r.Listening {
			out[r.Alias] = true
		}
	}
	return out
}

// duplicateSids finds session ids claimed by more than one peer in the envelope.
func duplicateSids(f *Formation) map[string]bool {
	seen := map[string]int{}
	for i := range f.Peers {
		sid := f.Peers[i].SessionID
		if sid == "" || sid == "reserved" {
			continue
		}
		seen[sid]++
	}
	dup := map[string]bool{}
	for sid, n := range seen {
		if n > 1 {
			dup[sid] = true
		}
	}
	return dup
}

// driftFindings diffs the cheap anchors the envelope recorded. v1 checks git_head:
// it is one command, and it is the anchor both hand-authored snapshots pinned.
// Anything else in drift_anchors is prose for a human and is left alone.
func driftFindings(f *Formation, w *PlanWorld) []DriftFinding {
	raw, ok := f.DriftAnchors["git_head"]
	if !ok || w.GitHead == "" {
		return nil
	}
	var saved string
	if json.Unmarshal(raw, &saved) != nil || saved == "" {
		return nil
	}
	if saved == w.GitHead {
		return nil
	}
	return []DriftFinding{{Anchor: "git_head", Saved: saved, Now: w.GitHead}}
}

// UnknownAliases reports --only names that are not peers in the formation, so a
// typo fails loudly instead of quietly selecting nothing.
func UnknownAliases(f *Formation, only []string) []string {
	known := make(map[string]bool, len(f.Peers))
	for i := range f.Peers {
		known[f.Peers[i].Alias] = true
	}
	var bad []string
	for _, a := range only {
		if !known[a] {
			bad = append(bad, a)
		}
	}
	sort.Strings(bad)
	return bad
}
