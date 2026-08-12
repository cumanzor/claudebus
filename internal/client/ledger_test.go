package client

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The durable ledger (bdx-mec.2). These tests pin the properties that make it worth
// having at all: it outlives the peer dirs, it survives torn writes, one run gets one
// id, and a crashed peer still produces a terminal event.

func ledgerLines(t *testing.T, ch string) []LedgerEvent {
	t.Helper()
	return ReadLedger(ch)
}

// The whole point: the alias-to-session binding must survive the reap that destroys
// meta.json. This is the api36 failure reproduced and then prevented.
func TestLedgerOutlivesThePeerDirectory(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := Leave("cc"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cc", "coder")); !os.IsNotExist(err) {
		t.Fatalf("peer dir should be gone after leave")
	}
	evs := ledgerLines(t, "cc")
	if len(evs) != 2 {
		t.Fatalf("want join+leave, got %d events: %+v", len(evs), evs)
	}
	for _, ev := range evs {
		if ev.Alias != "coder" || ev.SessionID != "SID" {
			t.Errorf("alias/session binding lost: %+v", ev)
		}
		if ev.FormationRunID == "" {
			t.Errorf("event carries no run id: %+v", ev)
		}
	}
	if evs[0].Event != LedgerJoin || evs[1].Event != LedgerLeave {
		t.Errorf("event order/kinds wrong: %s then %s", evs[0].Event, evs[1].Event)
	}
}

// The ledger must be invisible to the reap, structurally. .ledger lives outside the
// channel tree, so pruning a whole channel cannot touch it.
func TestPruneCannotReachTheLedger(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	_, _ = Leave("cc")
	PruneChannel("cc")
	if _, err := os.Stat(filepath.Join(root, ledgerDir, "cc.jsonl")); err != nil {
		t.Fatalf("ledger destroyed by prune: %v", err)
	}
}

// main + reviewer2 edge 1: two joiners racing into an empty channel must converge on
// ONE run id. Read-check-write would let both mint and leave the run split in two.
func TestConcurrentMintYieldsOneRunID(t *testing.T) {
	root := setupStore(t)
	const N = 16
	// every joiner observes the boundary BEFORE mutating, exactly as Join does
	wasPop := RunBoundary("cc")
	if err := os.MkdirAll(filepath.Join(root, "cc"), 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make([]string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			alias := fmt.Sprintf("p%d", i)
			// mutation first: the peer is on disk before it resolves its run, which
			// is what lets a racing sibling see it as live and inherit
			dir := filepath.Join(root, "cc", alias)
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(fmt.Sprintf(
				`{"alias":%q,"channel":"cc","sessionId":"S%d","listenerPid":null,"ownerPid":null,"lastActivity":%q}`,
				alias, i, Now())), 0o644)
			ids[i] = ResolveRunForJoin("cc", alias, wasPop)
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatalf("empty run id from a concurrent mint")
		}
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent mint produced %d distinct run ids, want 1: %v", len(seen), seen)
	}
}

// reviewer2 edge 2: the run boundary is LIVENESS-based, not directory-based. A
// crashed formation leaves dead peer dirs behind until something prunes; the next
// formation on that channel name must NOT inherit the dead run's id.
func TestDeadPeersDoNotKeepARunAlive(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	first := ledgerLines(t, "cc")[0].FormationRunID
	if first == "" {
		t.Fatal("no run id after join")
	}
	// crash the peer: meta stays on disk, but stale enough to read dead. No prune.
	metaPath := filepath.Join(root, "cc", "coder", "meta.json")
	seedDeadMeta(t, metaPath)
	if !PeerDead(metaPath) {
		t.Fatal("test setup: peer should read dead")
	}
	if dirEntriesExist(root, "cc") == 0 {
		t.Fatal("test setup: peer dir should still exist (no prune)")
	}
	wasPop2 := RunBoundary("cc")
	liveUnarmedPeer(t, root, "cc", "newpeer", "S-NEW") // production writes meta before resolving
	second := ResolveRunForJoin("cc", "newpeer", wasPop2)
	if second == first {
		t.Fatalf("dead peer dirs kept the run alive: got the same id %q for a new run", first)
	}
}

// A leave must be stamped with the run it CLOSES. Routing it through the minting
// path would observe the emptied channel, mint a fresh id, and leave the real run
// with no terminal event plus a phantom run containing only a leave.
func TestLeaveClosesTheRunItBelongsTo(t *testing.T) {
	setupStore(t)
	_, _, _ = Join("cc", "coder")
	joinRun := ledgerLines(t, "cc")[0].FormationRunID
	_, _ = Leave("cc")
	evs := ledgerLines(t, "cc")
	leave := evs[len(evs)-1]
	if leave.Event != LedgerLeave {
		t.Fatalf("want leave last, got %q", leave.Event)
	}
	if leave.FormationRunID != joinRun {
		t.Fatalf("leave minted a new run %q, want the closing run %q", leave.FormationRunID, joinRun)
	}
}

// main + reviewer2 edge 4: a crashed peer never emits its own leave, so the reaper
// emits one — and must capture the sid BEFORE RemoveAll, because afterwards the
// binding exists nowhere.
func TestReaperEmitsLeaveForACrashedPeer(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	seedDeadMeta(t, filepath.Join(root, "cc", "coder", "meta.json"))

	PruneChannel("cc")

	evs := ledgerLines(t, "cc")
	last := evs[len(evs)-1]
	if last.Event != LedgerLeave {
		t.Fatalf("reap produced no terminal event, got %q", last.Event)
	}
	if last.Emitter != EmitterReaper {
		t.Errorf("emitter = %q, want %q so a consumer can tell this from a self-reported leave", last.Emitter, EmitterReaper)
	}
	if last.SessionID != "SID" {
		t.Errorf("sid not captured before RemoveAll: %+v", last)
	}
	if last.Alias != "coder" {
		t.Errorf("alias lost: %+v", last)
	}
	// the reaper reports the DEPARTED peer's own facts (read from its meta before
	// deletion), not the reaper's. Harness stays empty because meta never carried it
	// and the reaper cannot know a dead session's harness.
	if last.Origin != OriginJoined {
		t.Errorf("departed peer's origin dropped: %+v", last)
	}
	if last.Harness != "" {
		t.Errorf("reaper invented a harness for a dead peer: %+v", last)
	}
}

// Emitter must not be folded into Origin: Origin has a closed vocabulary that
// formation validation enforces, and it answers a different question.
func TestReaperEventDoesNotPolluteOriginVocabulary(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	seedDeadMeta(t, filepath.Join(root, "cc", "coder", "meta.json"))
	PruneChannel("cc")
	for _, ev := range ledgerLines(t, "cc") {
		if ev.Origin == "" {
			continue
		}
		if ev.Origin != OriginFresh && ev.Origin != OriginFork && ev.Origin != OriginJoined {
			t.Errorf("origin %q is outside the validated vocabulary", ev.Origin)
		}
	}
}

// reviewer2 edge 3: reader tolerance. A crash mid-write leaves a partial last line,
// and a torn line mid-file is followed by another appender's intact line. Neither
// may abort the scan.
func TestReaderSkipsTornAndMalformedLines(t *testing.T) {
	root := setupStore(t)
	if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "cc", Alias: "a", SessionID: "S1"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ledgerDir, "cc.jsonl")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"event\":\"join\",\"alias\":\"tor\n") // torn mid-file
	f.WriteString("not json at all\n")
	f.Close()
	if err := AppendLedger(LedgerEvent{Event: LedgerLeave, Channel: "cc", Alias: "b", SessionID: "S2"}); err != nil {
		t.Fatal(err)
	}
	f, _ = os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("{\"event\":\"lea") // truncated final line, no newline
	f.Close()

	evs := ReadLedger("cc")
	var aliases []string
	for _, ev := range evs {
		aliases = append(aliases, ev.Alias)
	}
	if len(evs) != 2 || aliases[0] != "a" || aliases[1] != "b" {
		t.Fatalf("torn lines broke the scan: got %d events %v", len(evs), aliases)
	}
}

// Single-write appends interleave line-atomically: N concurrent appenders yield N
// intact, parseable lines.
func TestConcurrentAppendsAreLineAtomic(t *testing.T) {
	setupStore(t)
	const N = 40
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = AppendLedger(LedgerEvent{
				Event: LedgerJoin, Channel: "cc", Alias: "peer", SessionID: "S",
				Cwd: strings.Repeat("x", 200), // fatten the line to widen the tear window
			})
		}(i)
	}
	wg.Wait()
	if got := len(ReadLedger("cc")); got != N {
		t.Fatalf("got %d intact lines from %d concurrent appends", got, N)
	}
}

// A rename must record where the alias came FROM. Without prevAlias a consumer sees
// one peer vanish and another appear, and has to guess the link.
func TestRenameRecordsAliasContinuity(t *testing.T) {
	setupStore(t)
	_, _, _ = Join("cc", "coder")
	if _, _, _, err := Rename("builder", "cc"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	evs := ledgerLines(t, "cc")
	last := evs[len(evs)-1]
	if last.Event != LedgerRename {
		t.Fatalf("want rename, got %q", last.Event)
	}
	if last.Alias != "builder" || last.PrevAlias != "coder" {
		t.Errorf("continuity not recorded: alias=%q prevAlias=%q", last.Alias, last.PrevAlias)
	}
	if last.SessionID != "SID" {
		t.Errorf("rename lost the session binding: %+v", last)
	}
}

// Ordering rule: the ledger is written AFTER the mutation, so a failed operation
// leaves no evidence that it happened.
func TestFailedJoinLeavesNoLedgerEvent(t *testing.T) {
	setupStore(t)
	if _, _, err := Join("bad/channel", "coder"); err == nil {
		t.Fatal("expected a rejected channel name")
	}
	if evs := ReadLedger("bad/channel"); len(evs) != 0 {
		t.Fatalf("failed join recorded %d events", len(evs))
	}
}

// Finding 5: an incomplete or unknown-kind event must ERROR, not silently vanish.
// A dropped event is indistinguishable from one that never happened, which is the
// exact failure the ledger exists to prevent.
func TestAppendLedgerRejectsMalformedEvents(t *testing.T) {
	setupStore(t)
	if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Alias: "a"}); err == nil {
		t.Error("a channel-less event should be rejected")
	}
	if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "cc"}); err == nil {
		t.Error("an alias-less event should be rejected")
	}
	if err := AppendLedger(LedgerEvent{Event: "prune", Channel: "cc", Alias: "a"}); err == nil {
		t.Error("an unknown kind should be rejected, closing the vocabulary")
	}
	if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "cc", Alias: "a"}); err != nil {
		t.Errorf("a complete event was rejected: %v", err)
	}
}

// Finding 5: the base field set is serialized even when empty, so a consumer reads
// known-absent rather than distinguishing a missing key from an old writer.
func TestBaseFieldsAreAlwaysSerialized(t *testing.T) {
	setupStore(t)
	if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "cc", Alias: "a"}); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(filepath.Join(CBUSDir(), ledgerDir, "cc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"harness", "host", "pid", "cwd", "origin", "sessionId", "formationRunId"} {
		if !strings.Contains(string(line), `"`+key+`"`) {
			t.Errorf("base key %q was omitted from a sparse event: %s", key, line)
		}
	}
}

// seedDeadMeta rewrites a peer's meta so it reads dead without removing the dir,
// standing in for a crashed session that nothing has reaped yet.
func seedDeadMeta(t *testing.T, metaPath string) {
	t.Helper()
	b, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// never-armed + a stamp far past the unarmed grace window = dead
	m["listenerPid"] = nil
	m["lastActivity"] = "2020-01-01T00:00:00Z"
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(metaPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func dirEntriesExist(root, ch string) int {
	entries, err := os.ReadDir(filepath.Join(root, ch))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

// The backward-compatibility claim that the whole additive approach rests on
// (bdx-mec.2): an OLDER binary must neither REJECT a snapshot carrying
// formationRunId nor DROP the key when it re-saves. This started as a throwaway
// probe run before any code was written; it is permanent because the property is
// what makes it safe to ship the new field while live formations keep running the
// installed binary.
//
// The mechanism is the Extra map: keys a version does not know are captured on
// decode and re-emitted on encode.
//
// Finding 3 caught the earlier version of this test as a FALSE POSITIVE: it decoded
// into the current Formation type, which knows formationRunId, so the field was
// absorbed by the typed struct and Extra was never exercised — it proved nothing
// about a binary that genuinely lacks the field. This version decodes through a
// FROZEN LEGACY CODEC (legacyFormation below) whose field set has NO run key, so the
// run id can only survive as an unknown-key round-trip, which is the real constraint.
func TestOldBinaryPreservesFormationRunID(t *testing.T) {
	const runID = "run_20260723T000000Z_abc123"
	newSnapshot := `{"schema":"cbus-formation/v1","name":"n","channel":"c","host":null,` +
		`"anchorAlias":"a","savedAt":"","savedBy":"","drift_anchors":null,"payload":null,` +
		`"formationRunId":"` + runID + `","peers":[{"alias":"a","formationRunId":"` + runID + `"}]}`

	var legacy legacyFormation
	if err := json.Unmarshal([]byte(newSnapshot), &legacy); err != nil {
		t.Fatalf("a legacy codec failed to decode a new snapshot: %v", err)
	}
	// the run key is UNKNOWN to this codec, so it must sit in Extra, not be lost
	if _, ok := legacy.Extra["formationRunId"]; !ok {
		t.Fatalf("legacy codec dropped the envelope run key: %+v", legacy.Extra)
	}
	if len(legacy.Peers) != 1 {
		t.Fatalf("legacy peer decode lost the peer: %+v", legacy.Peers)
	}
	if _, ok := legacy.Peers[0].Extra["formationRunId"]; !ok {
		t.Fatalf("legacy PEER codec dropped the per-peer run key: %+v", legacy.Peers[0].Extra)
	}
	out, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("legacy re-encode: %v", err)
	}
	// BOTH occurrences (envelope + peer) must survive an old binary round-trip
	if strings.Count(string(out), runID) != 2 {
		t.Fatalf("legacy binary dropped a run id on re-save (want 2 occurrences): %s", out)
	}

	// and the current codec must ROUND-TRIP the field, decode AND re-emit — reverting
	// its fields() entry would drop the key on save, which a decode-only check misses
	var f Formation
	if err := json.Unmarshal([]byte(newSnapshot), &f); err != nil {
		t.Fatalf("current decode: %v", err)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("schema was bumped — an older binary would reject this: %v", err)
	}
	if f.FormationRunID != runID {
		t.Errorf("current codec run id = %q, want %q", f.FormationRunID, runID)
	}
	reSaved, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("current re-encode: %v", err)
	}
	if strings.Count(string(reSaved), runID) != 2 {
		t.Fatalf("current codec dropped the run id on re-save (envelope+peer): %s", reSaved)
	}
}

// legacyFormation is a FROZEN replica of the pre-mec.2 envelope codec: the same
// Extra-map unknown-key machinery, but a field set that predates formationRunId. It
// stands in for the installed v0.8.1 binary, which has exactly this shape, so the
// backward-compat property is tested against a codec that genuinely lacks the field
// rather than one that quietly knows it.
type legacyFormation struct {
	Schema  string
	Name    string
	Channel string
	Peers   []legacyPeer
	Extra   map[string]json.RawMessage
}

// legacyPeer is the frozen pre-mec.2 FormationPeer codec: a field set WITHOUT a run
// key plus the same Extra unknown-key machinery. Finding 5 noted the earlier compat
// test carried peers as raw bytes, so per-peer formationRunId round-tripped without
// ever exercising an old peer codec. This exercises it directly.
type legacyPeer struct {
	Alias string
	Extra map[string]json.RawMessage
}

func (p legacyPeer) legacyFields() []jsonField { return []jsonField{{"alias", p.Alias}} }

func (p legacyPeer) MarshalJSON() ([]byte, error) {
	return marshalOrdered(p.legacyFields(), p.Extra)
}

func (p *legacyPeer) UnmarshalJSON(b []byte) error {
	var v struct {
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	extra, err := unknownKeys(b, knownKeys(legacyPeer{}.legacyFields()))
	if err != nil {
		return err
	}
	p.Alias, p.Extra = v.Alias, extra
	return nil
}

func (l legacyFormation) legacyFields() []jsonField {
	return []jsonField{
		{"schema", l.Schema}, {"name", l.Name}, {"channel", l.Channel},
		{"host", nil}, {"anchorAlias", ""}, {"savedAt", ""}, {"savedBy", ""},
		{"drift_anchors", nil}, {"payload", nil}, {"peers", l.Peers},
	}
}

func (l legacyFormation) MarshalJSON() ([]byte, error) {
	return marshalOrdered(l.legacyFields(), l.Extra)
}

func (l *legacyFormation) UnmarshalJSON(b []byte) error {
	var v struct {
		Schema  string       `json:"schema"`
		Name    string       `json:"name"`
		Channel string       `json:"channel"`
		Peers   []legacyPeer `json:"peers"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	extra, err := unknownKeys(b, knownKeys(legacyFormation{}.legacyFields()))
	if err != nil {
		return err
	}
	l.Schema, l.Name, l.Channel, l.Peers, l.Extra = v.Schema, v.Name, v.Channel, v.Peers, extra
	return nil
}

// A snapshot written BEFORE this field existed must still load, with the run id
// reading as unknown rather than as a guess.
func TestPreLedgerSnapshotStillLoads(t *testing.T) {
	old := `{"schema":"cbus-formation/v1","name":"n","channel":"c","host":null,` +
		`"anchorAlias":"a","savedAt":"","savedBy":"","drift_anchors":null,"payload":null,` +
		`"peers":[{"alias":"a","model":"","rolefile":"","role":null,"origin":"joined",` +
		`"mode":"template","sessionId":"S","onStale":"","profile":"","cwd":"",` +
		`"target":"pane","machine":"","addresses":[]}]}`
	var f Formation
	if err := json.Unmarshal([]byte(old), &f); err != nil {
		t.Fatalf("a pre-ledger snapshot failed to decode: %v", err)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("a pre-ledger snapshot failed validation: %v", err)
	}
	if f.FormationRunID != "" {
		t.Errorf("run id should read unknown, got %q", f.FormationRunID)
	}
}

// A session returning to its OWN meta is a resume, not a fresh arrival. The
// distinction is the point: two join events sharing a session id would force a
// consumer to infer continuity that the store already knows for a fact.
func TestResumeRejoinRecordsContinuity(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	// the process ended but the registration survived: same session comes back
	seedDeadMeta(t, filepath.Join(root, "cc", "coder", "meta.json"))
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	evs := ledgerLines(t, "cc")
	last := evs[len(evs)-1]
	if last.Event != LedgerResume {
		t.Fatalf("a same-session rejoin recorded %q, want %q", last.Event, LedgerResume)
	}
	if last.SessionID != "SID" || last.PrevSessionID != "SID" {
		t.Errorf("continuity not stated: sessionId=%q prevSessionId=%q", last.SessionID, last.PrevSessionID)
	}
	if evs[0].Event != LedgerJoin {
		t.Errorf("first arrival should be a join, got %q", evs[0].Event)
	}
}

// A reservation records the slot a parent opened for a child that has not booted.
// The child's session id must read as UNKNOWN, never as the "reserved" sentinel:
// writing a store placeholder into a sessionId field hands consumers a fake id.
func TestSpawnReservationRecordsNoFakeSessionID(t *testing.T) {
	setupStore(t)
	if _, err := ReserveAlias("cc", "child", OriginFresh, "opus"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	evs := ledgerLines(t, "cc")
	if len(evs) != 1 || evs[0].Event != LedgerSpawn {
		t.Fatalf("want one spawn event, got %+v", evs)
	}
	if evs[0].SessionID != "" {
		t.Errorf("sessionId = %q, want empty (the child has no session yet)", evs[0].SessionID)
	}
	if evs[0].Alias != "child" || evs[0].Origin != OriginFresh {
		t.Errorf("spawn facts wrong: %+v", evs[0])
	}
}

// Arming is the only moment the store learns which PROCESS holds the tail; join
// writes null pids because a joined peer has not armed yet.
func TestRebindRecordsTheListeningProcess(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	armMeta(filepath.Join(root, "cc", "coder", "meta.json"), startTokenOf(t, os.Getpid()))
	evs := ledgerLines(t, "cc")
	last := evs[len(evs)-1]
	if last.Event != LedgerRebind {
		t.Fatalf("arm recorded %q, want %q", last.Event, LedgerRebind)
	}
	if last.Pid != os.Getpid() {
		t.Errorf("pid = %d, want the arming process %d", last.Pid, os.Getpid())
	}
	if last.SessionID != "SID" || last.Alias != "coder" {
		t.Errorf("rebind lost identity: %+v", last)
	}
}

// Every wired kind must carry the run it belongs to, or a consumer cannot group a
// formation's events at all.
func TestAllWiredEventsCarryARunID(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	// a REAL start token, or the peer reads dead and the reaper eats it mid-test
	armMeta(filepath.Join(root, "cc", "coder", "meta.json"), startTokenOf(t, os.Getpid()))
	_, _ = ReserveAlias("cc", "child", OriginFork, "")
	_, _, _, _ = Rename("builder", "cc")
	_, _ = Leave("cc")
	evs := ledgerLines(t, "cc")
	if len(evs) < 5 {
		t.Fatalf("expected the wired kinds to fire, got %d events", len(evs))
	}
	kinds := map[string]bool{}
	for _, ev := range evs {
		if ev.FormationRunID == "" {
			t.Errorf("%s event carries no run id: %+v", ev.Event, ev)
		}
		kinds[ev.Event] = true
	}
	for _, want := range []string{LedgerJoin, LedgerRebind, LedgerSpawn, LedgerRename, LedgerLeave} {
		if !kinds[want] {
			t.Errorf("no %q event was recorded", want)
		}
	}
}

// Only a join opens a run; a re-arm reads the peer's own claim, never mints.
//
// Regression: an arm immediately after a join produced a SECOND run id for the same
// run, because non-join events resolved their run through the minting path. Under the
// claim model a rebind reads the live peer's claim and carries the open run unchanged.
func TestRebindNeverMintsANewRun(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	runID := ledgerLines(t, "cc")[0].FormationRunID
	if runID == "" {
		t.Fatal("join recorded no run id")
	}
	armMeta(filepath.Join(root, "cc", "coder", "meta.json"), startTokenOf(t, os.Getpid()))
	for _, ev := range ledgerLines(t, "cc") {
		if ev.FormationRunID != runID {
			t.Fatalf("%s event carries run %q, want the open run %q", ev.Event, ev.FormationRunID, runID)
		}
	}
}

// A peer's run claim dies with its dir on leave, so a finished run leaves no trace
// to inherit. This is the stale-run-file class removed at the root.
func TestRunClaimDiesWithThePeer(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	if readClaim(filepath.Join(root, "cc", "coder")) == "" {
		t.Fatalf("a joined peer should hold a run claim")
	}
	_, _ = Leave("cc")
	if currentRun("cc", "") != "" {
		t.Errorf("a run survived after every claiming peer left")
	}
	// the leave still carried the run it closed, read from the claim before removal
	evs := ledgerLines(t, "cc")
	if last := evs[len(evs)-1]; last.Event != LedgerLeave || last.FormationRunID == "" {
		t.Errorf("closing leave lost its run id: %+v", last)
	}
}

// A run stays current while ANY member still claims it.
func TestRunSurvivesAPartialDeparture(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	runID := readClaim(filepath.Join(root, "cc", "coder"))
	// a second live member of the same run
	other := filepath.Join(root, "cc", "other")
	_ = os.MkdirAll(other, 0o755)
	_ = os.WriteFile(filepath.Join(other, "meta.json"),
		[]byte(`{"sessionId":"OTHER","lastActivity":"`+Now()+`"}`), 0o644)
	writeClaim("cc", "other", runID)

	_, _ = Leave("cc") // coder leaves
	if got := currentRun("cc", ""); got != runID {
		t.Errorf("run %q lost while a live member remained: got %q", runID, got)
	}
}

// A minter that CRASHED holding the lock must not wedge later joins, and no caller
// may mint outside the lock (finding 2): two concurrent resolvers behind a fresh
// crashed lock must converge on one id, not split.
// A stale lock FILE left by a crashed minter must not block: flock is released by
// the kernel when the crashed process's fd closed, so the leftover file is unlocked
// and a new acquirer takes it immediately. Two concurrent resolvers still converge.
func TestStaleLockFileDoesNotBlockOrSplit(t *testing.T) {
	root := setupStore(t)
	_ = os.MkdirAll(filepath.Join(root, ledgerDir), 0o755)
	// a leftover lock file with no live holder (the crashed process's flock is gone)
	_ = os.WriteFile(filepath.Join(root, ledgerDir, ".cc.mint"), []byte("stale"), 0o644)

	for _, a := range []string{"a", "b"} {
		d := filepath.Join(root, "cc", a)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "meta.json"),
			[]byte(`{"sessionId":"S-`+a+`","lastActivity":"`+Now()+`"}`), 0o644)
	}
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i, a := range []string{"a", "b"} {
		wg.Add(1)
		go func(i int, a string) { defer wg.Done(); ids[i] = ResolveRunForJoin("cc", a, false) }(i, a)
	}
	wg.Wait()
	if ids[0] == "" || ids[1] == "" {
		t.Fatalf("a stale lock file blocked minting: %v", ids)
	}
	if ids[0] != ids[1] {
		t.Fatalf("concurrent resolve split the run: %q vs %q", ids[0], ids[1])
	}
}

// The deterministic seam reviewer1 asked for regardless of mechanism: while one
// holder has the mint lock, a second acquirer must NOT proceed — flock gives true
// mutual exclusion, so there is no stale-read/live-recreate/steal window to exploit.
func TestMintLockIsMutuallyExclusive(t *testing.T) {
	setupStore(t)
	release, ok := acquireMintLock("cc")
	if !ok {
		t.Fatal("first acquire failed")
	}
	got := make(chan bool, 1)
	go func() {
		r2, ok2 := acquireMintLock("cc")
		got <- ok2
		if ok2 {
			r2()
		}
	}()
	select {
	case <-got:
		t.Fatal("a second acquirer took the lock while it was held")
	case <-time.After(150 * time.Millisecond):
		// correctly blocked
	}
	release()
	if !<-got {
		t.Fatal("the second acquirer never got the lock after release")
	}
}

// SaveFormation must stamp the envelope run AND each on-channel peer's run, through
// production, not a hand-rolled repeat of the read. A kept off-channel peer retains
// its prior run.
func TestSnapshotSaveRecordsTheRun(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	want := readClaim(filepath.Join(root, "cc", "coder"))
	if want == "" {
		t.Fatal("no run claimed after join")
	}
	// a kept peer that is NOT on the channel, carrying a prior run
	seed := &Formation{Schema: FormationSchema, Name: "cc", Channel: "cc",
		Peers: []FormationPeer{{Alias: "ghost", Mode: ModeTemplate, OnStale: OnStaleTemplate,
			Target: "pane", FormationRunID: "run_OLD", Addresses: []string{}}}}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	f, _, err := SaveFormation("cc", "cc", nil)
	if err != nil {
		t.Fatalf("SaveFormation: %v", err)
	}
	if f.FormationRunID != want {
		t.Errorf("envelope run = %q, want %q", f.FormationRunID, want)
	}
	byAlias := map[string]FormationPeer{}
	for _, p := range f.Peers {
		byAlias[p.Alias] = p
	}
	if byAlias["coder"].FormationRunID != want {
		t.Errorf("on-channel peer run = %q, want %q", byAlias["coder"].FormationRunID, want)
	}
	if byAlias["ghost"].FormationRunID != "run_OLD" {
		t.Errorf("kept off-channel peer run overwritten: %q", byAlias["ghost"].FormationRunID)
	}
}

// reviewer1 #1 (and reviewer2's independent confirmation): a resumed session's id
// matches a HISTORICAL ledger binding, but the session has moved to a new run.
// Back-catalog identity must never prove current membership. This is the case a
// pure liveness+identity check got wrong; the claim model is what fixes it.
func TestResumedSessionDoesNotResurrectDeadRun(t *testing.T) {
	root := setupStore(t)
	// run_DEAD ran: alias a, session S, recorded in the ledger. The run then ended
	// uncleanly (a crash), so the ledger event survives.
	_ = AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "cc", Alias: "a", SessionID: "S", FormationRunID: "run_DEAD"})
	// the SAME session resumes under the SAME alias into a NEW formation and is live,
	// but crucially holds NO claim for run_DEAD (its dir was reclaimed on rejoin)
	liveUnarmedPeer(t, root, "cc", "a", "S")

	// a sibling joins; the historical a/S binding must not resurrect run_DEAD
	liveUnarmedPeer(t, root, "cc", "b", "S-B")
	id := ResolveRunForJoin("cc", "b", RunBoundary("cc"))
	if id == "run_DEAD" {
		t.Fatalf("a resumed session's historical binding resurrected the dead run")
	}
	// positive half, so this test bites even if inheritance were disabled entirely
	// (which would ALSO avoid run_DEAD, but by minting a distinct id for everyone):
	// b's fresh run must now be inheritable by a live sibling.
	liveUnarmedPeer(t, root, "cc", "c", "S-C")
	if got := ResolveRunForJoin("cc", "c", true); got != id {
		t.Fatalf("inheritance is broken: sibling minted %q instead of inheriting %q", got, id)
	}
}

// reviewer2's two reused-channel repros, kept permanently. A stale channel-level run
// record must never be inherited on its own; only a live claim counts.
func TestReusedChannelDoesNotResurrectDeadRunID(t *testing.T) {
	root := setupStore(t)
	// a previous formation's live-but-unclaimed peer (pre-ledger / mixed-binary shape)
	liveUnarmedPeer(t, root, "cc", "a", "SID-A")
	// no claim anywhere names a run, so a joiner must mint fresh, not inherit nothing
	liveUnarmedPeer(t, root, "cc", "b", "SID-B")
	id := ResolveRunForJoin("cc", "b", RunBoundary("cc"))
	if id == "" {
		t.Fatal("joiner failed to mint a run on a channel with no claimed run")
	}
}

func TestConcurrentJoinersOnReusedChannelConverge(t *testing.T) {
	root := setupStore(t)
	// two peers writing meta then resolving concurrently into an empty channel
	for _, a := range []string{"a", "b"} {
		liveUnarmedPeer(t, root, "cc", a, "SID-"+a)
	}
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i, a := range []string{"a", "b"} {
		wg.Add(1)
		go func(i int, a string) { defer wg.Done(); ids[i] = ResolveRunForJoin("cc", a, false) }(i, a)
	}
	wg.Wait()
	if ids[0] != ids[1] {
		t.Fatalf("one formation, two run ids: %q vs %q", ids[0], ids[1])
	}
}

// liveUnarmedPeer fabricates a peer dir whose meta reads alive (unarmed grace)
// without going through Join, so it holds no claim — modelling both a pre-ledger
// peer and a new peer mid-join before it has resolved.
func liveUnarmedPeer(t *testing.T, root, ch, alias, sid string) {
	t.Helper()
	dir := filepath.Join(root, ch, alias)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"sessionId":"` + sid + `","lastActivity":"` + Now() + `"}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A restore (formation apply relaunching a peer) records facts that exist ONLY at
// the applier: which run the snapshot came from and which session was relaunched.
// The child has not booted, so its own join cannot know it was restored.
func TestRestoreRecordsContinuityAndClearsLauncherPid(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	applierOn(t, "ch", "applier")
	f := applyFixture(
		peer("coder", func(p *FormationPeer) {
			p.Machine = ShortHostname()
			p.SessionID = "PRIOR-SID"
			p.Origin = OriginFork
		}),
	)
	if _, err := applyWith(t, f, ApplyOptions{}, &recForker{}, nil); err != nil {
		t.Fatal(err)
	}
	var restore *LedgerEvent
	for i := range ReadLedger("ch") {
		if ev := ReadLedger("ch")[i]; ev.Event == LedgerRestore {
			restore = &ev
		}
	}
	if restore == nil {
		t.Fatal("apply launched a peer but recorded no restore event")
	}
	if restore.Alias != "coder" || restore.PrevSessionID != "PRIOR-SID" {
		t.Errorf("restore continuity wrong: %+v", *restore)
	}
	if restore.Origin != OriginFork {
		t.Errorf("restore dropped the peer's origin: %+v", *restore)
	}
	// the APPLIER launched the child; the applier's pid is not the child's
	if restore.Pid != 0 {
		t.Errorf("restore stamped the launcher's pid onto the unborn child: %+v", *restore)
	}
}

// Harness is stamped from the ancestor-walk identity, normalized so claude-* build
// flavours collapse to one name. Under `go test` there is no coding-harness ancestor,
// so HarnessName is empty — which is the honest answer, not a guess.
func TestHarnessNameNormalization(t *testing.T) {
	cases := map[string]string{
		"claude": "claude", "claude-2.1.214": "claude", "claude-nightly": "claude",
		"codex": "codex", "grok": "grok", "opencode": "opencode",
	}
	for in, want := range cases {
		if got := normalizeHarness(in); got != want {
			t.Errorf("normalizeHarness(%q) = %q, want %q", in, got, want)
		}
	}
	// no harness ancestor under the test runner: empty, never inferred
	if h := HarnessName(); h != "" && h != "claude" && h != "codex" && h != "grok" && h != "opencode" {
		t.Errorf("HarnessName returned an unnormalized value: %q", h)
	}
}

// Finding 1: the claim file is the identity AUTHORITY, so a failed claim write must
// yield a BLANK run, never a nonempty id nothing on disk backs (which would make the
// join event lie and split the next sibling's run). Both legs — write and rename.
func TestFailedClaimWriteYieldsBlankRun(t *testing.T) {
	root := setupStore(t)
	_ = os.MkdirAll(filepath.Join(root, "cc", "a"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "cc", "a", "meta.json"),
		[]byte(`{"sessionId":"S","lastActivity":"`+Now()+`"}`), 0o644)

	origW, origR := claimWrite, claimRename
	defer func() { claimWrite, claimRename = origW, origR }()

	claimWrite = func(string, []byte, os.FileMode) error { return fmt.Errorf("disk full") }
	if id := ResolveRunForJoin("cc", "a", false); id != "" {
		t.Errorf("write failure reported a nonempty run %q that is not on disk", id)
	}
	if readClaim(filepath.Join(root, "cc", "a")) != "" {
		t.Errorf("a claim was left on disk despite the write failing")
	}

	claimWrite = origW
	claimRename = func(string, string) error { return fmt.Errorf("rename failed") }
	if id := ResolveRunForJoin("cc", "a", false); id != "" {
		t.Errorf("rename failure reported a nonempty run %q", id)
	}
	if _, err := os.Stat(filepath.Join(root, "cc", "a", ".run.tmp."+strconv.Itoa(os.Getpid()))); err == nil {
		t.Errorf("rename failure left the temp file behind")
	}
}

// Finding 2: snapshot run identity derives from the roster being saved, dead peers
// included; clears a stale per-peer id when the peer holds no claim; and surfaces a
// conflict instead of picking one.
func TestSnapshotRunFromRoster(t *testing.T) {
	t.Run("dead peer's claim still sets the envelope", func(t *testing.T) {
		root := setupStore(t)
		_, _, _ = Join("cc", "coder")
		want := readClaim(filepath.Join(root, "cc", "coder"))
		seedDeadMeta(t, filepath.Join(root, "cc", "coder", "meta.json")) // dying peer
		f, _, err := SaveFormation("cc", "cc", nil)
		if err != nil {
			t.Fatal(err)
		}
		if f.FormationRunID != want {
			t.Errorf("dead peer's valid claim ignored: envelope=%q want %q", f.FormationRunID, want)
		}
	})
	t.Run("no claim clears a stale per-peer id", func(t *testing.T) {
		root := setupStore(t)
		// a live on-channel peer with NO claim (old binary / failed claim)
		liveUnarmedPeer(t, root, "cc", "coder", "S")
		seed := &Formation{Schema: FormationSchema, Name: "cc", Channel: "cc",
			Peers: []FormationPeer{{Alias: "coder", Mode: ModeTemplate, OnStale: OnStaleTemplate,
				Target: "pane", FormationRunID: "run_STALE", Addresses: []string{}}}}
		if err := seed.Save(); err != nil {
			t.Fatal(err)
		}
		f, _, err := SaveFormation("cc", "cc", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range f.Peers {
			if p.Alias == "coder" && p.FormationRunID != "" {
				t.Errorf("stale run id retained on a claimless peer: %q", p.FormationRunID)
			}
		}
	})
	t.Run("conflicting claims are surfaced, envelope left blank", func(t *testing.T) {
		root := setupStore(t)
		liveUnarmedPeer(t, root, "cc", "a", "SA")
		liveUnarmedPeer(t, root, "cc", "b", "SB")
		t.Setenv("CLAUDE_CODE_SESSION_ID", "SA") // the saver is a peer, so the mint resolves an anchor
		writeClaim("cc", "a", "run_X")
		writeClaim("cc", "b", "run_Y")
		f, rep, err := SaveFormation("cc", "cc", nil)
		if err != nil {
			t.Fatal(err)
		}
		if f.FormationRunID != "" {
			t.Errorf("a conflicting-claim channel picked a run: %q", f.FormationRunID)
		}
		if len(rep.RunConflict) != 2 {
			t.Errorf("conflict not surfaced: %v", rep.RunConflict)
		}
	})
}

// Finding 3: a forced removal (cbus unregister) must leave a terminal event, marked
// forced so it is distinguishable from a self-leave or a crash-reap.
func TestUnregisterEmitsForcedLeave(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "coder")
	runID := readClaim(filepath.Join(root, "cc", "coder"))
	if err := Unregister("cc", "coder"); err != nil {
		t.Fatal(err)
	}
	evs := ledgerLines(t, "cc")
	last := evs[len(evs)-1]
	if last.Event != LedgerLeave || last.Emitter != EmitterForced {
		t.Fatalf("forced removal recorded no forced leave: %+v", last)
	}
	if last.Alias != "coder" || last.SessionID != "SID" || last.FormationRunID != runID {
		t.Errorf("forced leave lost subject facts: %+v", last)
	}
}

// reviewer1 round 3, finding 2: the authority principle is per-EVENT, not per-join. A
// peer whose join-claim FAILED has no claim of its own; once a sibling mints a run,
// that peer's later rebind/rename must NOT adopt the sibling's run. Absence of a
// peer's own claim is unknown (blank) always, never inferred from a sibling.
func TestFailedClaimStaysBlankOnLaterEvents(t *testing.T) {
	root := setupStore(t)
	// the failing peer: on disk, live, but its claim write fails
	liveUnarmedPeer(t, root, "cc", "failer", "S-FAIL")
	origW := claimWrite
	claimWrite = func(string, []byte, os.FileMode) error { return fmt.Errorf("disk full") }
	if id := ResolveRunForJoin("cc", "failer", false); id != "" {
		t.Fatalf("join with a failed claim reported a run: %q", id)
	}
	claimWrite = origW
	if readClaim(filepath.Join(root, "cc", "failer")) != "" {
		t.Fatal("a claim landed on disk despite the injected failure")
	}

	// a sibling now legitimately mints and claims a run R
	liveUnarmedPeer(t, root, "cc", "sib", "S-SIB")
	R := ResolveRunForJoin("cc", "sib", true)
	if R == "" {
		t.Fatal("sibling failed to mint")
	}

	// the failer's later rebind must stay blank — never adopt R from the sibling
	RecordEvent(LedgerRebind, "cc", "failer", "S-FAIL", nil)
	evs := ledgerLines(t, "cc")
	last := evs[len(evs)-1]
	if last.Event != LedgerRebind {
		t.Fatalf("want rebind last, got %q", last.Event)
	}
	if last.FormationRunID != "" {
		t.Fatalf("a claimless peer's event inferred a sibling's run %q (authority leak)", last.FormationRunID)
	}
	if readClaim(filepath.Join(root, "cc", "failer")) != "" {
		t.Error("a claim appeared for the failer after a sibling event")
	}
}

// The launcher-authored escape hatch: spawn/restore DO carry the acting run, because
// the launcher is the authority for the child slot it creates. A reservation made by
// a live parent inherits the parent's run explicitly.
func TestSpawnCarriesTheParentsRunExplicitly(t *testing.T) {
	root := setupStore(t)
	_, _, _ = Join("cc", "parent")
	parentRun := readClaim(filepath.Join(root, "cc", "parent"))
	if _, err := ReserveAlias("cc", "child", OriginFork, ""); err != nil {
		t.Fatal(err)
	}
	var spawn *LedgerEvent
	for _, ev := range ReadLedger("cc") {
		if ev.Event == LedgerSpawn {
			e := ev
			spawn = &e
		}
	}
	if spawn == nil {
		t.Fatal("no spawn event recorded")
	}
	if spawn.FormationRunID != parentRun {
		t.Errorf("spawn run = %q, want the parent's run %q", spawn.FormationRunID, parentRun)
	}
}

// reviewer2 round-3 nit: a launcher-authored event must carry the LAUNCHER'S OWN run,
// not "any live claim on the channel". Those agree in a converged run and diverge
// exactly during a split — where sourcing any claim would attribute the child to a run
// its parent is not in. Constructed so the parent's claim is NOT the first enumerated.
func TestSpawnUsesTheParentsOwnRunNotAnyLiveClaim(t *testing.T) {
	root := setupStore(t)
	// this session joins as "zzz" so it sorts LAST: a first-claim-wins implementation
	// would pick "aaa"'s run instead of ours
	if _, _, err := Join("cc", "zzz"); err != nil {
		t.Fatal(err)
	}
	parentRun := readClaim(filepath.Join(root, "cc", "zzz"))
	if parentRun == "" {
		t.Fatal("parent holds no claim")
	}
	// a split: another live peer claiming a DIFFERENT run, enumerating first
	liveUnarmedPeer(t, root, "cc", "aaa", "S-OTHER")
	if err := writeClaim("cc", "aaa", "run_OTHER"); err != nil {
		t.Fatal(err)
	}

	if _, err := ReserveAlias("cc", "child", OriginFork, ""); err != nil {
		t.Fatal(err)
	}
	var spawn *LedgerEvent
	for _, ev := range ReadLedger("cc") {
		if ev.Event == LedgerSpawn {
			e := ev
			spawn = &e
		}
	}
	if spawn == nil {
		t.Fatal("no spawn event recorded")
	}
	if spawn.FormationRunID == "run_OTHER" {
		t.Fatalf("spawn took another peer's run during a split instead of the parent's")
	}
	if spawn.FormationRunID != parentRun {
		t.Fatalf("spawn run = %q, want the reserving parent's own run %q", spawn.FormationRunID, parentRun)
	}
}

// reviewer1 round-3 remaining HIGH: a join into an ALREADY-SPLIT roster must not
// adopt whichever alias sorts first. Multiple live authorities disagreeing means the
// run is unknown; no caller may pick one. Blank claim, blank event, loud warning —
// the same rule SaveFormation already applied, now applied by the join path too.
func TestJoinIntoSplitRosterTakesNoSide(t *testing.T) {
	root := setupStore(t)
	// a live split: aaa claims run_X (enumerates first), zzz claims run_Y
	liveUnarmedPeer(t, root, "cc", "aaa", "S-A")
	liveUnarmedPeer(t, root, "cc", "zzz", "S-Z")
	if err := writeClaim("cc", "aaa", "run_X"); err != nil {
		t.Fatal(err)
	}
	if err := writeClaim("cc", "zzz", "run_Y"); err != nil {
		t.Fatal(err)
	}

	// drive the REAL Join path and capture stderr, so this test covers what its name
	// and the report claim: the emitted event, the absent claim, and the warning —
	// not merely ResolveRunForJoin's return value.
	warn := captureStderr(t, func() {
		if _, _, err := Join("cc", "m"); err != nil {
			t.Fatalf("a peer must still be able to JOIN a split channel: %v", err)
		}
	})

	// the emitted JOIN event must carry no run
	var join *LedgerEvent
	for _, ev := range ReadLedger("cc") {
		if ev.Event == LedgerJoin && ev.Alias == "m" {
			e := ev
			join = &e
		}
	}
	if join == nil {
		t.Fatal("no join event recorded for the joiner")
	}
	if join.FormationRunID != "" {
		t.Fatalf("join event took a side in a split roster: %q", join.FormationRunID)
	}
	// and no claim of its own
	if got := readClaim(filepath.Join(root, "cc", "m")); got != "" {
		t.Errorf("joiner claimed %q despite the split", got)
	}
	// the split must be ANNOUNCED, naming both runs — an arbitrary adoption would
	// hide exactly the condition an operator needs to see
	if !strings.Contains(warn, "run_X") || !strings.Contains(warn, "run_Y") {
		t.Errorf("the split warning did not name both runs:\n%s", warn)
	}
	if !strings.Contains(warn, "WARNING") {
		t.Errorf("the split was not announced on stderr:\n%s", warn)
	}
	// currentRun must report the split as unknown, not first-wins
	if got := currentRun("cc", ""); got != "" {
		t.Errorf("currentRun picked %q from a split roster", got)
	}
	if ids := liveRuns("cc", ""); len(ids) != 2 || ids[0] != "run_X" || ids[1] != "run_Y" {
		t.Errorf("liveRuns did not report both claims deterministically: %v", ids)
	}
}

// captureStderr collects what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}
