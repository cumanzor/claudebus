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

// RosterPeer is one peer as the live store records it: the four facts save can
// actually capture.
type RosterPeer struct {
	Alias     string
	SessionID string
	Cwd       string
	Machine   string // meta.host, which is ShortHostname() on the writing machine
	Listening bool
}

// ChannelRoster reads a channel's peers straight from the store. It deliberately
// does NOT prune: a formation is saved precisely when an effort pauses and its
// peers are dying or dead, and a roster that dropped them would empty the file it
// was asked to record. Liveness is reported, never acted on.
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
		out = append(out, RosterPeer{
			Alias:     alias,
			SessionID: m.SessionID,
			Cwd:       m.Cwd,
			Machine:   m.Host,
			Listening: MetaListenerAlive(metaPath),
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
}

// SaveFormation captures ch's topology into the named envelope, refreshing an
// existing file in place.
//
// What it captures is bounded by what the substrate records. meta.json holds a
// peer's alias, sessionId, cwd and host; there is no model, no role, no origin, no
// profile anywhere on disk. So save owns exactly four fields per peer and treats
// everything else as the human's: it fills a blank once, and never overwrites what
// it finds. That is what makes a re-save at a milestone boundary safe — it refreshes
// sid checkpoints without eating the prose around them.
//
// A peer in the file but no longer on the channel is KEPT, not dropped: a paused
// effort is the main thing a formation exists to hold.
func SaveFormation(name, ch string) (*Formation, *SaveReport, error) {
	path, err := FormationPath(name)
	if err != nil {
		return nil, nil, err
	}
	roster, err := ChannelRoster(ch)
	if err != nil {
		return nil, nil, err
	}
	rep := &SaveReport{}

	var f *Formation
	if fileExists(path) {
		// An existing file may hold hours of hand-authored prose. If it will not
		// load, that is a reason to stop, never a reason to replace it.
		if f, err = LoadFormation(name); err != nil {
			return nil, nil, fmt.Errorf("refusing to overwrite an envelope that will not load: %v", err)
		}
		if f.Channel != ch {
			return nil, nil, fmt.Errorf("formation %q records channel %q, refusing to re-point it at %q "+
				"(save it under a different name, or fix the channel field)", name, f.Channel, ch)
		}
	} else {
		rep.New = true
		f = &Formation{Schema: FormationSchema, Name: name, Channel: ch}
	}

	byAlias := make(map[string]*FormationPeer, len(f.Peers))
	for i := range f.Peers {
		byAlias[f.Peers[i].Alias] = &f.Peers[i]
	}
	onChannel := make(map[string]bool, len(roster))
	for _, r := range roster {
		onChannel[r.Alias] = true
		if p, ok := byAlias[r.Alias]; ok {
			capturePeer(p, r) // captured facts only; hand-edits survive
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
		capturePeer(&p, r)
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
	if f.AnchorAlias == "" {
		f.AnchorAlias = anchorDefault(ch, f.Peers)
	}
	setGitHeadAnchor(f)
	if err := f.Save(); err != nil {
		return nil, nil, err
	}
	return f, rep, nil
}

// capturePeer copies the four facts the store actually knows. Everything else on
// the peer is the human's and is left exactly as found.
func capturePeer(p *FormationPeer, r RosterPeer) {
	p.SessionID = r.SessionID
	p.Cwd = r.Cwd
	p.Machine = r.Machine
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
