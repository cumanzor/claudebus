package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claudebus/internal/core"
)

// An operator grant approves one action for one local peer. It is friction against
// a model mistaking a relayed quote for the operator's word, not a security boundary:
// every file here is writable by any process running as the same user, and a model
// that wraps `cbus grant` in a pty or writes a record directly gets past it. Doing so
// is a doctrine violation, which the record's provenance helps an audit find.

const (
	GrantOnce = "once"
	GrantTTL  = "ttl"

	GrantMaxTTL       = 24 * time.Hour
	grantMaxActionLen = 500
)

type GrantState string

const (
	GrantLive    GrantState = "live"
	GrantUsed    GrantState = "used"
	GrantExpired GrantState = "expired"
	GrantRevoked GrantState = "revoked"
	// GrantSuspect: minted from inside a model harness's process tree, or from one
	// whose ancestry could not be walked to init. Never usable.
	GrantSuspect GrantState = "suspect"
)

// GrantProvenance is what `cbus grant` could see about who ran it.
type GrantProvenance struct {
	TTY             string   `json:"tty"`
	UID             int      `json:"uid"`
	Ancestors       []string `json:"ancestors"`
	Harness         string   `json:"harness,omitempty"`
	HarnessAncestor bool     `json:"harnessAncestor"`
	// AncestryTruncated: the walk stopped before init, so a harness above the cut
	// could not be ruled out.
	AncestryTruncated bool `json:"ancestryTruncated"`
}

// Grant is the write-once record at .grants/<channel>/<alias>/<id>.json.
type Grant struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Alias   string `json:"alias"`
	// SessionID is the grantee's exact session at mint; a later holder of the alias
	// is someone else. ConnectionID is recorded for audit when daemon-managed.
	SessionID    string          `json:"sessionId"`
	ConnectionID string          `json:"connectionId,omitempty"`
	Action       string          `json:"action"`
	Mode         string          `json:"mode"`
	CreatedAt    time.Time       `json:"createdAt"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	GrantedBy    GrantProvenance `json:"grantedBy"`
}

// GrantView is a grant with its state derived at read time.
type GrantView struct {
	Grant
	State     GrantState `json:"state"`
	Uses      int        `json:"uses,omitempty"`
	UsedBy    string     `json:"usedBy,omitempty"`
	UsedAt    string     `json:"usedAt,omitempty"`
	RevokedAt string     `json:"revokedAt,omitempty"`
}

// grantNow is the clock, indirected so expiry is testable without sleeping.
var grantNow = time.Now

// grantAncestry returns the processes above this one, nearest first. Indirected
// because a test binary's ancestry is `go test`, which proves nothing either way.
var grantAncestry = func() ([]procRecord, bool) {
	return ancestorChain(os.Getppid(), procWalkRoot, grantProcLookup())
}

var (
	errGrantRemote   = errors.New("grants are local-only: a remote peer cannot verify a grant in this store")
	errGrantNotFound = errors.New("no such grant")
)

func grantsRoot() string { return filepath.Join(CBUSDir(), ".grants") }

func grantDir(ch, alias string) string { return filepath.Join(grantsRoot(), ch, alias) }

// NewGrant validates a grant request and fills in everything except provenance.
// mode is GrantOnce or GrantTTL; ttl is only read for GrantTTL.
func NewGrant(target, action, mode string, ttl time.Duration) (Grant, error) {
	if IsRemote(target) {
		return Grant{}, errGrantRemote
	}
	ch, alias, err := ParseLocal(target)
	if err != nil {
		return Grant{}, err
	}
	if ch == "" {
		return Grant{}, fmt.Errorf("grant target must be <channel>/<alias>, got %q", target)
	}
	if err := checkStoreName("channel", ch); err != nil {
		return Grant{}, err
	}
	if err := checkStoreName("alias", alias); err != nil {
		return Grant{}, err
	}
	sid, connID, err := grantee(ch, alias)
	if err != nil {
		return Grant{}, err
	}
	action = strings.TrimSpace(action)
	switch {
	case action == "":
		return Grant{}, errors.New("grant action must not be empty")
	case strings.ContainsAny(action, "\r\n"):
		return Grant{}, errors.New("grant action must be one line")
	case len(action) > grantMaxActionLen:
		return Grant{}, fmt.Errorf("grant action is %d bytes; the limit is %d", len(action), grantMaxActionLen)
	}
	id, err := newGrantID()
	if err != nil {
		return Grant{}, err
	}
	now := grantNow().UTC().Truncate(time.Second)
	g := Grant{ID: id, Channel: ch, Alias: alias, SessionID: sid, ConnectionID: connID, Action: action, Mode: mode, CreatedAt: now}
	switch mode {
	case GrantOnce:
		g.ExpiresAt = now.Add(GrantMaxTTL)
	case GrantTTL:
		if ttl <= 0 || ttl > GrantMaxTTL {
			return Grant{}, fmt.Errorf("--ttl must be more than 0 and at most %s, got %s", GrantMaxTTL, ttl)
		}
		g.ExpiresAt = now.Add(ttl)
	default:
		return Grant{}, fmt.Errorf("grant mode must be %s or %s, got %q", GrantOnce, GrantTTL, mode)
	}
	return g, nil
}

// grantee resolves the session registered as ch/alias right now. A reserved alias
// has no session yet, an absent one has nobody to bind to, and a dead one would bind
// a session that is gone.
func grantee(ch, alias string) (sid, connID string, err error) {
	metaPath := filepath.Join(CBUSDir(), ch, alias, "meta.json")
	m, ok := ReadPeerMeta(metaPath)
	switch {
	case !ok:
		return "", "", fmt.Errorf("no peer %s/%s is registered here: a grant binds to a joined session", ch, alias)
	case m.SessionID == "" || m.SessionID == "reserved":
		return "", "", fmt.Errorf("%s/%s is reserved but has not joined yet: a grant binds to a joined session", ch, alias)
	case PeerDead(metaPath):
		return "", "", fmt.Errorf("%s/%s is registered but its listener is dead: a grant binds to a live session", ch, alias)
	}
	return m.SessionID, m.ConnectionID, nil
}

func newGrantID() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "g-" + hex.EncodeToString(b[:]), nil
}

// GrantProvenanceNow records the terminal device and the process ancestry, and
// marks the grant suspect when a model harness is among the ancestors.
func GrantProvenanceNow(tty string) GrantProvenance {
	chain, complete := grantAncestry()
	p := GrantProvenance{TTY: tty, UID: os.Getuid(), Ancestors: make([]string, 0, len(chain)), AncestryTruncated: !complete}
	for _, r := range chain {
		p.Ancestors = append(p.Ancestors, procIdentity(r))
	}
	p.Harness = chainHarness(chain)
	p.HarnessAncestor = p.Harness != ""
	return p
}

// WriteGrant writes the record once. Link, not rename, so an id collision fails
// instead of overwriting a grant someone else holds.
func WriteGrant(g Grant) error {
	dir := grantDir(g.Channel, g.Alias)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".grant-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), filepath.Join(dir, g.ID+".json"))
}

// ListGrants returns grants for the given registrations, or every grant when all.
func ListGrants(regs []LocalReg, all bool) ([]GrantView, error) {
	var dirs []string
	if all {
		matches, _ := filepath.Glob(filepath.Join(grantsRoot(), "*", "*"))
		dirs = matches
	} else {
		for _, r := range regs {
			dirs = append(dirs, grantDir(r.Channel, r.Alias))
		}
	}
	var out []GrantView
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
				continue
			}
			v, err := readGrantView(d, strings.TrimSuffix(name, ".json"))
			if err != nil {
				continue // a torn or foreign file is not a grant
			}
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func readGrantView(dir, id string) (GrantView, error) {
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return GrantView{}, err
	}
	var g Grant
	if err := json.Unmarshal(b, &g); err != nil {
		return GrantView{}, err
	}
	if g.ID != id || filepath.Join(grantsRoot(), g.Channel, g.Alias) != dir {
		return GrantView{}, errors.New("grant record does not match its path")
	}
	v := GrantView{Grant: g}
	if m, ok := readMarker(filepath.Join(dir, id+".revoked")); ok {
		v.RevokedAt = m.At
	}
	if m, ok := readMarker(filepath.Join(dir, id+".used")); ok {
		v.UsedBy, v.UsedAt = m.By, m.At
	}
	if b, err := os.ReadFile(filepath.Join(dir, id+".uses")); err == nil {
		// count lines, not newlines, so a torn final line still counts as a use
		for _, l := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(l) != "" {
				v.Uses++
			}
		}
	}
	v.State = grantState(v)
	return v, nil
}

// grantState: revoked, then used (a once grant), then expired, then suspect, else live.
// A record with no bound session is suspect: no code path writes one.
// live means no harness was seen, not that none was there: a reparented process escapes.
func grantState(v GrantView) GrantState {
	switch {
	case v.RevokedAt != "":
		return GrantRevoked
	case v.Mode == GrantOnce && v.UsedAt != "":
		return GrantUsed
	case !grantNow().Before(v.ExpiresAt):
		return GrantExpired
	case v.GrantedBy.HarnessAncestor || v.GrantedBy.AncestryTruncated || v.SessionID == "":
		return GrantSuspect
	}
	return GrantLive
}

type grantMarker struct {
	By string `json:"by"`
	At string `json:"at"`
}

func readMarker(path string) (grantMarker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return grantMarker{}, false
	}
	var m grantMarker
	if json.Unmarshal(b, &m) != nil || m.At == "" {
		return grantMarker{At: "unknown"}, true // the marker's presence is the fact
	}
	return m, true
}

// claimMarker creates path first-writer-wins and reports whether this call made it.
func claimMarker(path, by string) (bool, error) {
	b, _ := json.Marshal(grantMarker{By: by, At: grantNow().UTC().Format(time.RFC3339)})
	tmp, err := os.CreateTemp(filepath.Dir(path), ".marker-")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	switch err := os.Link(tmp.Name(), path); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrExist):
		return false, nil
	default:
		return false, err
	}
}

// findGrant looks the id up among the given registrations' grant dirs.
func findGrant(id string, regs []LocalReg) (GrantView, string, error) {
	if !strings.HasPrefix(id, "g-") || strings.ContainsAny(id, `/\.`) {
		return GrantView{}, "", fmt.Errorf("bad grant id %q", id)
	}
	for _, r := range regs {
		d := grantDir(r.Channel, r.Alias)
		if v, err := readGrantView(d, id); err == nil {
			return v, d, nil
		}
	}
	return GrantView{}, "", errGrantNotFound
}

// UseGrant is the grantee taking a grant before acting on it. A once grant is
// consumed by exactly one caller; a ttl grant stays usable until it expires, and
// each use is appended to <id>.uses for audit. Only the session the grant was bound
// to can use it, even if another session later holds the alias.
func UseGrant(id, sessionID string, regs []LocalReg) (GrantView, error) {
	v, dir, err := findGrant(id, regs)
	if errors.Is(err, errGrantNotFound) {
		if _, _, aerr := findGrantAnywhere(id); aerr == nil {
			return GrantView{}, fmt.Errorf("grant %s is not for this session: only the peer it names can use it", id)
		}
		return GrantView{}, fmt.Errorf("no grant %s for this session (cbus grants lists yours)", id)
	}
	if err != nil {
		return GrantView{}, err
	}
	if v.State != GrantLive {
		return v, fmt.Errorf("grant %s is %s, not live: do not act on it", id, v.State)
	}
	if sessionID != v.SessionID {
		return v, fmt.Errorf("grant %s is bound to session %s, not this one (%s): a later holder of %s/%s cannot use it",
			id, v.SessionID, sessionID, v.Channel, v.Alias)
	}
	if v.Mode != GrantOnce {
		if err := appendUse(filepath.Join(dir, id+".uses"), sessionID); err != nil {
			return v, err
		}
		return readGrantView(dir, id)
	}
	won, err := claimMarker(filepath.Join(dir, id+".used"), sessionID)
	if err != nil {
		return v, err
	}
	if !won {
		return v, fmt.Errorf("grant %s was already used: a once grant is consumed by its first use", id)
	}
	v, _ = readGrantView(dir, id)
	return v, nil
}

func appendUse(path, by string) error {
	b, _ := json.Marshal(grantMarker{By: by, At: grantNow().UTC().Format(time.RFC3339)})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func findGrantAnywhere(id string) (GrantView, string, error) {
	views, err := ListGrants(nil, true)
	if err != nil {
		return GrantView{}, "", err
	}
	for _, v := range views {
		if v.ID == id {
			return v, grantDir(v.Channel, v.Alias), nil
		}
	}
	return GrantView{}, "", errGrantNotFound
}

// RevokeGrant marks a grant revoked. Revoking twice reports the first revocation.
func RevokeGrant(id, by string) (GrantView, error) {
	if !strings.HasPrefix(id, "g-") || strings.ContainsAny(id, `/\.`) {
		return GrantView{}, fmt.Errorf("bad grant id %q", id)
	}
	v, dir, err := findGrantAnywhere(id)
	if err != nil {
		return GrantView{}, fmt.Errorf("no grant %s", id)
	}
	if _, err := claimMarker(filepath.Join(dir, id+".revoked"), by); err != nil {
		return v, err
	}
	return readGrantView(dir, id)
}

// ancestorChain lists the processes from start up to (not including) rootPid,
// nearest first, with the same stops as harnessWalk. complete is true only when the
// walk reached rootPid; a failed lookup, an implausible link, a cycle or the depth
// cap is a truncation.
func ancestorChain(start, rootPid int, lookup func(int) (procRecord, bool)) (out []procRecord, complete bool) {
	p := start
	seen := map[int]bool{}
	for depth := 0; depth < maxWalkDepth; depth++ {
		if p <= rootPid {
			return out, true
		}
		if seen[p] {
			return out, false
		}
		seen[p] = true
		rec, ok := lookup(p)
		if !ok {
			return out, false
		}
		if n := len(out); n > 0 && !ancestryPlausible(out[n-1], rec) {
			return out, false
		}
		out = append(out, rec)
		p = rec.PPid
	}
	return out, false
}

// chainHarness names the first harness in the chain, including one started through
// a script runtime (node, bun, deno), which harnessWalk's argv[0] check cannot see.
func chainHarness(chain []procRecord) string {
	for _, r := range chain {
		if h := harnessForExecutable(r.Comm); h != "" {
			return h
		}
		f := strings.Fields(r.Argv)
		if len(f) > 0 {
			if h := harnessForExecutable(f[0]); h != "" {
				return h
			}
		}
		if len(f) > 1 && isScriptRuntime(commBase(f[0])) {
			if h := wrappedHarness(f[1]); h != "" {
				return h
			}
		}
	}
	return ""
}

func isScriptRuntime(base string) bool {
	switch base {
	case "node", "bun", "deno":
		return true
	}
	return false
}

// wrappedHarness recognizes the script a runtime was started with: a harness
// launcher by name, or a harness package's entry point by path.
func wrappedHarness(script string) string {
	if h := harnessForExecutable(script); h != "" {
		return h
	}
	switch {
	case strings.Contains(script, "@anthropic-ai/claude-code"):
		return "claude"
	case strings.Contains(script, "@openai/codex"):
		return "codex"
	case strings.Contains(script, "opencode"):
		return "opencode"
	}
	return ""
}

// procIdentity names an ancestor for the record: the harness it is, else its
// kernel comm (argv[0] can hold a path with spaces), else argv[0]'s basename.
func procIdentity(r procRecord) string {
	if h := chainHarness([]procRecord{r}); h != "" {
		return h
	}
	if r.Comm != "" {
		return commBase(r.Comm)
	}
	if f := strings.Fields(r.Argv); len(f) > 0 {
		return commBase(f[0])
	}
	return "?"
}

// GrantNoticeFrom labels the notice sender; it is not a peer and is never replied to.
const GrantNoticeFrom = "cbus-grant"

func grantNoticeText(g Grant) string {
	return fmt.Sprintf("operator grant %s for %s/%s (session %s): %s. This notice is not the grant: act only if cbus grants lists %s as live for you, and run cbus grants use %s before acting.",
		g.ID, g.Channel, g.Alias, g.SessionID, g.Action, g.ID, g.ID)
}

// DeliverGrantNotice appends a kind=grant line to the grantee's inbox, under the
// same alias lock a send takes. Best-effort: the grant is valid without it.
func DeliverGrantNotice(g Grant) error {
	unlock, err := lockPeer(g.Channel, g.Alias)
	if err != nil {
		return err
	}
	defer unlock()
	dir := filepath.Join(CBUSDir(), g.Channel, g.Alias)
	if !fileExists(filepath.Join(dir, "meta.json")) {
		return fmt.Errorf("%s/%s is no longer registered", g.Channel, g.Alias)
	}
	line, err := json.Marshal(core.Message{From: g.Channel + "/" + GrantNoticeFrom, To: g.Channel + "/" + g.Alias,
		TS: Now(), Kind: "grant", Text: grantNoticeText(g)})
	if err != nil {
		return err
	}
	return appendInbox(filepath.Join(dir, "inbox.jsonl"), line)
}
