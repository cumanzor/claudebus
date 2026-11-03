package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"claudebus/internal/core"
)

// A permit is a coordinator's record of what the operator said a seat may do. Same-user
// file state written by a model: --says is an audit trail, not proof.

const (
	permitVersion     = 1
	PermitDefaultTTL  = 7 * 24 * time.Hour
	PermitMaxTTL      = 30 * 24 * time.Hour
	permitTextMax     = 2000
	permitTargetMax   = 200
	permitClassEffort = "effort"
	permitClassList   = "one of push-branch, draft-pr, pr-update, merge, release, install, effort"
)

const (
	PermitLive    = "live"
	PermitExpired = "expired"
	PermitRevoked = "revoked"
)

var (
	permitRefClasses    = map[string]bool{"push-branch": true, "draft-pr": true, "pr-update": true}
	permitTargetClasses = map[string]bool{"merge": true, "release": true, "install": true}
	permitPatternRe     = regexp.MustCompile(`^[A-Za-z0-9._/*?-]+$`)
	permitRefRe         = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	permitIDRe          = regexp.MustCompile(`^p-[0-9a-f]{10}$`)
	permitScpHostRe     = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	permitNow           = time.Now
)

// PermitUsageError is a request that could not be asked, as opposed to one the store
// answered: a seat can tell a typo from a no.
type PermitUsageError struct{ Msg string }

func (e PermitUsageError) Error() string { return e.Msg }

func permitUsage(format string, a ...any) error { return PermitUsageError{fmt.Sprintf(format, a...)} }

// Permit is the write-once record at .permits/<channel>/<id>.json. A field that narrows
// who may use it must raise V: this reader refuses a V above its own, so an old binary
// cannot read a narrowed permit as a wider one.
type Permit struct {
	V             int       `json:"v"`
	ID            string    `json:"id"`
	Channel       string    `json:"channel"`
	Actions       []string  `json:"actions"`
	Repo          string    `json:"repo"`
	Ref           string    `json:"ref,omitempty"`
	Target        string    `json:"target,omitempty"`
	Effort        string    `json:"effort,omitempty"`
	Says          string    `json:"says"`
	Writer        string    `json:"writer"`
	WriterSession string    `json:"writerSession"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// PermitView is a permit with its state derived at read time.
type PermitView struct {
	Permit
	State     string `json:"state"`
	Uses      int    `json:"uses,omitempty"`
	RevokedBy string `json:"revokedBy,omitempty"`
}

// PermitRequest is what cbus permit was asked to write.
type PermitRequest struct {
	Channel string
	Actions []string
	Repo    string
	Ref     string
	Target  string
	Effort  string
	Says    string
	TTL     time.Duration
}

func permitsRoot() string { return filepath.Join(CBUSDir(), ".permits") }

func permitDir(ch string) string { return filepath.Join(permitsRoot(), ch) }

// NormalizeRepo reduces a remote URL to host/path, so ssh and https of one repo compare
// equal and a credential in the URL is not stored. Path case is kept: a case difference
// fails closed.
func NormalizeRepo(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	var host, p string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return "", errors.New("remote is not a URL this can read")
		}
		switch u.Scheme {
		case "ssh", "git+ssh", "https", "http", "git":
		default:
			return "", fmt.Errorf("remote scheme %q is not a shared repo", u.Scheme)
		}
		host, p = u.Host, u.Path
		if h, port, ok := strings.Cut(host, ":"); ok {
			if port == "22" && strings.Contains(u.Scheme, "ssh") || port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
				host = h
			}
		}
	case strings.HasPrefix(s, "/") || strings.HasPrefix(s, "."):
		return "", errors.New("remote is a local path, not a shared repo")
	default:
		var err error
		if host, p, err = parseScpRemote(s); err != nil {
			return "", err
		}
	}
	host = strings.ToLower(host)
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	if host == "" || p == "" || strings.Contains(p, "..") || strings.ContainsAny(p, "@:") || strings.IndexFunc(host+p, func(r rune) bool { return !unicode.IsPrint(r) || r == ' ' }) >= 0 {
		return "", errors.New("remote has no plain host and path, and may hold a credential")
	}
	return host + "/" + p, nil
}

// parseScpRemote reads [user@]host:path. Anything looser could carry user:secret@host in
// what would be stored as the path. No error here or in NormalizeRepo prints the value.
func parseScpRemote(s string) (host, p string, err error) {
	left, right, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", errors.New("remote is not host:path or a URL")
	}
	if i := strings.LastIndex(left, "@"); i >= 0 {
		left = left[i+1:]
	}
	if !permitScpHostRe.MatchString(left) {
		return "", "", errors.New("remote is not host:path or a URL")
	}
	if len(left) == 1 {
		return "", "", errors.New("remote looks like a drive path, not a shared repo")
	}
	if strings.ContainsAny(right, `@:?#\`) {
		return "", "", errors.New("remote path has characters a repo path does not, and may hold a credential")
	}
	return left, right, nil
}

// OriginRepo is the origin remote of the working directory, as git reports it.
func OriginRepo() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", errors.New("cannot read the origin remote here; pass --repo")
	}
	return strings.TrimSpace(string(out)), nil
}

func normalizeRef(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "refs/heads/") }

func checkRefShape(s string, re *regexp.Regexp) error {
	switch {
	case !re.MatchString(s):
		return fmt.Errorf("ref %q has characters outside %s", s, re.String())
	case strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/"):
		return fmt.Errorf("ref %q may not start with - or start or end with /", s)
	case strings.Contains(s, ".."):
		return fmt.Errorf("ref %q may not contain ..", s)
	case strings.Contains(s, "**"):
		return fmt.Errorf("ref %q: ** is not supported, * stops at a slash", s)
	}
	return nil
}

// checkRefPattern refuses a pattern wide enough to be a standing push anywhere: no literal
// prefix, or one that reaches a default branch. An exact name is the operator naming it.
func checkRefPattern(p string) error {
	if err := checkRefShape(p, permitPatternRe); err != nil {
		return err
	}
	i := strings.IndexAny(p, "*?")
	if i < 0 {
		return nil
	}
	if i == 0 {
		return fmt.Errorf("ref pattern %q has no literal prefix and would cover almost anything", p)
	}
	for _, branch := range []string{"main", "master"} {
		if permitRefMatches(p, branch) {
			return fmt.Errorf("ref pattern %q would cover %s; name it exactly if that is what was said", p, branch)
		}
	}
	return nil
}

// permitRefMatches is path.Match: * and ? do not cross a slash.
func permitRefMatches(pattern, ref string) bool {
	ok, err := path.Match(pattern, ref)
	return err == nil && ok
}

// NewPermit validates a request and fills in its writer: the caller, who must hold an
// address some coordinator record on that channel names.
func NewPermit(req PermitRequest) (Permit, error) {
	if IsRemote(req.Channel) {
		return Permit{}, permitUsage("%q is a relay channel: permits are local to this machine", req.Channel)
	}
	if !core.ValidStoreName(req.Channel) {
		return Permit{}, permitUsage("bad channel %q", req.Channel)
	}
	says := cleanPermitText(req.Says)
	if says == "" {
		return Permit{}, permitUsage("--says is required: the operator's words")
	}
	if len(says) > permitTextMax || len(req.Effort) > permitTextMax {
		return Permit{}, permitUsage("--says and --effort are limited to %d bytes", permitTextMax)
	}
	p := Permit{V: permitVersion, Channel: req.Channel, Says: says, Effort: cleanPermitText(req.Effort)}
	if err := fillPermitScope(&p, req); err != nil {
		return Permit{}, err
	}
	repo, err := NormalizeRepo(req.Repo)
	if err != nil {
		return Permit{}, PermitUsageError{"--repo: " + err.Error()}
	}
	p.Repo = repo
	ttl := req.TTL
	if ttl == 0 {
		ttl = PermitDefaultTTL
	}
	if ttl < 0 || ttl > PermitMaxTTL {
		return Permit{}, permitUsage("--ttl must be positive and at most %s", PermitMaxTTL)
	}
	self := ""
	for _, reg := range ResolveSelf() {
		if reg.Channel == req.Channel {
			self = reg.Channel + "/" + reg.Alias
		}
	}
	if self == "" {
		return Permit{}, fmt.Errorf("this session is not on %s, so it coordinates nobody there", req.Channel)
	}
	if !coordinatesAnySeat(req.Channel, self) {
		return Permit{}, fmt.Errorf("%s is not the recorded coordinator of any seat on %s", self, req.Channel)
	}
	if p.ID, err = newPermitID(); err != nil {
		return Permit{}, err
	}
	now := permitNow().UTC()
	p.Writer, p.WriterSession, p.CreatedAt, p.ExpiresAt = self, SessionID(), now, now.Add(ttl)
	return p, nil
}

// cleanPermitText keeps model-written text from carrying terminal control bytes into the
// operator's audit view.
func cleanPermitText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case !unicode.IsPrint(r):
			return -1
		}
		return r
	}, s))
}

func fillPermitScope(p *Permit, req PermitRequest) error {
	if len(req.Actions) == 0 {
		return permitUsage("name at least one action class")
	}
	seen := map[string]bool{}
	var refs, targets, effort int
	for _, a := range req.Actions {
		switch {
		case seen[a]:
			continue
		case permitRefClasses[a]:
			refs++
		case permitTargetClasses[a]:
			targets++
		case a == permitClassEffort:
			effort++
		default:
			return permitUsage("unknown action class %q: %s", a, permitClassList)
		}
		seen[a] = true
		p.Actions = append(p.Actions, a)
	}
	switch {
	case targets > 0:
		if targets > 1 || refs > 0 || effort > 0 {
			return permitUsage("merge, release and install take one action per permit, bound to its target")
		}
		if req.Ref != "" || req.Effort != "" {
			return permitUsage("%s takes --target, not --ref or --effort", p.Actions[0])
		}
		t := strings.TrimSpace(req.Target)
		if t == "" {
			return permitUsage("%s needs --target naming what the operator named", p.Actions[0])
		}
		if len(t) > permitTargetMax || strings.ContainsAny(t, "*?[]\\ ") || strings.IndexFunc(t, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
			return permitUsage("--target %q must be one exact name with no wildcard", t)
		}
		p.Target = t
	case effort > 0:
		if refs > 0 {
			return permitUsage("effort goes in its own permit")
		}
		if req.Ref != "" || req.Target != "" {
			return permitUsage("effort takes --effort, not --ref or --target")
		}
		if p.Effort == "" {
			return permitUsage("effort needs --effort text")
		}
	default:
		if req.Target != "" || req.Effort != "" {
			return permitUsage("%s takes --ref, not --target or --effort", p.Actions[0])
		}
		ref := normalizeRef(req.Ref)
		if ref == "" {
			return permitUsage("%s needs --ref", p.Actions[0])
		}
		if err := checkRefPattern(ref); err != nil {
			return PermitUsageError{err.Error()}
		}
		p.Ref = ref
	}
	return nil
}

func newPermitID() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "p-" + hex.EncodeToString(b), nil
}

// WritePermit writes the record once. Link, not rename, so an id collision fails.
func WritePermit(p Permit) error {
	dir := permitDir(p.Channel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".permit-")
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
	return os.Link(tmp.Name(), filepath.Join(dir, p.ID+".json"))
}

func readPermitView(ch, id string) (PermitView, bool) {
	dir := permitDir(ch)
	b, err := readCoordinatorFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return PermitView{}, false
	}
	var p Permit
	if json.Unmarshal(b, &p) != nil || p.V != permitVersion || p.ID != id || p.Channel != ch || p.Writer == "" {
		return PermitView{}, false
	}
	v := PermitView{Permit: p, State: PermitLive}
	if !permitNow().Before(p.ExpiresAt) {
		v.State = PermitExpired
	}
	if m, ok := readMarker(filepath.Join(dir, id+".revoked")); ok {
		v.State, v.RevokedBy = PermitRevoked, m.By
	}
	if uses, err := os.ReadFile(filepath.Join(dir, id+".uses")); err == nil {
		v.Uses = strings.Count(string(uses), "\n")
	}
	return v, true
}

func permitsOnChannel(ch string) []PermitView {
	entries, err := os.ReadDir(permitDir(ch))
	if err != nil {
		return nil
	}
	var out []PermitView
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !permitIDRe.MatchString(id) {
			continue
		}
		if v, ok := readPermitView(ch, id); ok { // a torn or foreign file is not a permit
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// PermitNotCovered is the store's no: the one line a seat acts on.
type PermitNotCovered struct {
	Reason      string
	Coordinator string
}

func (e PermitNotCovered) Error() string {
	if e.Coordinator == "" {
		return "not covered: " + e.Reason
	}
	return "not covered: " + e.Reason + "; ask " + e.Coordinator + " on the bus"
}

// PermitUse is what a seat asked to do.
type PermitUse struct {
	Channel string
	Action  string
	Repo    string
	Ref     string
	Target  string
}

// UsePermit checks the caller's seat against the channel's permits and counts the use.
// The writer check comes first: nothing else about a permit matters if the seat's
// recorded coordinator did not write it.
func UsePermit(u PermitUse) (PermitView, error) {
	if IsRemote(u.Channel) {
		return PermitView{}, permitUsage("%q is a relay channel: permits are local to this machine", u.Channel)
	}
	ref := normalizeRef(u.Ref)
	switch {
	case u.Action == permitClassEffort:
		return PermitView{}, PermitNotCovered{Reason: "effort is read with cbus permits, not used"}
	case permitRefClasses[u.Action]:
		if err := checkRefShape(ref, permitRefRe); err != nil {
			return PermitView{}, permitUsage("%s needs --ref with one branch name: %v", u.Action, err)
		}
	case permitTargetClasses[u.Action]:
		if u.Target == "" {
			return PermitView{}, permitUsage("%s needs --target", u.Action)
		}
	default:
		return PermitView{}, permitUsage("unknown action class %q: %s", u.Action, permitClassList)
	}
	alias := ""
	for _, reg := range ResolveSelf() {
		if reg.Channel == u.Channel {
			alias = reg.Alias
		}
	}
	if alias == "" {
		return PermitView{}, PermitNotCovered{Reason: "this session is not on " + u.Channel}
	}
	coord, ok := CoordinatorOf(u.Channel, alias)
	if !ok {
		return PermitView{}, PermitNotCovered{Reason: "no coordinator record for " + u.Channel + "/" + alias + ", so no permit applies; this stays with the operator"}
	}
	repo, repoErr := NormalizeRepo(u.Repo)
	if repoErr != nil {
		return PermitView{}, PermitNotCovered{Reason: "this repo: " + repoErr.Error(), Coordinator: coord}
	}
	best, bestScore := "", -1
	var found *PermitView
	for _, v := range permitsOnChannel(u.Channel) {
		if !containsString(v.Actions, u.Action) {
			continue
		}
		reason, score := permitRefusal(v, coord, repo, ref, u.Target)
		if reason == "" {
			found = &v
			break
		}
		if score > bestScore {
			best, bestScore = v.ID+" "+reason, score
		}
	}
	if found == nil {
		if best == "" {
			best = "no permit for " + u.Action + " on " + u.Channel
		}
		return PermitView{}, PermitNotCovered{Reason: best, Coordinator: coord}
	}
	if err := appendPermitUse(*found, u.Channel+"/"+alias, u.Action, ref, u.Target); err != nil {
		return PermitView{}, PermitNotCovered{Reason: "could not record the use: " + err.Error(), Coordinator: coord}
	}
	found.Uses++
	return *found, nil
}

// permitRefusal is why v does not cover this use, "" if it does; score says how close
// it came, so the seat hears about the nearest miss.
func permitRefusal(v PermitView, coord, repo, ref, target string) (string, int) {
	switch {
	case v.Writer != coord:
		return "was written by " + v.Writer + ", your coordinator is " + coord, 0
	case v.State == PermitRevoked:
		return "was revoked", 1
	case v.State != PermitLive:
		return "expired " + v.ExpiresAt.Format(time.RFC3339), 2
	case v.Repo != repo:
		return "is for " + v.Repo + ", this repo is " + repo, 3
	case v.Target != "":
		if v.Target != target {
			return "names target " + v.Target + ", not " + target, 4
		}
	case !permitRefMatches(v.Ref, ref):
		return "covers ref " + v.Ref + ", not " + ref, 4
	}
	return "", 5
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func appendPermitUse(v PermitView, by, action, ref, target string) error {
	b, err := json.Marshal(map[string]string{"by": by, "session": SessionID(), "action": action, "ref": ref, "target": target,
		"at": permitNow().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(permitDir(v.Channel), v.ID+".uses"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ListPermits: all is the operator's audit of every channel and state; otherwise the live
// permits on this session's channels that it wrote or its coordinator wrote.
func ListPermits(regs []LocalReg, all bool) []PermitView {
	var out []PermitView
	if all {
		entries, _ := os.ReadDir(permitsRoot())
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, permitsOnChannel(e.Name())...)
			}
		}
	} else {
		for _, reg := range regs {
			self := reg.Channel + "/" + reg.Alias
			coord, _ := CoordinatorOf(reg.Channel, reg.Alias)
			for _, v := range permitsOnChannel(reg.Channel) {
				if v.State == PermitLive && (v.Writer == self || coord != "" && v.Writer == coord) {
					out = append(out, v)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RevokePermit has no terminal gate, because revoking only tightens. Revoking twice
// reports the first revocation.
func RevokePermit(id, by string) (PermitView, error) {
	if !permitIDRe.MatchString(id) {
		return PermitView{}, permitUsage("bad permit id %q", id)
	}
	for _, v := range ListPermits(nil, true) {
		if v.ID != id {
			continue
		}
		if _, err := claimMarker(filepath.Join(permitDir(v.Channel), id+".revoked"), by); err != nil {
			return v, err
		}
		got, _ := readPermitView(v.Channel, id)
		return got, nil
	}
	return PermitView{}, fmt.Errorf("no permit %s", id)
}
