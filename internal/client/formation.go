package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"claudebus/internal/core"
)

// FormationSchema is the envelope's schema id. The trailing /v1 IS the format
// version: one field encodes one fact, so a hand-edit cannot leave a "version"
// int disagreeing with the schema id (design r2 §2 shows no parallel version key).
const FormationSchema = "cbus-formation/v1"

// formationsDir is the envelope subdir under $CBUS_DIR. Dot-prefixed so the
// channel walkers (ResolveSelf, Prune, list) skip it — they enumerate $CBUS_DIR
// children and treat every non-dot dir as a channel.
const formationsDir = ".formations"

// Per-peer recreation modes (design r2 §3). origin records how a peer was BORN;
// mode records how to bring it back. They are different facts and split on
// purpose: the B31 console peer was legitimately fork-born, and restoring it BY
// fork is what produced the ghost-orchestrator failure.
const (
	ModeResume   = "resume"   // literal --resume <sid>: the peer continues as itself
	ModeFork     = "fork"     // --resume <sid> --fork-session: checkpoint semantics
	ModeTemplate = "template" // fresh spawn + role brief
)

// Peer origins.
const (
	OriginFresh  = "fresh"  // spawned blank
	OriginFork   = "fork"   // forked off another session's transcript
	OriginJoined = "joined" // a pre-existing session that joined the channel
)

// onStale policies when a recorded transcript is gone (r1 semantics, unchanged).
const (
	OnStaleTemplate = "template"
	OnStaleSkip     = "skip"
	OnStaleFail     = "fail"
)

// Formation is a saved snapshot of a channel's shape: its peers, their roles and
// models, and how to relaunch them (design r2 §2). Unknown keys are preserved
// verbatim through a save so hand-edited fields survive a refresh, and Payload is
// opaque — the tool never interprets a pointer inside it and never shells out to
// whatever store it names (§4).
type Formation struct {
	Schema       string                     `json:"schema"`
	Name         string                     `json:"name"`
	Channel      string                     `json:"channel"`
	Host         *string                    `json:"host"`
	AnchorAlias  string                     `json:"anchorAlias"`
	SavedAt      string                     `json:"savedAt"`
	SavedBy      string                     `json:"savedBy"`
	DriftAnchors map[string]json.RawMessage `json:"drift_anchors"`
	Payload      json.RawMessage            `json:"payload"`
	// FormationRunID identifies the RUN this snapshot was taken from (bdx-mec.2).
	// Channel names are reused, so a name is not run identity. Additive under the
	// UNCHANGED cbus-formation/v1 schema: Validate matches the schema string exactly,
	// so bumping it would make older binaries reject new snapshots — the opposite of
	// backward compatible. Older binaries instead carry this key through their Extra
	// map untouched, verified by TestOldBinaryPreservesFormationRunID.
	FormationRunID string          `json:"formationRunId"`
	Peers          []FormationPeer `json:"peers"`

	// Extra holds top-level keys this version does not know, so a hand-edit
	// survives save/load. Never emitted through the typed fields.
	Extra map[string]json.RawMessage `json:"-"`
}

// FormationPeer is one peer's recreation record. Field names follow design r2 §2
// verbatim, including its mixed drift_anchors/anchorAlias casing — the spec is the
// contract; normalizing it here would fork the schema from the document.
type FormationPeer struct {
	Alias     string   `json:"alias"`
	Model     string   `json:"model"`
	Rolefile  string   `json:"rolefile"` // committed prompt pinned at a commit: roles/coder.md@b3a806e
	Role      *string  `json:"role"`     // freeform fallback for formations without committed roles
	Origin    string   `json:"origin"`
	Mode      string   `json:"mode"`
	SessionID string   `json:"sessionId"`
	OnStale   string   `json:"onStale"`
	Profile   string   `json:"profile"`
	Cwd       string   `json:"cwd"`
	Target    string   `json:"target"`
	Split     string   `json:"split"` // pane layout: ""/auto (largest-area chain) | right | down; hand-maintained, save never records it
	Machine   string   `json:"machine"`
	Addresses []string `json:"addresses"` // reserved: v1 apply prints these as manual joins
	// the run this peer's sessionId belongs to; blank when the snapshot predates the
	// ledger or the run is genuinely unknown, never inferred
	FormationRunID string `json:"formationRunId"`

	Extra map[string]json.RawMessage `json:"-"`
}

// fields returns the envelope's known keys in emission order. It is the single
// source for BOTH emission order and the known-key set unknownKeys strips, so the
// two cannot drift; formation_test's tag guard asserts it covers every json tag.
func (f *Formation) fields() []jsonField {
	peers := f.Peers
	if peers == nil {
		peers = []FormationPeer{}
	}
	return []jsonField{
		{"schema", f.Schema},
		{"name", f.Name},
		{"channel", f.Channel},
		{"host", f.Host},
		{"anchorAlias", f.AnchorAlias},
		{"savedAt", f.SavedAt},
		{"savedBy", f.SavedBy},
		{"drift_anchors", f.DriftAnchors},
		{"payload", f.Payload},
		{"formationRunId", f.FormationRunID},
		{"peers", peers},
	}
}

func (p *FormationPeer) fields() []jsonField {
	addrs := p.Addresses
	if addrs == nil {
		addrs = []string{}
	}
	return []jsonField{
		{"alias", p.Alias},
		{"model", p.Model},
		{"rolefile", p.Rolefile},
		{"role", p.Role},
		{"origin", p.Origin},
		{"mode", p.Mode},
		{"sessionId", p.SessionID},
		{"onStale", p.OnStale},
		{"profile", p.Profile},
		{"cwd", p.Cwd},
		{"target", p.Target},
		{"split", p.Split},
		{"machine", p.Machine},
		{"addresses", addrs},
		{"formationRunId", p.FormationRunID},
	}
}

func (f Formation) MarshalJSON() ([]byte, error) {
	return marshalOrdered(f.fields(), f.Extra)
}

func (p FormationPeer) MarshalJSON() ([]byte, error) {
	return marshalOrdered(p.fields(), p.Extra)
}

func (f *Formation) UnmarshalJSON(b []byte) error {
	type plain Formation // shed the custom marshaler to avoid recursion
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	extra, err := unknownKeys(b, knownKeys((&Formation{}).fields()))
	if err != nil {
		return err
	}
	*f = Formation(v)
	f.Extra = extra
	return nil
}

func (p *FormationPeer) UnmarshalJSON(b []byte) error {
	type plain FormationPeer
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	extra, err := unknownKeys(b, knownKeys((&FormationPeer{}).fields()))
	if err != nil {
		return err
	}
	*p = FormationPeer(v)
	p.Extra = extra
	return nil
}

// jsonField is one key and its value, in emission order.
type jsonField struct {
	key string
	val any
}

func knownKeys(fields []jsonField) map[string]bool {
	m := make(map[string]bool, len(fields))
	for _, f := range fields {
		m[f.key] = true
	}
	return m
}

// unknownKeys returns the object's keys that this version does not model. nil
// (not an empty map) when there are none, so a round-trip of a file with no
// hand-edits carries no allocation and no emitted difference.
func unknownKeys(b []byte, known map[string]bool) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	for k := range raw {
		if known[k] {
			delete(raw, k)
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return raw, nil
}

// marshalNoEscape marshals a value with encoding/json's HTML escaping OFF. The
// default turns < > & into < > &, which is valid JSON and unreadable
// prose — and this envelope is a file humans write in. A role brief saying "if a < b"
// or a note with "A & B" would come back mangled from the tool that promised to
// preserve it. The escaping buys nothing here: nobody embeds a formation in a <script>.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil // Encode appends one
}

// marshalOrdered emits a compact JSON object: known fields in declaration order,
// then unknown hand-edited keys sorted (deterministic, so a re-save converges to
// the same bytes). Compact is deliberate — encoding/json re-indents a custom
// marshaler's output, and Save indents the whole envelope in one pass.
func marshalOrdered(fields []jsonField, extra map[string]json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	write := func(k string, raw []byte) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(raw)
	}
	for _, f := range fields {
		raw, err := marshalNoEscape(f.val)
		if err != nil {
			return nil, err
		}
		write(f.key, raw)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		write(k, extra[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// SidState classifies a peer's recorded transcript.
type SidState int

const (
	SidNone      SidState = iota // nothing recorded to resume from
	SidPresent                   // a transcript for this sid exists on this machine
	SidStale                     // recorded, and no transcript found
	SidUnchecked                 // recorded on another machine — not this host's to judge
)

// SidState reports whether the peer's recorded transcript still exists. A peer
// tagged with another machine reads UNCHECKED, never stale: this host cannot see
// that host's transcripts, so calling the sid stale would dress a guess as a
// finding. "reserved" is the ReserveAlias placeholder, not a session.
func (p *FormationPeer) SidState() (state SidState, detail string) {
	if p.SessionID == "" || p.SessionID == "reserved" {
		return SidNone, ""
	}
	if path, ok := TranscriptPath(p.Profile, p.SessionID); ok {
		return SidPresent, path
	}
	if p.Machine != "" && p.Machine != ShortHostname() {
		return SidUnchecked, "recorded on " + p.Machine
	}
	return SidStale, "no transcript found on this machine"
}

// RoleTODO reports a peer whose brief would be empty at apply time: no committed
// rolefile, and no freeform role text — or text still carrying a TODO marker,
// which is what a peer that was asked to self-describe and never did leaves behind.
func (p *FormationPeer) RoleTODO() bool {
	if p.Rolefile != "" {
		return false
	}
	if p.Role == nil || strings.TrimSpace(*p.Role) == "" {
		return true
	}
	return strings.Contains(strings.ToUpper(*p.Role), "TODO")
}

// FormationEntry is one saved envelope as `list` sees it: loaded, or the reason it
// would not load. An unreadable file is listed WITH its error rather than skipped —
// an envelope that quietly vanishes from the listing is worse than one that shows
// up broken, because the file is still sitting there.
type FormationEntry struct {
	Name string
	F    *Formation
	Err  error
}

// ListFormations returns every saved envelope, name-sorted (os.ReadDir order). No
// formations dir means none saved, which is not an error.
func ListFormations() ([]FormationEntry, error) {
	dir := filepath.Join(CBUSDir(), formationsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []FormationEntry
	for _, e := range entries {
		// skip the .formation.tmp.<pid> write-in-progress and any dot file
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		f, lerr := LoadFormation(name)
		out = append(out, FormationEntry{Name: name, F: f, Err: lerr})
	}
	return out, nil
}

// RemoveFormation deletes a saved envelope, returning the path it removed.
// RemoveFormation deletes a RUNTIME envelope. It never touches a committed template
// (D22): a name that exists only in the repo gets a loud refusal pointing at git, so
// rm cannot delete a version-controlled file out from under the working tree.
func RemoveFormation(name string) (path string, err error) {
	path, err = FormationPath(name)
	if err != nil {
		return "", err
	}
	if !fileExists(path) {
		if dir, ok := repoFormationsDir(); ok && fileExists(filepath.Join(dir, name+".json")) {
			return "", fmt.Errorf("%q is a committed template (%s), not a runtime formation — remove it via git, not cbus",
				name, filepath.Join(dir, name+".json"))
		}
		return "", fmt.Errorf("no formation %q (looked in %s)", name, filepath.Dir(path))
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Dir(path)) // rmdir if empty, like the channel dirs
	return path, nil
}

// FormationPath is the envelope's path: $CBUS_DIR/.formations/<name>.json.
func FormationPath(name string) (string, error) {
	if !core.ValidName(name) {
		return "", fmt.Errorf("formation name must be [A-Za-z0-9._-]")
	}
	return filepath.Join(CBUSDir(), formationsDir, name+".json"), nil
}

// LoadFormation reads and validates a saved envelope.
//
// The filename and the name field must agree. They are two copies of one identity,
// and Save derives its path from the field: a file loaded under one name while
// carrying another would write itself over an unrelated formation (copy roles.json
// to backup.json, load "backup", save — roles.json is gone). Refusing here is the
// loud half of that fix; adopting the requested name instead would silently revert
// a hand-edited field, which is the one thing this envelope promises never to do.
func LoadFormation(name string) (*Formation, error) {
	path, err := FormationPath(name)
	if err != nil {
		return nil, err
	}
	f, err := loadFormationFileAt(path, name)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no formation %q (looked in %s)", name, filepath.Dir(path))
	}
	return f, err
}

// loadFormationFileAt reads one envelope file and validates it, enforcing name==
// filename (C1) — in the repo root as well as the runtime store (H2). A missing file
// returns the raw os error so a caller can distinguish not-found (fall through to
// another root) from torn/invalid (stop, never silently skip a broken file).
func loadFormationFileAt(path, name string) (*Formation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Formation
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if f.Name != name {
		return nil, fmt.Errorf("%s: records name %q — the filename and the name field must agree; "+
			"to rename, set name to %q, or `mv %s.json %s.json` to keep the name field",
			path, f.Name, name, name, f.Name)
	}
	return &f, nil
}

// repoFormationsDir is <git-toplevel>/formations, the committed-template root — the
// same git-top discovery LoadRole and branch use. Absent outside a repo. No shell or
// env expansion anywhere (H8): filepath.Join over a git-reported path and a
// ValidName-screened name, never a user string passed to a shell.
func repoFormationsDir() (string, bool) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", false
	}
	return filepath.Join(top, formationsDirName), true
}

// formationsDirName is the repo subdir holding committed templates (sibling to
// roles/, commands/). Distinct from formationsDir, the dot-prefixed runtime subdir.
const formationsDirName = "formations"

// ResolveFormation loads a formation by name, the runtime store FIRST, then the
// repo's committed templates. Runtime wins on a name in both: a saved instance is the
// user's own live state and shadows a committed starter of the same name (D20). This
// is the OPPOSITE precedence from LoadRole (repo-first) on purpose — a role's
// canonical home is the committed repo, an envelope's is the runtime store, and the
// repo is only a starter source for envelopes. Returns a human label for the source
// so a shadowed template is a stated behavior, not a surprise.
func ResolveFormation(name string) (f *Formation, source string, err error) {
	rt, err := FormationPath(name)
	if err != nil {
		return nil, "", err
	}
	f, err = loadFormationFileAt(rt, name)
	if err == nil {
		return f, "runtime store (" + rt + ")", nil
	}
	if !os.IsNotExist(err) {
		return nil, "", err // a torn/invalid runtime file stops here — never fall through to the repo
	}
	if dir, ok := repoFormationsDir(); ok {
		rp := filepath.Join(dir, name+".json")
		f, err = loadFormationFileAt(rp, name)
		if err == nil {
			return f, "committed template (" + rp + ")", nil
		}
		if !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("no formation %q (looked in the runtime store %s and the repo's %s/)",
		name, filepath.Dir(rt), formationsDirName)
}

// Save writes the envelope atomically (sibling temp + rename, the writeMeta
// dance): a concurrent reader sees old-or-new, never torn. It validates first —
// a file that cannot be loaded back is not worth writing.
func (f *Formation) Save() error {
	if err := f.Validate(); err != nil {
		return err
	}
	path, err := FormationPath(f.Name)
	if err != nil {
		return err
	}
	// NOT json.MarshalIndent: it would re-escape < > & on the way out, undoing
	// marshalNoEscape. An Encoder carries the setting through, indents, and ends the
	// file with a newline like the text file it is.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return err
	}
	b := buf.Bytes()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".formation.tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Validate checks the envelope's shape. It rules on THIS file's internal
// consistency only; the cross-peer refusals that gate a relaunch (a sid recorded
// under two aliases, forking a fork-born peer) belong to the apply plan, where
// the live channel is also in scope.
func (f *Formation) Validate() error {
	if f.Schema != FormationSchema {
		return fmt.Errorf("unsupported schema %q (want %q)", f.Schema, FormationSchema)
	}
	if !core.ValidName(f.Name) {
		return fmt.Errorf("formation name must be [A-Za-z0-9._-], got %q", f.Name)
	}
	if !core.ValidName(f.Channel) {
		return fmt.Errorf("channel must be [A-Za-z0-9._-], got %q", f.Channel)
	}
	if f.AnchorAlias != "" && !core.ValidName(f.AnchorAlias) {
		return fmt.Errorf("anchorAlias must be [A-Za-z0-9._-], got %q", f.AnchorAlias)
	}
	seen := make(map[string]bool, len(f.Peers))
	anchorFound := f.AnchorAlias == ""
	for i := range f.Peers {
		p := &f.Peers[i]
		if err := p.validate(); err != nil {
			return fmt.Errorf("peer %d: %v", i, err)
		}
		if seen[p.Alias] {
			return fmt.Errorf("duplicate peer alias %q", p.Alias)
		}
		seen[p.Alias] = true
		if p.Alias == f.AnchorAlias {
			anchorFound = true
		}
	}
	if !anchorFound {
		return fmt.Errorf("anchorAlias %q is not one of the peers", f.AnchorAlias)
	}
	return nil
}

func (p *FormationPeer) validate() error {
	if !core.ValidName(p.Alias) {
		return fmt.Errorf("alias must be [A-Za-z0-9._-], got %q", p.Alias)
	}
	// same pre-screen Spawn applies: a flag-shaped token would be parsed as a flag
	// by the child CLI, which launches an instantly-closing window.
	if p.Model != "" && (!core.ValidName(p.Model) || strings.HasPrefix(p.Model, "-")) {
		return fmt.Errorf("bad model %q", p.Model)
	}
	if err := oneOf("mode", p.Mode, ModeResume, ModeFork, ModeTemplate); err != nil {
		return err
	}
	if err := oneOf("origin", p.Origin, OriginFresh, OriginFork, OriginJoined); err != nil {
		return err
	}
	if err := oneOf("onStale", p.OnStale, OnStaleTemplate, OnStaleSkip, OnStaleFail); err != nil {
		return err
	}
	if err := oneOf("target", p.Target, "window", "tab", "tmux", "pane"); err != nil {
		return err
	}
	if err := oneOf("split", p.Split, "auto", "right", "down"); err != nil {
		return err
	}
	return nil
}

// oneOf accepts an empty value (the field is unset — defaulting is the plan's
// job, at apply time) and rejects any non-empty value outside the set.
func oneOf(field, val string, allowed ...string) error {
	if val == "" {
		return nil
	}
	for _, a := range allowed {
		if val == a {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s, got %q", field, strings.Join(allowed, "|"), val)
}
