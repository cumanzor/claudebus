package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"claudebus/internal/core"
)

// peerMeta is the meta.json shape written by the store. listenerPid/ownerPid are
// carried as raw JSON so a rewrite (rename) preserves null-vs-int verbatim. Field
// order matches the bash json.dump (bin/cbus:428-433); lastActivity is the D3
// dual-write, omitted when absent so a rewritten bash-era meta stays byte-identical.
type peerMeta struct {
	Alias       string          `json:"alias"`
	Channel     string          `json:"channel"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	ListenerPid json.RawMessage `json:"listenerPid"`
	OwnerPid    json.RawMessage `json:"ownerPid"`
	// Structural identity witness (P3): the listener's opaque start-time token, so a
	// recycled pid does not read as the process that armed. omitempty because bash-era
	// and pre-P3 metas have none and must rewrite byte-identically; an armed meta
	// without it falls to the TRANSITION argv branch. EVERY rewriter must carry this
	// field or it silently strips a live peer down to that branch.
	ListenerStart string `json:"listenerStart,omitempty"`
	Host          string `json:"host"`
	TS            string `json:"ts"`
	LastActivity  string `json:"lastActivity,omitempty"`
	// Birth-record (cbus-m9l): how a peer was born and on what model, known to the
	// LAUNCHER and stamped into the reservation, so formation save can capture what a
	// session cannot know about itself. omitempty like lastActivity: a rewrite of a
	// bash-era or pre-m9l meta that lacks them stays byte-identical, and an absent
	// field reads as unknown/hand-maintained — never inferred.
	Origin string `json:"origin,omitempty"`
	Model  string `json:"model,omitempty"`
}

var jsonNull = json.RawMessage("null")

// writeMeta writes meta.json atomically: a sibling temp file renamed over the
// target, so a concurrent reader sees old-or-new, never torn (protocol.md §2.2
// upgrade; bash writes in place). indent=2 to match json.dump.
func writeMeta(dir string, m peerMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".meta.tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "meta.json"))
}

// pickAlias returns "main" if free, else the lowest free "fork-N" (bin/cbus:387-392),
// skipping any alias in exclude (claimed by a concurrent sibling this invocation).
func pickAlias(ch string, exclude map[string]bool) string {
	root := CBUSDir()
	if !exclude["main"] && !fileExists(filepath.Join(root, ch, "main", "meta.json")) {
		return "main"
	}
	for n := 1; ; n++ {
		a := "fork-" + strconv.Itoa(n)
		if !exclude[a] && !fileExists(filepath.Join(root, ch, a, "meta.json")) {
			return a
		}
	}
}

// claimAlias atomically claims a free alias via bare-mkdir (EEXIST = lost to a
// concurrent sibling). It REMEMBERS EEXIST-failed aliases and excludes them from
// later picks, so it converges in one extra round rather than burning all its
// retries inside a sibling's mkdir→meta window (F1: bash's ~ms subshell pick was an
// accidental backoff; the Go µs loop needs explicit exclusion — reviewer repro'd
// 8 concurrent joins losing 3). The Class-B contract is a unique alias per joiner;
// exact fork-N numbering under concurrency was never deterministic.
func claimAlias(ch string) (alias, dir string, err error) {
	root := CBUSDir()
	if err := os.MkdirAll(filepath.Join(root, ch), 0o755); err != nil {
		return "", "", err
	}
	exclude := map[string]bool{}
	for tries := 0; tries < 50; tries++ {
		alias = pickAlias(ch, exclude)
		dir = filepath.Join(root, ch, alias)
		e := os.Mkdir(dir, 0o755)
		if e == nil {
			return alias, dir, nil
		}
		if errors.Is(e, fs.ErrExist) {
			exclude[alias] = true
		}
	}
	return "", "", fmt.Errorf("cannot claim an alias in %q", ch)
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

func chSuffix(ch string) string {
	if ch == "" {
		return ""
	}
	return " to " + strconv.Quote(ch)
}

// Join joins ch, auto-picking or claiming alias. Returns the resolved alias and
// alreadyJoined=true when this session already held a registration in ch (no-op).
// It auto-prunes the channel and broadcasts a join event (bin/cbus:394-438).
func Join(ch, alias string) (chosen string, alreadyJoined bool, err error) {
	if !core.ValidName(ch) {
		return "", false, fmt.Errorf("channel must be [A-Za-z0-9._-]")
	}
	// Birth-record (cbus-m9l): capture it BEFORE PruneChannel. A resume-rejoin's own
	// meta carries a DEAD listener, so prune would reap it (and with it the origin) an
	// instant before birthForJoin could read it — D18 case 1 would silently fail for
	// any session that had armed. Reading up front preserves it through the reap. An
	// auto-pick join has no prior meta and stays origin=joined, model unknown.
	origin, model := OriginJoined, ""
	if alias != "" && core.ValidName(alias) {
		origin, model = birthForJoin(filepath.Join(CBUSDir(), ch, alias, "meta.json"), SessionID())
	}
	PruneChannel(ch)
	for _, reg := range ResolveSelf() {
		if reg.Channel == ch {
			return reg.Alias, true, nil
		}
	}
	root := CBUSDir()
	var dir string
	if alias == "" {
		if alias, dir, err = claimAlias(ch); err != nil {
			return "", false, err
		}
	} else {
		if !core.ValidName(alias) {
			return "", false, fmt.Errorf("alias must be [A-Za-z0-9._-]")
		}
		dir = filepath.Join(root, ch, alias)
		metaPath := filepath.Join(dir, "meta.json")
		if fileExists(metaPath) && MetaListenerAlive(metaPath) {
			return "", false, fmt.Errorf("%q is taken by a live listener", ch+"/"+alias)
		}
		_ = os.RemoveAll(dir) // reclaim a stale/dead peer holding the name
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", false, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "inbox.jsonl"), nil, 0o644); err != nil { // truncate-at-join
		return "", false, err
	}
	// A join starts a new epoch, so any replay cursor from the previous one is void.
	// The truncate above can reuse the inbox INODE (an explicit-alias reclaim removes
	// the dir, but a path where the dir survives truncates in place), and a stale
	// sidecar keyed to a reused dev+ino would resume a fresh inbox at an old offset and
	// silently skip everything sent before the first arm. Join owns epoch semantics, so
	// join is where the cursor dies; the dev+ino check in resolveResume stays as
	// belt-and-braces for the reap-and-recreate case.
	_ = os.Remove(filepath.Join(dir, cursorFile))
	now := Now()
	m := peerMeta{
		Alias: alias, Channel: ch, SessionID: SessionID(), Cwd: cwd(),
		ListenerPid: jsonNull, OwnerPid: jsonNull,
		Host: ShortHostname(), TS: now, LastActivity: now,
		Origin: origin, Model: model,
	}
	if err := writeMeta(dir, m); err != nil {
		return "", false, err
	}
	BroadcastPresence(ch, alias, "join", "joined "+ch+" as "+alias, alias)
	return alias, false, nil
}

// birthForJoin resolves the birth-record for an explicit-alias join against whatever
// meta already sits at the name (cbus-m9l, D18 three-way). The identity of that meta
// decides whose facts they are:
//
//   - sessionId "reserved": a reservation placeholder the launcher stamped — carry
//     its origin/model through the reclaim (blank and all; a torn/pre-m9l reservation
//     stays blank, never inferred to "joined").
//   - sessionId == this session's own: a resume-rejoin (the process ended, the meta
//     survived, the same session is coming back) — preserve its origin/model, our own
//     facts, so a restore does not flip a fresh-born peer to joined and blank its
//     model.
//   - anything else, or no readable meta: a session joining under a name it did not
//     reserve — origin=joined, model unknown. Another session's birth-record would be
//     misattribution if carried.
func birthForJoin(metaPath, selfSid string) (origin, model string) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return OriginJoined, ""
	}
	var m struct {
		SessionID string `json:"sessionId"`
		Origin    string `json:"origin"`
		Model     string `json:"model"`
	}
	if json.Unmarshal(b, &m) != nil {
		return OriginJoined, ""
	}
	if m.SessionID == "reserved" || (selfSid != "" && m.SessionID == selfSid) {
		return m.Origin, m.Model
	}
	return OriginJoined, ""
}

// ReserveAlias claims an alias in ch on behalf of a NOT-YET-BOOTED child session
// (branch/spawn title children with their alias, which the child cannot know before
// boot — so the parent claims it and bakes it into the launch prompt). The
// placeholder meta carries sessionId "reserved" and null pids: the child's explicit
// `cbus join <ch> <alias>` reclaims it (a reservation is never listener-alive), an
// abandoned reservation dies via the unarmed grace sweep like any joined-never-armed
// peer, and no presence is broadcast (the child's own join announces). want=""
// auto-picks via the same atomic claim dance as Join.
//
// origin/model are the birth-record (cbus-m9l): the launcher knows how the child was
// born (spawn=fresh, branch=fork) and on what model, and stamps them here so the
// child's join can carry them into the real-sid meta. Blank when the caller does not
// know (never a guess).
func ReserveAlias(ch, want, origin, model string) (alias string, err error) {
	if !core.ValidName(ch) {
		return "", fmt.Errorf("channel must be [A-Za-z0-9._-]")
	}
	PruneChannel(ch)
	root := CBUSDir()
	var dir string
	if want == "" {
		if alias, dir, err = claimAlias(ch); err != nil {
			return "", err
		}
	} else {
		if !core.ValidName(want) {
			return "", fmt.Errorf("alias must be [A-Za-z0-9._-]")
		}
		for _, reg := range ResolveSelf() { // reclaim below would eat our own registration
			if reg.Channel == ch && reg.Alias == want {
				return "", fmt.Errorf("%q is this session's own alias", ch+"/"+want)
			}
		}
		alias = want
		dir = filepath.Join(root, ch, alias)
		metaPath := filepath.Join(dir, "meta.json")
		if fileExists(metaPath) && MetaListenerAlive(metaPath) {
			return "", fmt.Errorf("%q is taken by a live listener", ch+"/"+alias)
		}
		_ = os.RemoveAll(dir) // reclaim a stale/dead peer holding the name
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	now := Now()
	m := peerMeta{
		Alias: alias, Channel: ch, SessionID: "reserved", Cwd: cwd(),
		ListenerPid: jsonNull, OwnerPid: jsonNull,
		Host: ShortHostname(), TS: now, LastActivity: now,
		Origin: origin, Model: model,
	}
	if err := writeMeta(dir, m); err != nil {
		return "", err
	}
	return alias, nil
}

// Unreserve drops a reservation (fork failed after the claim) — best-effort.
func Unreserve(ch, alias string) {
	_ = os.RemoveAll(filepath.Join(CBUSDir(), ch, alias))
	_ = os.Remove(filepath.Join(CBUSDir(), ch)) // rmdir if empty
}

// Leave removes this session's registration(s) — all, or only in ch — broadcasting
// a leave BEFORE removal (bin/cbus:662-673).
func Leave(ch string) (left []string, err error) {
	root := CBUSDir()
	for _, reg := range ResolveSelf() {
		if ch != "" && reg.Channel != ch {
			continue
		}
		BroadcastPresence(reg.Channel, reg.Alias, "leave", "left "+reg.Channel, reg.Alias)
		_ = os.RemoveAll(filepath.Join(root, reg.Channel, reg.Alias))
		_ = os.Remove(filepath.Join(root, reg.Channel)) // rmdir if empty
		left = append(left, reg.Channel+"/"+reg.Alias)
	}
	if len(left) == 0 {
		return nil, fmt.Errorf("not joined%s", chSuffix(ch))
	}
	return left, nil
}

// Unregister force-removes any peer and broadcasts departed (bin/cbus:691-699).
func Unregister(ch, al string) error {
	root := CBUSDir()
	dir := filepath.Join(root, ch, al)
	if !dirExists(dir) {
		return fmt.Errorf("no such peer %q", ch+"/"+al)
	}
	_ = os.RemoveAll(dir)
	BroadcastPresence(ch, al, "departed", "unregistered", al)
	_ = os.Remove(filepath.Join(root, ch))
	return nil
}

// Rename renames this session's local alias (mv dir + rewrite meta.alias),
// reclaiming a dead name-holder with a departed event (bin/cbus:706-737). Returns
// the channel, old alias, and alreadyNamed=true when new==old (no-op).
func Rename(newAlias, wantCh string) (ch, old string, alreadyNamed bool, err error) {
	if !core.ValidName(newAlias) {
		return "", "", false, fmt.Errorf("alias must be [A-Za-z0-9._-]")
	}
	if wantCh != "" && !core.ValidName(wantCh) {
		return "", "", false, fmt.Errorf("bad channel %q", wantCh)
	}
	var match string
	count := 0
	for _, reg := range ResolveSelf() {
		count++
		if wantCh == "" && match == "" {
			match = reg.Channel + "/" + reg.Alias
		}
		if reg.Channel == wantCh {
			match = reg.Channel + "/" + reg.Alias
		}
	}
	if match == "" {
		return "", "", false, fmt.Errorf("not joined%s in this session", chSuffix(wantCh))
	}
	if wantCh == "" && count > 1 {
		return "", "", false, fmt.Errorf("joined to %d channels — pass one: cbus rename %s <channel>", count, newAlias)
	}
	ch, old, _ = strings.Cut(match, "/")
	if old == newAlias {
		return ch, old, true, nil
	}
	root := CBUSDir()
	newDir := filepath.Join(root, ch, newAlias)
	if _, statErr := os.Lstat(newDir); statErr == nil {
		if MetaListenerAlive(filepath.Join(newDir, "meta.json")) {
			return "", "", false, fmt.Errorf("%q is taken by a live listener", ch+"/"+newAlias)
		}
		_ = os.RemoveAll(newDir)
		BroadcastPresence(ch, newAlias, "departed", "departed (name reclaimed)", old) // skip=old actor
	}
	if os.Rename(filepath.Join(root, ch, old), newDir) != nil {
		return "", "", false, fmt.Errorf("rename failed")
	}
	_ = renameMeta(newDir, newAlias) // best-effort, like bash `|| true`
	BroadcastPresence(ch, newAlias, "rename", "renamed "+old+" -> "+newAlias, newAlias)
	return ch, old, false, nil
}

// renameMeta rewrites meta.alias AND invalidates the listener identity, preserving
// every other field verbatim (raw pids). The port writes the alias as a string always,
// dropping bash jset's digit→int coercion (port-map row 16, a documented C-delta).
//
// The invalidation is the P3 half (port-map D1) and it is deliberate NEW behavior.
// argv-grep used to invalidate on rename by accident: the needle followed the new
// alias while the live follower's argv still carried the old path, so the peer read
// dead for free. A structural (pid, starttime) witness survives a directory rename
// untouched, so without clearing it the stale tail would start reading ALIVE and the
// "old tail is stale, re-arm" contract would silently break.
//
// It clears listenerStart ONLY, never listenerPid. Both must hold at once:
//   - identity gone   => the predicate reads dead, so the contract holds
//   - listenerPid kept => the peer still reads ever-armed, which the send gate and the
//     cursor-less migration rule both depend on
//
// The second half's REASON changed when the durable cursor landed, and the original
// wording is no longer true: replay is the cursor's decision now, so with a valid
// cursor present a null listenerPid would resume at the cursor just the same. What
// nulling it actually breaks is the CURSOR-LESS case — a peer mid-migration would fall
// from "seek END once" to a full replay of its entire inbox history — plus the send
// gate, which reads a null listenerPid as never-armed and accepts unconditionally.
func renameMeta(dir, newAlias string) error {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return err
	}
	var m peerMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	m.Alias = newAlias
	m.ListenerStart = ""
	return writeMeta(dir, m)
}

// PruneChannel drops dead peers in one channel via the atomic reap dance and
// removes the channel dir once empty (bin/cbus:352-385). Returns human messages
// (bash prints these to stderr).
func PruneChannel(ch string) []string {
	var msgs []string
	root := CBUSDir()
	chDir := filepath.Join(root, ch)
	if !dirExists(chDir) {
		return nil
	}
	// legacy v1: a meta.json directly at the channel level
	if fileExists(filepath.Join(chDir, "meta.json")) {
		if PeerDead(filepath.Join(chDir, "meta.json")) {
			_ = os.RemoveAll(chDir)
			msgs = append(msgs, "pruned legacy peer "+ch)
		}
		return msgs
	}
	entries, _ := os.ReadDir(chDir)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		peer := e.Name()
		peerDir := filepath.Join(chDir, peer)
		metaPath := filepath.Join(peerDir, "meta.json")
		if !fileExists(metaPath) || !PeerDead(metaPath) {
			continue
		}
		// dot-prefixed same-parent temp: glob-invisible, EXDEV-proof rename claim.
		tmp := filepath.Join(chDir, ".reap."+strconv.Itoa(os.Getpid())+"."+peer)
		if os.Rename(peerDir, tmp) != nil {
			continue // lost the claim to another reaper
		}
		switch {
		case PeerDead(filepath.Join(tmp, "meta.json")):
			_ = os.RemoveAll(tmp)
			msgs = append(msgs, "pruned "+ch+"/"+peer)
			BroadcastPresence(ch, peer, "departed", "departed (listener gone)", peer)
		case dirExists(peerDir):
			_ = os.RemoveAll(tmp) // a fresh join reclaimed the slot — drop our copy
		default:
			if os.Rename(tmp, peerDir) != nil { // false claim — restore
				_ = os.RemoveAll(tmp)
			}
		}
	}
	_ = os.Remove(chDir) // rmdir if empty
	return msgs
}

// PruneRemoteMarkers sweeps .remote markers whose owning pid is dead; legacy
// file-markers are always removed; empty dirs are rmdir'd (bin/cbus:195-221).
func PruneRemoteMarkers() []string {
	var msgs []string
	root := filepath.Join(CBUSDir(), ".remote")
	hosts, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, h := range hosts {
		if !h.IsDir() {
			continue
		}
		hostDir := filepath.Join(root, h.Name())
		chans, _ := os.ReadDir(hostDir)
		for _, c := range chans {
			chPath := filepath.Join(hostDir, c.Name())
			if !c.IsDir() { // legacy machine-global file-marker — always swept
				_ = os.Remove(chPath)
				msgs = append(msgs, "pruned legacy remote marker "+c.Name()+"@"+h.Name())
				continue
			}
			sids, _ := os.ReadDir(chPath)
			for _, s := range sids {
				if s.IsDir() {
					continue
				}
				mf := filepath.Join(chPath, s.Name())
				if !pidAlive(markerOwnerPid(mf)) {
					_ = os.Remove(mf)
					msgs = append(msgs, "pruned remote marker "+c.Name()+"@"+h.Name()+" (session "+s.Name()+")")
				}
			}
			_ = os.Remove(chPath) // rmdir if empty
		}
		_ = os.Remove(hostDir)
	}
	return msgs
}

// markerOwnerPid reads a marker's ownerPid (0 if absent/torn -> treated as dead).
func markerOwnerPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var m struct {
		OwnerPid json.RawMessage `json:"ownerPid"`
	}
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	return rawInt(m.OwnerPid)
}

// Prune runs PruneChannel over one channel or all, plus the remote-marker sweep on
// a bare prune (bin/cbus:632-647). Returns all messages.
func Prune(chosen string) []string {
	var msgs []string
	root := CBUSDir()
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if chosen != "" && e.Name() != chosen {
			continue
		}
		msgs = append(msgs, PruneChannel(e.Name())...)
	}
	if chosen == "" {
		msgs = append(msgs, PruneRemoteMarkers()...)
	}
	return msgs
}

// LeaveRemote drops THIS session's identity marker for ch@host (no relay call —
// queued mail stays on the relay; bin/cbus:651-660).
func LeaveRemote(host, ch string) error {
	if ch == "" {
		return fmt.Errorf("usage: cbus leave <channel>@<host>")
	}
	mf := filepath.Join(CBUSDir(), ".remote", host, ch, markerSID())
	if !fileExists(mf) {
		return fmt.Errorf("no remote identity for %s@%s in this session", ch, host)
	}
	_ = os.Remove(mf)
	_ = os.Remove(filepath.Join(CBUSDir(), ".remote", host, ch))
	_ = os.Remove(filepath.Join(CBUSDir(), ".remote", host))
	return nil
}
