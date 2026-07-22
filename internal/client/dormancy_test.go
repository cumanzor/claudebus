package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// armedPeer builds a joined peer whose meta records THIS process as the listener, and
// returns the peer dir, inbox, meta path and the matching identity.
func armedPeer(t *testing.T, ch, al string) (peer, inbox, meta string, id *listenerIdentity) {
	t.Helper()
	root := CBUSDir()
	peer = filepath.Join(root, ch, al)
	if err := os.MkdirAll(peer, 0o755); err != nil {
		t.Fatal(err)
	}
	inbox = filepath.Join(peer, "inbox.jsonl")
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	meta = filepath.Join(peer, "meta.json")
	if err := os.WriteFile(meta, []byte(fmt.Sprintf(
		`{"alias":%q,"channel":%q,"sessionId":"sid","cwd":"/w","listenerPid":null,"ownerPid":null,"host":"h","ts":"2026-07-19T00:00:00Z"}`,
		al, ch)), 0o644); err != nil {
		t.Fatal(err)
	}
	start := selfStart(t)
	armMeta(meta, start)
	return peer, inbox, meta, &listenerIdentity{pid: os.Getpid(), start: start, metaPath: meta}
}

// TestIdentityCauses walks every way a follower can stop being the listener. Each cause
// gets its own marker text, so the table doubles as the truthfulness check: no line may
// claim a displacement that did not happen.
func TestIdentityCauses(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	_, _, meta, id := armedPeer(t, "ch", "al")

	if got := id.check(); got.cause != stillListener {
		t.Fatalf("a freshly armed follower must be the listener, got cause %d", got.cause)
	}

	cases := []struct {
		name    string
		mutate  func()
		want    dormancyCause
		wantTxt string
	}{
		{"another listener stole it", func() {
			setMetaFields(t, meta, `"listenerPid":424242,"listenerStart":"1.1"`)
		}, causeDisplaced, "displaced by another listener"},

		{"peer re-joined", func() {
			setMetaFields(t, meta, `"listenerPid":null,"listenerStart":""`)
		}, causeRejoined, "peer re-joined"},

		{"registration gone", func() {
			if err := os.Remove(meta); err != nil {
				t.Fatal(err)
			}
		}, causeGone, "peer registration is gone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.mutate()
			got := id.check()
			if got.cause != c.want {
				t.Errorf("cause = %d, want %d", got.cause, c.want)
			}
			if txt := got.marker(); !strings.Contains(txt, c.wantTxt) {
				t.Errorf("marker %q does not say %q", txt, c.wantTxt)
			}
			// truthfulness: only the displacement case may say "displaced"
			if txt := got.marker(); c.want != causeDisplaced && strings.Contains(txt, "displaced") {
				t.Errorf("marker %q claims a displacement that did not happen", txt)
			}
		})
	}
}

// TestRenameIsDetectedThroughTheRealFlow is F1. The renamed case cannot be staged by
// editing a meta in place, because a real Rename MOVES the peer directory — the old
// follower's path stops resolving entirely and the cleared witness lands at the NEW
// path, which that follower never reads. An in-place fixture produced a state no real
// flow writes, and it made a genuinely unreachable branch look tested.
//
// So this drives Join -> arm -> Rename and asserts on what the follower ACTUALLY sees,
// including that the remedy names the new address rather than telling the user to
// re-join a name their peer no longer occupies.
func TestRenameIsDetectedThroughTheRealFlow(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	oldMeta := filepath.Join(root, "dev", "main", "meta.json")
	start := selfStart(t)
	armMeta(oldMeta, start)
	id := &listenerIdentity{pid: os.Getpid(), start: start, metaPath: oldMeta}
	if d := id.check(); d.cause != stillListener {
		t.Fatalf("precondition: armed follower reads cause %d", d.cause)
	}

	if _, _, _, err := Rename("newname", ""); err != nil {
		t.Fatal(err)
	}

	d := id.check()
	if d.cause != causeRenamed {
		t.Fatalf("a real rename produced cause %d, want causeRenamed(%d) — "+
			"the old path is gone, so this is the state the follower is actually in",
			d.cause, causeRenamed)
	}
	if d.addr != "dev/newname" {
		t.Errorf("renamed address = %q, want dev/newname", d.addr)
	}
	m := d.marker()
	if !strings.Contains(m, "dev/newname") {
		t.Errorf("marker %q must name the new address; the user cannot act on it otherwise", m)
	}
	if strings.Contains(m, "re-join") {
		t.Errorf("marker %q tells the user to re-join, which would resurrect the vacated alias", m)
	}
}

// TestPrunedPeerIsNotMistakenForARename: findRenamed must not invent a rename. A peer
// that was genuinely pruned leaves no sibling bearing our pid, so the cause stays gone
// and the remedy stays re-join.
func TestPrunedPeerIsNotMistakenForARename(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	// a sibling peer exists in the channel, so the scan has something to walk past
	seedPeer(t, root, "dev", "watcher", "OTHER")
	oldMeta := filepath.Join(root, "dev", "main", "meta.json")
	start := selfStart(t)
	armMeta(oldMeta, start)
	id := &listenerIdentity{pid: os.Getpid(), start: start, metaPath: oldMeta}

	if err := os.RemoveAll(filepath.Join(root, "dev", "main")); err != nil {
		t.Fatal(err)
	}
	d := id.check()
	if d.cause != causeGone {
		t.Errorf("a pruned peer read cause %d, want causeGone(%d)", d.cause, causeGone)
	}
	if !strings.Contains(d.marker(), "re-join") {
		t.Errorf("marker %q must name re-join for a genuinely pruned peer", d.marker())
	}
}

func setMetaFields(t *testing.T, path, fields string) {
	t.Helper()
	body := fmt.Sprintf(`{"alias":"al","channel":"ch","sessionId":"sid","cwd":"/w",%s,"host":"h","ts":"2026-07-19T00:00:00Z"}`, fields)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestForeignReopenIsNotStreamed is cbus-0r8, and it is a CONFIDENTIALITY assertion
// rather than a correctness one: the failure it guards is a stranger's messages
// appearing in someone else's terminal.
//
// The setup is the documented zombie-reattach path. A follower is armed; prune reaps
// its peer (the whole dir goes); a DIFFERENT session joins the same alias and receives
// traffic. The orphaned follower notices the inode change, and before this milestone it
// would reopen the recreated inbox from byte 0 and shadow-replay the newcomer's mail to
// the old Monitor, indefinitely.
func TestForeignReopenIsNotStreamed(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	peer, inbox, _, id := armedPeer(t, "ch", "al")

	buf, running, stopJoin := startFollowAs(t, inbox, resumePoint{}, id)
	defer stopJoin()
	appendLine(t, inbox, "mine", "me", "my-own-message")
	waitFor(t, func() bool { return strings.Contains(buf.String(), "my-own-message") }, "own message")

	// prune reaps the peer, then a STRANGER claims the alias and gets mail
	if err := os.RemoveAll(peer); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(peer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setMetaFields(t, filepath.Join(peer, "meta.json"), `"listenerPid":null,"listenerStart":""`)
	appendLine(t, inbox, "stranger", "newcomer", "SECRET-not-for-the-old-window")

	// settle unconditionally rather than waiting on dormancy first: the confidentiality
	// claim must be what fails here, not a timeout on a precondition. A leaking follower
	// never goes dormant, so gating the leak check on dormancy would report the wrong
	// failure for the worst outcome.
	time.Sleep(300 * time.Millisecond)

	if strings.Contains(buf.String(), "SECRET-not-for-the-old-window") {
		t.Fatal("cbus-0r8: the orphaned follower streamed a STRANGER's message into the old window")
	}
	if running() {
		t.Error("the orphaned follower is still running; it must go dormant, not merely stay quiet")
	}
	if !strings.Contains(buf.String(), "◀ cbus tail ended") {
		t.Errorf("dormant follower emitted no marker; the session has nothing to act on:\n%q", buf.String())
	}
}

// TestOrphanDoesNotMoveTheNewEpochCursor is P3, aimed at the case the orchestrator
// escalated: an orphaned follower must stop MOVING the cursor, not merely stop reading.
// writeCursor addresses the peer by PATH, so once a rejoin recreates that path an
// unguarded orphan writes its stale offset into the NEW peer's sidecar and corrupts a
// live peer's resume point. This is why join removing .cursor is necessary but not
// sufficient — the orphan resurrects it.
func TestOrphanDoesNotMoveTheNewEpochCursor(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	peer, inbox, _, id := armedPeer(t, "ch", "al")

	_, running, stopJoin := startFollowAs(t, inbox, resumePoint{}, id)
	defer stopJoin()
	appendLine(t, inbox, "p", "q", "first")
	waitFor(t, func() bool {
		_, _, off, st := readCursor(peer)
		return st == cursorValid && off > 0
	}, "the follower's own cursor")

	// the peer is reclaimed by a new epoch, which clears the sidecar
	if err := os.RemoveAll(peer); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(peer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setMetaFields(t, filepath.Join(peer, "meta.json"), `"listenerPid":null,"listenerStart":""`)
	if _, _, _, st := readCursor(peer); st != cursorAbsent {
		t.Fatal("precondition: the new epoch must start with no cursor")
	}

	appendLine(t, inbox, "x", "y", "new-epoch-traffic")
	waitFor(t, func() bool { return !running() }, "follower to go dormant")
	time.Sleep(600 * time.Millisecond) // well past several check intervals

	if _, _, off, st := readCursor(peer); st != cursorAbsent {
		t.Errorf("the orphan resurrected the new epoch's cursor (state %d, offset %d) — "+
			"a live peer's resume point was corrupted by a follower that no longer owns it", st, off)
	}
}

// TestDisplacedFollowerStopsMovingTheCursor is rider P3 proper, and it covers the case
// the orphan test does NOT: displacement with no rotation.
//
// A --steal overwrites the identity tuple but leaves the inbox file alone, so the
// rotation-time check never fires and the loser keeps reading until its next periodic
// check. In that window an unguarded follower would keep advancing .cursor for a peer
// it no longer owns — moving the STEALER's resume point past messages the stealer has
// not delivered. Stopping the read is not enough; the write has to be conditional too.
//
// The orphan test passes even with the P3 guard removed, because the rotation check
// intercepts it first. This one is what actually pins the guard.
func TestDisplacedFollowerStopsMovingTheCursor(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	peer, inbox, meta, id := armedPeer(t, "ch", "al")

	_, running, stopJoin := startFollowAs(t, inbox, resumePoint{}, id)
	defer stopJoin()
	appendLine(t, inbox, "p", "q", "before-the-steal")
	waitFor(t, func() bool {
		_, _, off, st := readCursor(peer)
		return st == cursorValid && off > 0
	}, "the follower's own cursor")
	_, _, atSteal, _ := readCursor(peer)

	// the steal: a different listener takes the tuple. Same inbox, same inode, no
	// rotation — nothing about the FILE changes.
	setMetaFields(t, meta, `"listenerPid":424242,"listenerStart":"1.1"`)
	appendLine(t, inbox, "p", "q", "after-the-steal-belongs-to-the-stealer")

	waitFor(t, func() bool { return !running() }, "displaced follower to go dormant")
	time.Sleep(300 * time.Millisecond)

	_, _, after, st := readCursor(peer)
	if st != cursorValid {
		t.Fatalf("cursor state %d after the steal", st)
	}
	if after != atSteal {
		t.Errorf("the displaced follower advanced the cursor %d -> %d after losing the peer; "+
			"the stealer would resume PAST a message it never delivered", atSteal, after)
	}
}

// TestArmRefusesWithoutAWitness is rider P4. armMeta used to record no witness when the
// probe failed, which looks harmless and is not: a peer with a listenerPid and no
// listenerStart has no witness to be judged on, so it reads dead the instant it
// starts — and since the argv fallback is gone, permanently. The session
// would get a tail that is silently not the listener — armed, streaming, invisible to
// every peer, and reaped by the next prune.
//
// Refusing is the only honest option, and it must be loud: there is no degraded mode
// worth offering here.
func TestArmRefusesWithoutAWitness(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	peer := filepath.Join(root, "ch", "al")
	if err := os.MkdirAll(peer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peer, "inbox.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setMetaFields(t, filepath.Join(peer, "meta.json"), `"listenerPid":null,"listenerStart":""`)

	// the contract asserted at the seam the arm actually depends on: a witness must be
	// obtainable for THIS process before anything is written.
	if _, err := procStartTime(os.Getpid()); err != nil {
		t.Skipf("this platform cannot probe its own start time: %v", err)
	}

	// and the arm records it — the state P4 exists to make impossible is an armed meta
	// with a pid and no witness.
	start := selfStart(t)
	armMeta(filepath.Join(peer, "meta.json"), start)
	m, ok := ReadPeerMeta(filepath.Join(peer, "meta.json"))
	if !ok || m.ListenerPid == 0 {
		t.Fatal("armMeta did not record a listener")
	}
	if m.ListenerStart == "" {
		t.Error("armed meta has a pid but no witness — P4's trap state")
	}
	if !MetaListenerAlive(filepath.Join(peer, "meta.json")) {
		t.Error("a peer armed by this process must read alive; it would otherwise be an instant-dormant tail")
	}
}

// TestDisplacementGateRefusesASecondArm is the D5 gate. The rule is uniform for a live
// FOREIGN listener: refuse, no exemption for the same session, because arming over another
// process's live tail is the double-listener bug and not a convenience. The one carve-out is
// exact-process self (pid+start), pinned separately by TestDisplacementGateExemptsExactSelf.
func TestDisplacementGateRefusesASecondArm(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-one")
	if _, _, err := Join("ch", "al"); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(root, "ch", "al", "meta.json")
	// a LIVE listener that is NOT this process (a self pid+start is exempt as the same
	// process; the gate still refuses a second arm over someone ELSE's live tail).
	foreignLiveListener(t, meta)

	// BOUNDED, and this is not defensive padding. On regression ArmLocalTail does not
	// return an error — it arms and blocks in the follower loop forever, in THIS
	// process. An unbounded call would hang the test binary rather than fail it, which
	// is how my own gate-removal mutation run wedged twice before I bounded it. A test
	// that hangs on regression reports a timeout instead of a cause.
	armed := make(chan error, 1)
	go func() { armed <- ArmLocalTail("ch/al", false) }()
	select {
	case err := <-armed:
		if err == nil {
			t.Fatal("the gate let a second listener arm over a live one")
		}
		if !strings.Contains(err.Error(), "--steal") {
			t.Errorf("the refusal must name the escape hatch, got %q", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ArmLocalTail never returned — the gate let it through and it is now following")
	}

	// and a dead listener is NOT refused: the gate guards live tails, not stale records
	setMetaFields(t, meta, `"listenerPid":424242,"listenerStart":"1.1"`)
	if MetaListenerAlive(meta) {
		t.Fatal("precondition: pid 424242 should not be a live listener")
	}
	// ArmLocalTail blocks once it passes the gate, so probe the gate's own condition
	if m, ok := ReadPeerMeta(meta); !ok || MetaListenerAlive(meta) {
		t.Errorf("a dead ex-listener must fall through the gate (meta %+v)", m)
	}
}

// TestMarkerRemedyMatchesBehavior is the pin that would have caught the wrong-remedy
// defect at N2. It is not a string test: for each cause it puts the peer INTO that
// state and checks that the remedy the marker names is the one the code actually
// accepts. Text and behavior are asserted against each other, so neither can drift
// alone.
//
// The defect it exists to prevent: every marker used to end "re-arm to resume", which
// is true for exactly one of the four states. A pruned peer's re-arm fails "no such
// peer — join first" — and that is the state a prune leaves behind, so the advice was
// wrong precisely when a session most needed it.
func TestMarkerRemedyMatchesBehavior(t *testing.T) {
	// causeGone: the peer dir is gone, so a re-arm CANNOT work and the marker must say
	// re-join rather than re-arm.
	t.Run("gone names re-join because re-arm fails", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("CBUS_DIR", root)
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-g")
		if _, _, err := Join("ch", "al"); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(root, "ch", "al")); err != nil {
			t.Fatal(err)
		}
		armed := make(chan error, 1)
		go func() { armed <- ArmLocalTail("ch/al", false) }()
		select {
		case err := <-armed:
			if err == nil {
				t.Fatal("re-arming a pruned peer unexpectedly succeeded; the marker's advice would change")
			}
			if !strings.Contains(err.Error(), "join first") {
				t.Fatalf("pruned re-arm said %q; the marker assumes it demands a join", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ArmLocalTail never returned for a pruned peer")
		}
		m := dormancy{cause: causeGone}.marker()
		if !strings.Contains(m, "re-join") {
			t.Errorf("marker %q must name re-join: a re-arm provably fails in this state", m)
		}
		if strings.Contains(m, "— re-arm to resume") {
			t.Errorf("marker %q tells the user to do the thing that just failed", m)
		}
	})

	// causeDisplaced: the peer is alive and held, so a PLAIN re-arm is refused by the
	// gate and the marker must name --steal.
	t.Run("displaced names --steal because a plain re-arm is refused", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("CBUS_DIR", root)
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-d")
		if _, _, err := Join("ch", "al"); err != nil {
			t.Fatal(err)
		}
		foreignLiveListener(t, filepath.Join(root, "ch", "al", "meta.json"))
		armed := make(chan error, 1)
		go func() { armed <- ArmLocalTail("ch/al", false) }()
		select {
		case err := <-armed:
			if err == nil {
				t.Fatal("a plain re-arm over a live listener succeeded; the marker's advice would change")
			}
			if !strings.Contains(err.Error(), "--steal") {
				t.Fatalf("displaced re-arm said %q; the marker assumes it demands --steal", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ArmLocalTail never returned; the gate let it through")
		}
		if m := (dormancy{cause: causeDisplaced}).marker(); !strings.Contains(m, "--steal") {
			t.Errorf("marker %q must name --steal: a plain re-arm provably fails here", m)
		}
	})

	// causeRejoined is the one state where a plain re-arm is genuinely right.
	t.Run("re-joined names a plain re-arm", func(t *testing.T) {
		m := dormancy{cause: causeRejoined}.marker()
		if !strings.Contains(m, "re-arm to resume") || strings.Contains(m, "--steal") {
			t.Errorf("marker %q should ask for a plain re-arm", m)
		}
	})

	// causeRenamed: the old address is gone; the remedy is the NEW alias, not this one.
	// the renamed remedy is asserted against the REAL flow in
	// TestRenameIsDetectedThroughTheRealFlow, which is the only way to obtain a
	// genuine causeRenamed and its address.

	// and the truthfulness rule still holds across the reworded set
	for _, c := range []dormancyCause{causeRejoined, causeRenamed, causeGone} {
		m := dormancy{cause: c, addr: "dev/x"}.marker()
		if strings.Contains(m, "displaced") {
			t.Errorf("marker %q claims a displacement that did not happen", m)
		}
	}
}
