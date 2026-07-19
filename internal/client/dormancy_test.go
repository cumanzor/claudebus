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

	if got := id.check(); got != stillListener {
		t.Fatalf("a freshly armed follower must be the listener, got cause %d", got)
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

		{"alias renamed (witness cleared, pid kept)", func() {
			setMetaFields(t, meta, fmt.Sprintf(`"listenerPid":%d,"listenerStart":""`, os.Getpid()))
		}, causeRenamed, "alias was renamed"},

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
			if got != c.want {
				t.Errorf("cause = %d, want %d", got, c.want)
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
// listenerStart is judged on the TRANSITION argv branch, and a follower THIS binary
// armed has no inbox in its argv, so it reads dead the instant it starts. The session
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
