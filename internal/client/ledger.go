package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The durable channel ledger (bdx-mec.2). meta.json is the only place alias,
// channel and sessionId bind together, and PruneChannel destroys it with the peer
// dir — which is why a finished formation's alias-to-session map exists nowhere
// afterwards. The ledger records that binding append-only, outside every peer dir,
// so a run stays reconstructible after every peer is gone.
//
// Placement is load-bearing twice over: under CBUSDir()/.ledger it is out of reach
// of the peer reap, and the leading dot means PruneChannel and BroadcastPresence
// (which both skip "."-prefixed entries when walking a channel) never see it, so an
// older binary ignores the ledger by construction rather than by luck.
const (
	ledgerDir = ".ledger"

	LedgerJoin    = "join"
	LedgerLeave   = "leave"
	LedgerRename  = "rename"
	LedgerSpawn   = "spawn"
	LedgerResume  = "resume"
	LedgerRestore = "restore"
	LedgerRebind  = "rebind"
)

// Emitter distinguishes an event a session recorded about itself from one another
// process recorded on its behalf. A crashed peer never emits its own leave, so the
// reaper emits it; a consumer must be able to tell "this session said it left" from
// "we found it dead", because only the first is a statement by the session.
//
// This is deliberately NOT folded into Origin. Origin has a closed vocabulary
// (fresh/fork/joined) that Validate enforces at formation.go:552, and it answers a
// different question — how the session was born, not who wrote the line. Adding a
// fourth value would either break that validator or quietly widen a checked enum.
const (
	EmitterSelf   = "self"   // the session recorded its own event
	EmitterReaper = "reaper" // a pruning peer recorded a crashed peer's departure
	EmitterForced = "forced" // an operator/tool force-removed the peer (cbus unregister)
)

// ledgerKinds is the CLOSED event vocabulary. AppendLedger rejects anything else, so
// a typo or an ad-hoc kind (an earlier draft nearly shipped "prune") cannot enter the
// durable record — the whole value of the ledger is that a consumer can trust its
// vocabulary without a guard of its own.
var ledgerKinds = map[string]bool{
	LedgerJoin: true, LedgerLeave: true, LedgerRename: true, LedgerSpawn: true,
	LedgerResume: true, LedgerRestore: true, LedgerRebind: true,
}

// LedgerEvent is one append-only line. The base field set is ALWAYS SERIALIZED, even
// when empty: an unknown fact is written as "" (or 0 for pid), never omitted, so a
// consumer reads "known-absent" rather than having to distinguish a missing key from
// an old writer. Only the genuinely-conditional continuity fields (prevAlias,
// prevSessionId) and emitter are omitempty, because they apply to specific event
// kinds and their absence is meaningful rather than unknown.
type LedgerEvent struct {
	Event          string `json:"event"`
	TS             string `json:"ts"`
	FormationRunID string `json:"formationRunId"`
	Channel        string `json:"channel"`
	Alias          string `json:"alias"`
	SessionID      string `json:"sessionId"`
	Harness        string `json:"harness"`
	Host           string `json:"host"`
	Pid            int    `json:"pid"`
	Cwd            string `json:"cwd"`
	Origin         string `json:"origin"`
	// Continuity, recorded rather than inferred. A rename carries the alias it came
	// from; a resume/restore carries the session it continues. These are exactly the
	// two cases where alias+time-window reconstruction silently produces wrong edges.
	PrevAlias     string `json:"prevAlias,omitempty"`
	PrevSessionID string `json:"prevSessionId,omitempty"`
	Emitter       string `json:"emitter,omitempty"`
}

func ledgerRoot() string { return filepath.Join(CBUSDir(), ledgerDir) }

func ledgerPath(ch string) string { return filepath.Join(ledgerRoot(), ch+".jsonl") }

// AppendLedger appends one event as a single write, so concurrent appenders
// interleave line-atomically.
//
// It deliberately does NOT follow appendInbox, which discards every error including
// a short write. That is right for an inbox, which join truncates anyway, and wrong
// for the one file whose entire purpose is durability: a silently dropped line is
// indistinguishable from an event that never happened, which is the failure this
// ledger exists to prevent. A short write is an error here, and the caller surfaces
// it rather than hiding it.
func AppendLedger(ev LedgerEvent) error {
	if ev.TS == "" {
		ev.TS = Now()
	}
	// hard validation (finding 5): an incomplete or unknown-kind event is a caller
	// bug, and silently dropping it would let the bug hide. The base keys required
	// here are the ones that make an event addressable at all; subject facts (host,
	// cwd, pid, origin, harness) may legitimately be empty and are not gated.
	if !ledgerKinds[ev.Event] {
		return fmt.Errorf("cbus ledger: unknown event kind %q", ev.Event)
	}
	if ev.Channel == "" || ev.Alias == "" {
		return fmt.Errorf("cbus ledger: %s event missing channel or alias", ev.Event)
	}
	if err := os.MkdirAll(ledgerRoot(), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line := append(b, '\n')
	f, err := os.OpenFile(ledgerPath(ev.Channel), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := f.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return fmt.Errorf("cbus ledger: short write to %s (%d of %d bytes)", ledgerPath(ev.Channel), n, len(line))
	}
	return nil
}

// ReadLedger returns a channel's events oldest-first. A malformed line is skipped
// rather than fatal: the file is append-only and may be tailed mid-write.
func ReadLedger(ch string) []LedgerEvent {
	b, err := os.ReadFile(ledgerPath(ch))
	if err != nil {
		return nil
	}
	var out []LedgerEvent
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev LedgerEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// mintRunID builds an opaque, sortable run id. The random tail is what makes it
// identity rather than a timestamp: two channels minted in the same second, or a
// channel re-created within one second of emptying, must not collide.
func mintRunID() string {
	return "run_" + time.Now().UTC().Format("20060102T150405Z") + "_" + randToken(6)
}

// randToken returns n hex-ish chars from crypto/rand, falling back to the clock's
// sub-second bits if the entropy source fails — a degraded token is better than a
// blocked join, and the timestamp prefix already carries most of the uniqueness.
func randToken(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(int64(time.Now().UTC().Nanosecond()), 16)
	}
	return hex.EncodeToString(b)[:n]
}

// Run identity lives in a per-peer CLAIM, not a channel-level run file. Each member
// holds <peerDir>/.run naming the run it belongs to. That single choice removes the
// whole stale-run-file failure class: a run's identity exists only while a live peer
// claims it, so there is no channel-level record to go stale and be resurrected.
//
// The claim is a claim MADE FOR THIS JOIN, which is exactly the property review found
// missing. A ledger (alias,sid) binding proves membership in SOME run that once
// recorded it; because session ids survive resume by design, that historical binding
// cannot prove CURRENT membership. A live peer's own claim file can, because it is
// rewritten (or destroyed and remade) every time the peer joins or resumes.
//
// It is a sidecar rather than a peerMeta field on purpose, and this is the reason:
// peerMeta is a typed struct with no unknown-key preservation, so an older binary
// rewriting meta on rename would DROP a new field and silently degrade the proof —
// the rewriter landmine the plan already flags. A separate dot-file needs no
// preservation: os.Rename moves the whole peer dir (claim included) on rename, and
// RemoveAll destroys it on leave/reap. An older binary carries it correctly by doing
// nothing special — it never has to know the file exists.
const claimFile = ".run"

// RunBoundary reports whether ch already had live peers. A joiner must call this
// BEFORE its own mutation: afterwards its own meta makes the channel populated, so
// every join would look like it landed in a live run, including the first join after
// a formation died.
func RunBoundary(ch string) (wasPopulated bool) {
	return channelPopulated(ch)
}

// RunIDFor resolves the run id for ch, observing the boundary itself and writing no
// claim — a read-only convenience for callers that are not themselves joining.
func RunIDFor(ch string) string {
	return currentRun(ch, "")
}

// ResolveRunForJoin decides which run the joining alias belongs to AND records its
// claim, so the peer's membership in the run is a fact on disk before this returns.
// wasPopulated is the boundary the caller observed before mutating; alias is the
// joiner's own, excluded from every "is anyone else here" question.
//
// Inheritance is driven only by CURRENT claims: adopt the run a live sibling claims,
// else mint a fresh one. A finished run leaves no channel-level trace to inherit, and
// a resumed session's stale claim was destroyed when its dir was reclaimed at rejoin,
// so neither a reused channel name nor a resumed session id can resurrect a dead run.
func ResolveRunForJoin(ch, alias string, wasPopulated bool) string {
	if ch == "" {
		return ""
	}
	if err := os.MkdirAll(ledgerRoot(), 0o755); err != nil {
		return ""
	}
	// A SPLIT roster is unknowable, not first-wins: several live authorities disagree
	// about which run this channel is, so a joiner must take no side. Claim and event
	// stay blank and the split is announced, because an arbitrary adoption would hide
	// exactly the condition an operator needs to see. The peer still joins and works —
	// bus membership does not depend on run identity — which is the same
	// evidence-over-blocking call the save path makes.
	if ids := liveRuns(ch, alias); len(ids) > 1 {
		fmt.Fprintf(os.Stderr,
			"cbus: WARNING channel %q is split across %d runs (%s); joining without a run\n",
			ch, len(ids), strings.Join(ids, ", "))
		return ""
	}
	// fast path: a live sibling already anchors a single unambiguous run
	if wasPopulated {
		if id := currentRun(ch, alias); id != "" {
			return commitOrBlank(ch, alias, id)
		}
	}
	// mint path — serialized, and the claim is written UNDER the lock so a concurrent
	// opener that acquires next sees it and converges instead of minting a second id
	unlock, ok := acquireMintLock(ch)
	if !ok {
		return "" // wedged past all patience; blank beats an unsynchronized mint (finding 2)
	}
	defer unlock()
	// re-check under the lock: a sibling may have opened (or split) the run since
	if ids := liveRuns(ch, alias); len(ids) > 1 {
		fmt.Fprintf(os.Stderr,
			"cbus: WARNING channel %q is split across %d runs (%s); joining without a run\n",
			ch, len(ids), strings.Join(ids, ", "))
		return ""
	} else if len(ids) == 1 {
		return commitOrBlank(ch, alias, ids[0])
	}
	return commitOrBlank(ch, alias, mintRunID())
}

// commitOrBlank returns id only if the claim actually committed to disk. The claim
// file is the CURRENT-membership authority now, not mere evidence, so reporting a run
// whose claim did not persist would be a lie: the join event would assert membership
// nothing on disk backs, and the next sibling — seeing no claim — would mint a split
// run. A write failure degrades to a blank run (honestly unknown) plus a loud stderr,
// never to a nonexistent-but-nonempty id. (reviewer1 finding 1: evidence may be
// best-effort, the authority may not.)
func commitOrBlank(ch, alias, id string) string {
	if err := writeClaim(ch, alias, id); err != nil {
		fmt.Fprintln(os.Stderr, "cbus: run claim did not commit, run recorded blank:", err)
		return ""
	}
	return id
}

// liveRuns returns the DISTINCT run ids claimed by live peers of ch, excluding skip,
// sorted for determinism. More than one means the channel is SPLIT: several live
// authorities disagree about which run this is.
func liveRuns(ch, skip string) []string {
	entries, err := os.ReadDir(filepath.Join(CBUSDir(), ch))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == skip {
			continue
		}
		peerDir := filepath.Join(CBUSDir(), ch, e.Name())
		if PeerDead(filepath.Join(peerDir, "meta.json")) {
			continue
		}
		if id := readClaim(peerDir); id != "" {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// currentRun is ch's run when it is UNAMBIGUOUS: exactly one distinct live claim.
// Zero claims and a split both read "" — a caller needing to tell them apart uses
// liveRuns. Returning the first claim of a split would let a joiner silently adopt
// whichever alias sorts first, which is precisely what the authority rule forbids:
// when live authorities disagree, identity is unknown and no caller may pick one.
// SaveFormation already behaved this way; this makes the join path agree with it.
func currentRun(ch, skip string) string {
	if ids := liveRuns(ch, skip); len(ids) == 1 {
		return ids[0]
	}
	return ""
}

// LauncherRun is the run of the LAUNCHER itself — the session authoring a
// launcher-authored event (spawn, restore) about a child that has no claim yet.
//
// It reads the launcher's OWN claim, not any live claim on the channel. Those agree in
// a converged run and diverge exactly during a split, where "any live claim" would
// stamp whichever peer dir enumerates first and could attribute a child to a run its
// parent is not in. The launcher is an authority only about its own run.
func LauncherRun(ch, launcherAlias string) string {
	if launcherAlias == "" {
		return ""
	}
	return readClaim(filepath.Join(CBUSDir(), ch, launcherAlias))
}

// SelfAliasIn returns this session's own alias in ch, or "" when it holds none.
func SelfAliasIn(ch string) string {
	for _, reg := range ResolveSelf() {
		if reg.Channel == ch {
			return reg.Alias
		}
	}
	return ""
}

// runIDForEvent picks the run a non-join event belongs to: the acting peer's OWN
// claim, and nothing else. It never mints and never infers from a sibling.
//
// The sibling fallback (currentRun) was a finding-1 authority leak one event later:
// a peer whose join-claim failed has no claim of its own, and letting its rebind or
// rename adopt whatever run a sibling happens to claim re-invents the membership the
// authority refused to commit. For an existing subject alias, absence of its own
// claim is UNKNOWN — blank — always. Launcher-authored events (spawn, restore) do not
// come through here; they pass the acting run explicitly, because the launcher is the
// authority for the child slot it is creating.
func runIDForEvent(ch, alias string) string {
	if alias == "" {
		return ""
	}
	return readClaim(filepath.Join(CBUSDir(), ch, alias))
}

// writeClaim persists a peer's run claim atomically (temp + rename). It returns an
// error so its one caller (commitOrBlank) can refuse to report an uncommitted run.
// The write can be fault-injected via claimWriteHook for the failure tests.
func writeClaim(ch, alias, id string) error {
	if alias == "" || id == "" {
		return fmt.Errorf("empty alias or run id")
	}
	dir := filepath.Join(CBUSDir(), ch, alias)
	if !dirExists(dir) {
		return fmt.Errorf("peer dir %q absent", dir)
	}
	tmp := filepath.Join(dir, ".run.tmp."+strconv.Itoa(os.Getpid()))
	if err := claimWrite(tmp, []byte(id+"\n"), 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := claimRename(tmp, filepath.Join(dir, claimFile)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// claimWrite/claimRename are indirections so a test can inject a write or rename
// failure on the identity-authority path (reviewer1 finding 1 asked for both legs).
var (
	claimWrite  = os.WriteFile
	claimRename = os.Rename
)

func readClaim(peerDir string) string {
	b, err := os.ReadFile(filepath.Join(peerDir, claimFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// errLockContended is the ONE caller-visible signal for "another holder has it", and it
// is the reason this loop does not compare a platform errno directly. flock reports
// contention as EWOULDBLOCK and LockFileEx reports it as ERROR_LOCK_VIOLATION; a check
// written against either constant cannot match on the other platform, so it would fall
// straight through to the unexpected-error branch and give up on the FIRST contended
// try. The loop would look correct, compile everywhere, and abandon the retry it exists
// for under exactly the concurrency it was built to survive. Each platform maps its own
// errno onto this sentinel so the decision here is platform-blind.
var errLockContended = errors.New("mint lock held by another process")

// acquireMintLock takes a per-channel lock on an open fd via the kernel's own file-lock
// primitive: flock(2) on unix, LockFileEx on windows. That choice settles a problem
// three rounds of hand-rolled file dances kept re-leaking, and both primitives carry the
// two properties that made it settle. They RELEASE ON CRASH (the kernel drops the lock
// when the holder's handle closes, including on process death) and they confer true
// ownership, so there is no pid to be recycled, no token to go partial, and no
// rename-to-steal TOCTOU. Both are acquired non-blocking (LOCK_NB / FAIL_IMMEDIATELY) so
// we control the retry cadence rather than queueing in the kernel. The lock is a local
// file under CBUSDir(), so the classic NFS flock caveats do not apply. ok=false only if
// the lock stays held past all patience, in which case the caller records a blank run
// rather than mint unsynchronized.
func acquireMintLock(ch string) (release func(), ok bool) {
	if err := os.MkdirAll(ledgerRoot(), 0o755); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(filepath.Join(ledgerRoot(), "."+ch+".mint"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	for tries := 0; tries < 4000; tries++ { // ~4s at 1ms; a real mint is microseconds
		err := tryLockExclusive(f)
		if err == nil {
			return func() {
				// unobservable by construction: Close alone drops the lock on both platforms, so a
				// mutant deleting this call stays green. It is here to state the intent, not to be tested.
				_ = unlockFile(f)
				_ = f.Close()
			}, true
		}
		if !errors.Is(err, errLockContended) {
			_ = f.Close() // an unexpected lock error is not something to spin on
			return nil, false
		}
		time.Sleep(time.Millisecond)
	}
	_ = f.Close()
	return nil, false
}

// channelPopulated reports whether ch holds at least one peer that is not dead.
// Deliberately !PeerDead rather than listener-alive: a joined-but-unarmed peer is
// part of the run, and treating it as absent would mint a second id mid-run.
func channelPopulated(ch string) bool { return channelPopulatedExcept(ch, "") }

// channelPopulatedExcept is channelPopulated ignoring one alias, so a joiner can ask
// "is anyone OTHER than me live here" after it has already written its own meta.
func channelPopulatedExcept(ch, skip string) bool {
	entries, err := os.ReadDir(filepath.Join(CBUSDir(), ch))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if skip != "" && e.Name() == skip {
			continue
		}
		metaPath := filepath.Join(CBUSDir(), ch, e.Name(), "meta.json")
		if fileExists(metaPath) && !PeerDead(metaPath) {
			return true
		}
	}
	return false
}

// HarnessName reports which coding harness owns this process (claude, codex, grok,
// opencode), reusing the same ancestor walk and argv[0] identity that OwnerPID uses.
// Empty when no harness ancestor is found — never guessed.
func HarnessName() string {
	return harnessWalk(os.Getppid(), procWalkRoot, procLookup())
}

// normalizeHarness collapses the claude-* variants onto one name so a ledger
// consumer groups by harness rather than by build flavour.
func normalizeHarness(base string) string {
	if strings.HasPrefix(base, "claude") {
		return "claude"
	}
	return base
}

// subject is the on-disk facts about a peer, captured from its own meta and claim
// BEFORE the dir is destroyed. A terminal event (self-leave, reaper) must report the
// DEPARTING peer's facts, not the acting process's — the reaper is a different session
// entirely, and even a self-leave has already broadcast before it reads these.
type subject struct {
	SessionID string
	RunID     string
	Host      string
	Cwd       string
	Origin    string
	Pid       int
	self      bool   // the acting process IS this subject (self-leave), so its harness applies
	emitter   string // who wrote the line; defaults to reaper, self overrides via `self`
}

// readSubject pulls every fact the ledger records from a peer dir, tolerating a
// missing or torn meta as "unknown" (empty), never inferred.
func readSubject(peerDir string) subject {
	s := subject{RunID: readClaim(peerDir)}
	b, err := os.ReadFile(filepath.Join(peerDir, "meta.json"))
	if err != nil {
		return s
	}
	var m struct {
		SessionID string          `json:"sessionId"`
		Host      string          `json:"host"`
		Cwd       string          `json:"cwd"`
		Origin    string          `json:"origin"`
		OwnerPid  json.RawMessage `json:"ownerPid"`
	}
	if json.Unmarshal(b, &m) != nil {
		return s
	}
	s.SessionID, s.Host, s.Cwd, s.Origin = m.SessionID, m.Host, m.Cwd, m.Origin
	if pid, perr := strconv.Atoi(strings.TrimSpace(string(m.OwnerPid))); perr == nil {
		s.Pid = pid
	}
	return s
}

// RecordEventForSubject appends a terminal event carrying the SUBJECT's own facts.
// emitter names who wrote the line (self vs reaper); harness is passed rather than
// computed, because only a self-leave shares the subject's harness — the reaper does
// not, so it passes "" rather than misattributing its own.
func RecordEventForSubject(event, ch, alias string, subj subject) {
	if ch == "" {
		return
	}
	harness, emitter := "", EmitterReaper
	if subj.emitter != "" {
		emitter = subj.emitter // an explicit authorship (forced removal) wins
	}
	if subj.self {
		harness, emitter = HarnessName(), EmitterSelf
	}
	ev := LedgerEvent{
		Event: event,
		TS:    Now(),
		// the SUBJECT's own claim, read before its dir was destroyed. Never a sibling's
		// run: a departed peer that never committed a claim has an unknown run, blank.
		FormationRunID: subj.RunID,
		Channel:        ch,
		Alias:          alias,
		SessionID:      subj.SessionID,
		Harness:        harness,
		Host:           subj.Host,
		Pid:            subj.Pid,
		Cwd:            subj.Cwd,
		Origin:         subj.Origin,
		Emitter:        emitter,
	}
	if err := AppendLedger(ev); err != nil {
		fmt.Fprintln(os.Stderr, "cbus: ledger append failed:", err)
	}
}

// RecordEvent stamps the facts this process can observe about itself and appends.
//
// Ordering rule, one answer for all call sites: the ledger is written AFTER the
// mutation it describes, never before. Ledger-first would record joins that failed
// and renames that did not happen, and a reader cannot tell a false event from a
// true one. Mutation-first can instead lose an event to a crash in the gap, which is
// the survivable direction: the ledger is EVIDENCE, not an invariant, and every
// consumer must already tolerate a missing event because pre-ledger channels have
// none at all.
//
// A failed append never fails the bus operation, but it is reported on stderr rather
// than swallowed. A durability record that fails silently is worse than none, since
// its absence would be read as "this never happened".
func RecordEvent(event, ch, alias, sessionID string, fill func(*LedgerEvent)) {
	RecordEventInRun(event, ch, alias, sessionID, "", fill)
}

// RecordEventInRun is RecordEvent with the run already decided. A mutating caller
// resolves the run from the boundary it captured BEFORE mutating and passes it here;
// re-deriving it at record time would misread the caller's own presence as proof the
// run was already live.
func RecordEventInRun(event, ch, alias, sessionID, runID string, fill func(*LedgerEvent)) {
	if ch == "" {
		return
	}
	if runID == "" {
		runID = runIDForEvent(ch, alias)
	}
	ev := LedgerEvent{
		Event:          event,
		TS:             Now(),
		FormationRunID: runID,
		Channel:        ch,
		Alias:          alias,
		SessionID:      sessionID,
		Harness:        HarnessName(),
		Host:           ShortHostname(),
		Cwd:            cwd(),
		Emitter:        EmitterSelf,
	}
	if pid, ok := OwnerPID(); ok {
		ev.Pid = pid
	}
	if fill != nil {
		fill(&ev)
	}
	if err := AppendLedger(ev); err != nil {
		fmt.Fprintln(os.Stderr, "cbus: ledger append failed:", err)
	}
}
