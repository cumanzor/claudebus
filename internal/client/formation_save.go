package client

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"claudebus/internal/core"
)

// Save-side defaults. meta.json records a peer's alias, sessionId, cwd and host —
// and nothing else — so these four are POLICY, not captured facts (see SaveFormation).
// template/template is the safe pair: a template peer never touches a transcript, so
// a freshly saved formation cannot resume or fork anything until a human says so.
const (
	defaultMode    = ModeTemplate
	defaultOnStale = OnStaleTemplate
	defaultTarget  = "tab"
)

// roleTODOMarker is what save writes into a peer's role when there is nothing to
// capture. RoleTODO() would flag an absent role anyway; the literal marker is for
// the human editing the file, who needs to see the blank that wants filling.
const roleTODOMarker = "TODO: set rolefile to roles/<alias>.md@<commit>, or replace this with the peer's brief"

// RosterPeer is one peer as the live store records it. Origin/Model are the m9l
// birth-record — present only when the launcher stamped them, blank otherwise.
type RosterPeer struct {
	Alias     string
	SessionID string
	Cwd       string
	Machine   string // meta.host, which is ShortHostname() on the writing machine
	Origin    string
	Model     string
	Profile   string // the CCS instance the session stamped about itself at join
	Listening bool
	RunID     string // the peer's run claim, read from its dir (blank when unclaimed)
	HasClaim  bool   // whether a claim file was present at all, distinct from RunID==""
}

// ChannelRoster reads a channel's peers straight from the store. It deliberately
// does NOT prune: a formation is saved precisely when an effort pauses and its
// peers are dying or dead, and a roster that dropped them would empty the file it
// was asked to record. Liveness is reported, never acted on.
// NOT ScanStore (roster.go), and the two must not be unified without a ruling: this
// one takes ONE channel, ERRORS when it is absent, and DROPS a peer whose meta.json is
// torn, because a save must never record a peer it could not read. ScanStore keeps
// that peer with blank fields, because `list` has always shown it rather than hiding
// it. Same walk, opposite answer to the same two questions.
func ChannelRoster(ch string) ([]RosterPeer, error) {
	if !core.ValidName(ch) {
		return nil, fmt.Errorf("channel must be [A-Za-z0-9._-]")
	}
	chDir := filepath.Join(CBUSDir(), ch)
	entries, err := os.ReadDir(chDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no channel %q on this machine", ch)
		}
		return nil, err
	}
	var out []RosterPeer
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		metaPath := filepath.Join(chDir, e.Name(), "meta.json")
		m, ok := ReadPeerMeta(metaPath)
		if !ok {
			continue // torn or missing — the same read-tolerance list applies
		}
		alias := m.Alias
		if alias == "" {
			alias = e.Name() // meta is authoritative, the dir name is the fallback
		}
		runID := readClaim(filepath.Join(chDir, e.Name()))
		out = append(out, RosterPeer{
			Alias:     alias,
			SessionID: m.SessionID,
			Cwd:       m.Cwd,
			Machine:   m.Host,
			Origin:    m.Origin,
			Model:     m.Model,
			Profile:   m.Profile,
			Listening: MetaListenerAlive(metaPath),
			RunID:     runID,
			HasClaim:  runID != "",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// SaveReport is what a save did, per peer, so the CLI can tell the user which
// hand-maintained fields are still waiting for them.
type SaveReport struct {
	Added   []string // on the channel, new to the file
	Updated []string // in both — captured facts refreshed, hand-edits untouched
	Kept    []string // in the file, not on the channel now — never dropped
	New     bool     // the envelope did not exist before
	// Skipped birth-record fills: a meta carried an origin/model outside what the
	// envelope will accept (hand-corrupted meta), so it was NOT propagated. Surfaced,
	// never silent (cbus-m9l G6).
	SkippedBirth []string
	// BasedOn is set when the refresh base came from a committed template rather than
	// a prior runtime save, so the CLI can say the starter was inherited.
	BasedOn string
	// RunConflict holds the distinct run ids found across the roster when there was
	// more than one — a split or corruption. The envelope run is left blank and the
	// CLI surfaces this rather than silently picking a run.
	RunConflict []string
	// AnchorMissing is set when a REFRESH still comes out anchorless (a legacy file
	// re-saved from outside the channel). Minting one refuses instead; this is the
	// path that has to save and say so.
	AnchorMissing bool
}

// loadRepoTemplate reads a committed template as a save refresh base. It reads the
// repo file, never writes it (H1): save always writes runtime. Not-found returns the
// os error so the caller can start fresh; the name==filename rule applies (H2).
func loadRepoTemplate(name string) (*Formation, error) {
	dir, ok := repoFormationsDir()
	if !ok {
		return nil, os.ErrNotExist
	}
	return loadFormationFileAt(filepath.Join(dir, name+".json"), name)
}

// SaveFormation captures ch's topology into the named envelope, refreshing an
// existing file in place.
//
// What it captures is bounded by what the substrate records. meta.json holds a
// peer's alias, sessionId, cwd and host, a profile when the session stamped one at
// join, and origin/model when the launcher recorded them; there is no role and no
// prose anywhere on disk. Save refreshes the session facts, fills the
// launcher-recorded ones once, and treats everything else as the human's: it never
// overwrites a hand edit. That is what makes a re-save at a milestone boundary
// safe — it refreshes sid checkpoints without eating the prose around them.
//
// A peer in the file but no longer on the channel is KEPT, not dropped: a paused
// effort is the main thing a formation exists to hold.
//
// anchors are the caller's --anchor pairs, written into drift_anchors (nil is
// fine). git_head is refused up front — it is the one machine-owned key.
func SaveFormation(name, ch string, anchors map[string]string) (*Formation, *SaveReport, error) {
	if err := checkHandAnchors(anchors); err != nil {
		return nil, nil, err
	}
	path, err := FormationPath(name)
	if err != nil {
		return nil, nil, err
	}
	// A NEW envelope is a name this client mints (.formations/<name>.json, which
	// ListFormations skips when dot-prefixed, so a dotted one saves and then cannot be
	// listed). Refreshing an EXISTING file is not a mint, so a legacy dotted envelope
	// can still be re-saved rather than stranded.
	if !fileExists(path) {
		if err := checkStoreName("formation name", name); err != nil {
			return nil, nil, err
		}
	}
	roster, err := ChannelRoster(ch)
	if err != nil {
		return nil, nil, err
	}
	rep := &SaveReport{}

	var f *Formation
	switch {
	case fileExists(path):
		// An existing runtime file may hold hours of hand-authored prose. If it will
		// not load, that is a reason to stop, never a reason to replace it.
		if f, err = LoadFormation(name); err != nil {
			return nil, nil, fmt.Errorf("refusing to overwrite an envelope that will not load: %v", err)
		}
	default:
		// No runtime file. A committed template of the same name MAY serve as the
		// refresh base, so an apply-then-save cycle inherits its hand-authored fields
		// (rolefile refs survive). save still WRITES runtime only (H1) — the repo file
		// is read, never touched. Absent even there, start fresh.
		base, berr := loadRepoTemplate(name)
		switch {
		case berr == nil:
			f = base
			rep.BasedOn = "committed template"
		case os.IsNotExist(berr):
			f = &Formation{Schema: FormationSchema, Name: name, Channel: ch}
		default:
			return nil, nil, fmt.Errorf("cannot base on the committed template %q: %v", name, berr)
		}
		rep.New = true // a new runtime file either way
	}
	if f.Channel != ch {
		return nil, nil, fmt.Errorf("formation %q records channel %q, refusing to re-point it at %q "+
			"(save it under a different name, or fix the channel field)", name, f.Channel, ch)
	}

	byAlias := make(map[string]*FormationPeer, len(f.Peers))
	for i := range f.Peers {
		byAlias[f.Peers[i].Alias] = &f.Peers[i]
	}
	onChannel := make(map[string]bool, len(roster))
	for _, r := range roster {
		onChannel[r.Alias] = true
		if p, ok := byAlias[r.Alias]; ok {
			capturePeer(p, r, rep) // captured facts only; hand-edits survive
			rep.Updated = append(rep.Updated, r.Alias)
			continue
		}
		p := FormationPeer{
			Alias:     r.Alias,
			Mode:      defaultMode,
			OnStale:   defaultOnStale,
			Target:    defaultTarget,
			Role:      strPtr(roleTODOMarker),
			Addresses: []string{},
		}
		capturePeer(&p, r, rep)
		f.Peers = append(f.Peers, p)
		rep.Added = append(rep.Added, r.Alias)
	}
	for i := range f.Peers {
		if !onChannel[f.Peers[i].Alias] {
			rep.Kept = append(rep.Kept, f.Peers[i].Alias)
		}
	}

	f.SavedAt = Now()
	f.SavedBy = savedBy(ch)
	// Envelope run identity is derived from the UNIQUE claims of the ROSTER being
	// saved, dead peers included. Save exists precisely for a pausing effort whose
	// peers are dying, so reading only live claims (currentRun) would blank the run
	// of a channel whose dead dirs still hold a valid claim. First-claim-wins is also
	// wrong: if two distinct claims are present the run is ambiguous (a split or
	// corruption), and a save must surface that rather than pick one.
	rosterRun := map[string]string{}
	distinct := map[string]bool{}
	for _, r := range roster {
		rosterRun[r.Alias] = r.RunID
		if r.RunID != "" {
			distinct[r.RunID] = true
		}
	}
	switch len(distinct) {
	case 0:
		f.FormationRunID = "" // no claim anywhere: unknown, never inferred
	case 1:
		for id := range distinct {
			f.FormationRunID = id
		}
	default:
		ids := make([]string, 0, len(distinct))
		for id := range distinct {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		rep.RunConflict = ids // surfaced to the caller; the envelope run stays blank
		f.FormationRunID = ""
	}
	// Per ON-CHANNEL peer, assign its own claim UNCONDITIONALLY, including clearing a
	// stale FormationRunID to blank when the peer holds no claim (old binary, failed
	// claim, or pre-ledger). Retaining a prior id on a reused alias would silently
	// carry the OLD run forward, which violates blank-when-unknown. A kept OFF-channel
	// peer is deliberately untouched: this run is not its run.
	for i := range f.Peers {
		if onChannel[f.Peers[i].Alias] {
			f.Peers[i].FormationRunID = rosterRun[f.Peers[i].Alias]
		}
	}
	if f.AnchorAlias == "" {
		f.AnchorAlias = anchorDefault(ch, f.Peers)
	}
	// The anchor is load-bearing for restore, so an envelope without one is a defect.
	// A NEW envelope refuses rather than auto-picking a peer: a wrong anchor becomes
	// the wrong restore seat every time the formation is resumed, which outlasts a
	// failed save. A REFRESH still saves — refusing would strand legacy files saved
	// from outside the channel — and a re-save from a joined session heals it through
	// anchorDefault above, so the fleet converges without a migration.
	if f.AnchorAlias == "" {
		if rep.New {
			return nil, nil, fmt.Errorf("refusing to save %q with no anchor: this session is not a peer on %q, "+
				"so there is no alias to anchor to — join %s and save again (the saving session becomes the anchor), "+
				"or write anchorAlias by hand into %s", name, ch, ch, path)
		}
		rep.AnchorMissing = true
	}
	setGitHeadAnchor(f)
	setHandAnchors(f, anchors)
	if err := f.Save(); err != nil {
		return nil, nil, err
	}
	return f, rep, nil
}

// checkHandAnchors refuses the machine-owned key before any store work: git_head
// is recorded from the repo at save (setGitHeadAnchor), so a hand value would be
// silently overwritten on the very next save — refusing beats lying.
func checkHandAnchors(anchors map[string]string) error {
	if _, ok := anchors["git_head"]; ok {
		return fmt.Errorf("--anchor git_head is machine-owned (recorded from the repo at save time); pick another key")
	}
	return nil
}

// setHandAnchors writes the caller's anchor pairs. An explicit pair overwrites a
// same-named key already in the file: the preservation rule protects hand edits
// from the MACHINE, and a flag is the hand acting.
func setHandAnchors(f *Formation, anchors map[string]string) {
	if len(anchors) == 0 {
		return
	}
	if f.DriftAnchors == nil {
		f.DriftAnchors = map[string]json.RawMessage{}
	}
	for k, v := range anchors {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		f.DriftAnchors[k] = b
	}
}

// capturePeer copies what the store knows onto a peer. The three always-known facts
// (sessionId, cwd, machine) refresh in place. The birth-record (origin, model) is
// filled ONCE, on the m9l fill rules:
//   - fill only when the envelope field is BLANK and the meta value is non-blank;
//   - a hand-set envelope value always wins (no overwrite, no re-fill on a later save);
//   - a blank meta never clobbers a set field;
//   - a meta value the envelope would reject (G6: origin outside the enum, a
//     flag-shaped model) is NOT propagated — it is skipped and surfaced in the report,
//     so a corrupted meta cannot ride a garbage birth-record into the file silently.
func capturePeer(p *FormationPeer, r RosterPeer, rep *SaveReport) {
	p.SessionID = r.SessionID
	p.Cwd = r.Cwd
	p.Machine = r.Machine

	// Profile is a session fact the session stamps about itself at join, so it
	// refreshes like cwd — but a blank meta (pre-profile binary, non-CCS host) never
	// clobbers a hand-filled envelope, and a token the envelope would reject is
	// skipped and surfaced like a corrupted birth-record.
	if r.Profile != "" {
		if core.ValidName(r.Profile) {
			p.Profile = r.Profile
		} else {
			rep.SkippedBirth = append(rep.SkippedBirth, fmt.Sprintf("%s: profile %q (meta not a usable profile token)", r.Alias, r.Profile))
		}
	}

	if p.Origin == "" && r.Origin != "" {
		if validOrigin(r.Origin) {
			p.Origin = r.Origin
		} else {
			rep.SkippedBirth = append(rep.SkippedBirth, fmt.Sprintf("%s: origin %q (meta not one of fresh|fork|joined)", r.Alias, r.Origin))
		}
	}
	if p.Model == "" && r.Model != "" {
		if validModel(r.Model) {
			p.Model = r.Model
		} else {
			rep.SkippedBirth = append(rep.SkippedBirth, fmt.Sprintf("%s: model %q (meta not a usable model token)", r.Alias, r.Model))
		}
	}
}

func validOrigin(o string) bool {
	return o == OriginFresh || o == OriginFork || o == OriginJoined
}

// validModel is the same screen FormationPeer.validate applies, so a captured model
// can never make the envelope fail its own Validate.
func validModel(m string) bool {
	return core.ValidName(m) && !strings.HasPrefix(m, "-")
}

func strPtr(s string) *string { return &s }

// savedBy is this session's address on ch, falling back to the machine when the
// saver is not itself a peer (a formation can be saved from outside the channel).
func savedBy(ch string) string {
	for _, reg := range ResolveSelf() {
		if reg.Channel == ch {
			return reg.Channel + "/" + reg.Alias
		}
	}
	return ShortHostname() + " (not joined)"
}

// anchorDefault picks the saving session's own alias as the anchor — apply launches
// anchor-first, and the session running save is normally the orchestrator. It stays
// hand-editable; save only fills the blank.
func anchorDefault(ch string, peers []FormationPeer) string {
	for _, reg := range ResolveSelf() {
		if reg.Channel != ch {
			continue
		}
		for i := range peers {
			if peers[i].Alias == reg.Alias {
				return reg.Alias
			}
		}
	}
	return ""
}

// setGitHeadAnchor records the cheap drift anchor: the git HEAD at save time, which
// apply diffs and reports. Hand-written anchors (notes, anything else) are left
// alone — only git_head is ours. Outside a repo the key is not written at all
// rather than recorded as an empty string that would later read as drift.
func setGitHeadAnchor(f *Formation) {
	head, ok := gitHead()
	if !ok {
		return
	}
	if f.DriftAnchors == nil {
		f.DriftAnchors = map[string]json.RawMessage{}
	}
	b, err := json.Marshal(head)
	if err != nil {
		return
	}
	f.DriftAnchors["git_head"] = b
}

func gitHead() (string, bool) {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", false
	}
	head := strings.TrimSpace(string(out))
	if head == "" {
		return "", false
	}
	return head, true
}
