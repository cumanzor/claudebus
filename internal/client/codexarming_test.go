package client

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The v0.8.1 codex patch tranche: the join-time listener claim (FIX 1) and the teardown
// cause-ordering (FIX 2). These pin the two behaviours against the reviewer's gate items.

// mustJoinPeer joins ch/al in a fresh CBUS_DIR and returns (root, metaPath, inbox).
func mustJoinPeer(t *testing.T, ch, al string) (root, metaPath, inbox string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-claim")
	if _, _, err := Join(ch, al); err != nil {
		t.Fatal(err)
	}
	peerDir := filepath.Join(root, ch, al)
	return root, filepath.Join(peerDir, "meta.json"), filepath.Join(peerDir, "inbox.jsonl")
}

// TestClaimListenerAtJoinShowsListenImmediately pins the display fix: a bare join reads
// `off pid=?` (listenerPid null), and the join-time claim flips it to a live listener at
// once, before any bridge arm — so a sweep or an operator no longer misreads the arming
// window as dead.
func TestClaimListenerAtJoinShowsListenImmediately(t *testing.T) {
	_, meta, _ := mustJoinPeer(t, "cx", "peer")

	if MetaListenerAlive(meta) {
		t.Fatal("precondition: a bare join must read as NOT listening (off pid=?)")
	}
	claimListenerAtJoin("cx", "peer")
	if !MetaListenerAlive(meta) {
		t.Fatal("after the join-time claim the peer must read as a live listener")
	}
	// and `cbus list` (ScanStore) must show it listening
	var found bool
	for _, ch := range ScanStore().Channels {
		for _, p := range ch.Peers {
			if ch.Name == "cx" && p.Alias == "peer" {
				found = true
				if !p.Listening {
					t.Error("list must show the claimed peer as listening")
				}
				if p.ListenerPid != os.Getpid() {
					t.Errorf("list listenerPid = %d, want the wrapper pid %d", p.ListenerPid, os.Getpid())
				}
			}
		}
	}
	if !found {
		t.Fatal("claimed peer not found in ScanStore")
	}
}

// TestClaimListenerAtJoinRecordsRealSelfPid pins gate (c)/(d): the claim records THIS
// process's real pid and start witness — never a synthesised or foreign token — so it is
// never a phantom. When the wrapper later exits, that pid is dead and liveness reads the
// claim dead on its own terms (the dead-pid arm of MetaListenerAlive), the same path a
// claim-then-attach-failure lands on.
func TestClaimListenerAtJoinRecordsRealSelfPid(t *testing.T) {
	_, meta, _ := mustJoinPeer(t, "cx", "peer")
	claimListenerAtJoin("cx", "peer")

	m, ok := ReadPeerMeta(meta)
	if !ok {
		t.Fatal("meta unreadable after claim")
	}
	if m.ListenerPid != os.Getpid() {
		t.Errorf("claim listenerPid = %d, want the real self pid %d (no phantom)", m.ListenerPid, os.Getpid())
	}
	if m.ListenerStart != selfStart(t) {
		t.Errorf("claim listenerStart = %q, want the real self start token %q", m.ListenerStart, selfStart(t))
	}
	// the dead-pid case liveness handles: rewrite the claim's pid to a dead one and it must
	// read dead, no phantom survivor.
	setMetaFields(t, meta, fmt.Sprintf(`"listenerPid":424242,"listenerStart":%q,"ownerPid":null`, m.ListenerStart))
	if MetaListenerAlive(meta) {
		t.Error("a claim whose pid is dead must read dead (liveness handles the post-exit claim)")
	}
}

// TestClaimKeepsFirstArmReplay is THE load-bearing invariant (gate a, ruling 2): the claim
// sets listenerPid, which alone would send the real bridge arm down resolveResume's
// cursor-absent + ever-armed MIGRATION path and seek EOF, silently dropping every message
// queued in the arming window. The claim also seeds the cursor at 0, so the arm resolves to
// offset 0 (full replay) instead. The counter-pin (ruling 1) proves the Claude migration is
// untouched: the same listenerPid WITHOUT the seeded cursor still seeks END.
func TestClaimKeepsFirstArmReplay(t *testing.T) {
	_, meta, inbox := mustJoinPeer(t, "cx", "peer")
	peerDir := filepath.Dir(meta)

	// after the full claim (armMeta + cursor seed), the arm replays from byte 0
	claimListenerAtJoin("cx", "peer")
	if got := resolveResume(inbox, meta); got != (resumePoint{offset: 0}) {
		t.Fatalf("after the claim resolveResume = %+v, want offset 0 (full replay, NOT seek END)", got)
	}

	// counter-pin: listenerPid set but NO cursor (delete the seeded sidecar) is the Claude
	// migration case, and it STILL seeks END — the shared decision is untouched for the
	// legacy path.
	if err := os.Remove(filepath.Join(peerDir, cursorFile)); err != nil {
		t.Fatal(err)
	}
	if got := resolveResume(inbox, meta); !got.seekEnd {
		t.Fatalf("cursor-absent + listenerPid-set must still seek END (migration untouched), got %+v", got)
	}
}

// TestClaimSeedFailureSkipsClaim pins F1: when the cursor seed does not round-trip, the claim
// is SKIPPED — listenerPid is never committed — so the listenerPid-set + cursor-absent
// seek-END mail-loss state is unreachable by construction. A directory in the sidecar's place
// forces writeCursor's rename to fail and readCursor to read invalid, standing in for the
// ENOSPC/EIO/rename-race the ordering guards against. The discriminating assertion is that no
// claim lands (the pre-inversion arm-then-seed order would have committed listenerPid here).
func TestClaimSeedFailureSkipsClaim(t *testing.T) {
	_, meta, inbox := mustJoinPeer(t, "cx", "peer")
	peerDir := filepath.Dir(meta)
	if err := os.Mkdir(filepath.Join(peerDir, cursorFile), 0o755); err != nil {
		t.Fatal(err)
	}
	claimListenerAtJoin("cx", "peer")

	if MetaListenerAlive(meta) {
		t.Fatal("a seed that did not round-trip must NOT commit listenerPid (claim skipped)")
	}
	if m, ok := ReadPeerMeta(meta); !ok || m.ListenerPid != 0 {
		t.Fatalf("listenerPid must stay null after a failed seed, got %+v", m)
	}
	if got := resolveResume(inbox, meta); got.seekEnd {
		t.Fatalf("a skipped claim must never leave the arm on the seek-END path, got %+v", got)
	}
}

// TestReapWithinBoundsTheWait pins F2: the post-kill reap is bounded. A never-closing reap
// times out (returns false) instead of blocking forever, so a D-state app-server cannot
// suppress the teardown cause; an already-reaped process reports true at once.
func TestReapWithinBoundsTheWait(t *testing.T) {
	if reapWithin(make(chan struct{}), 10*time.Millisecond) {
		t.Fatal("a never-closing reap must time out, not report reaped")
	}
	done := make(chan struct{})
	close(done)
	if !reapWithin(done, time.Second) {
		t.Fatal("an already-closed reap must report reaped at once")
	}
}

// TestClaimDeliversPreArmMessage is the behavioural gate (a): a message that arrives between
// the join-time claim and the real arm is delivered when the follower finally arms, not
// silently skipped. It drives the follower with the resume point the claim leaves behind.
func TestClaimDeliversPreArmMessage(t *testing.T) {
	_, meta, inbox := mustJoinPeer(t, "cx", "peer")

	claimListenerAtJoin("cx", "peer")
	// a peer message lands in the durable inbox during the arming window
	appendLine(t, inbox, "cx/other", "cx/peer", "queued-in-the-window")

	// the bridge finally arms: resolve resume exactly as armLocalTailTo would, then follow
	resume := resolveResume(inbox, meta)
	buf, _, stopJoin := startFollow(t, inbox, resume)
	defer stopJoin()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "queued-in-the-window") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pre-arm message was not delivered after arm; got %q", buf.String())
}

// TestDisplacementGateExemptsExactSelf pins gate (b): after the wrapper claims listenership
// with its own pid+start, the bridge's arm (same process) must NOT be refused by the
// displacement gate as a second listener. A refusal returns instantly with a --steal error;
// the exemption lets the arm pass and block in the follower, so no --steal error appears.
func TestDisplacementGateExemptsExactSelf(t *testing.T) {
	mustJoinPeer(t, "cx", "peer")
	claimListenerAtJoin("cx", "peer") // the wrapper's early claim: this process's pid+start

	armed := make(chan error, 1)
	go func() { armed <- ArmLocalTail("cx/peer", false) }()
	select {
	case err := <-armed:
		// only a gate REFUSAL returns quickly, and it names --steal; anything else means the
		// arm passed the gate (the exemption held) and then ended for another reason.
		if err != nil && strings.Contains(err.Error(), "--steal") {
			t.Fatalf("the gate refused the same process re-arming its own claim: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		// still following: the exemption held, the arm was not refused. Success.
	}
}

// ---- FIX 2: teardown cause ordering -------------------------------------------------

// orderBuf records writes with a marker so a test can assert the app-server flush precedes
// the bridge cause line. killServer() writes the marker; teardownOutcome writes the cause to
// the same buffer, so their order in the buffer IS their order on the terminal.
func TestTeardownOutcomeBridgeCausePrintsLast(t *testing.T) {
	buf := &bytes.Buffer{}
	kills := 0
	killServer := func() { kills++; buf.WriteString("APPSERVER-DOWN\n") }

	bridgeExit := make(chan error, 1)
	bridgeExit <- errors.New("listener swept")

	err := teardownOutcome(errors.New("tui killed"), bridgeExit, killServer, buf, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "codex-bridge failed") {
		t.Fatalf("want a codex-bridge failure error, got %v", err)
	}
	out := buf.String()
	di := strings.Index(out, "APPSERVER-DOWN")
	ci := strings.Index(out, "codex-bridge failed")
	if di < 0 || ci < 0 || di > ci {
		t.Fatalf("app-server flush must precede the cause line (cause LAST); got %q", out)
	}
	if kills != 1 {
		t.Errorf("killServer called %d times, want exactly 1", kills)
	}
}

// TestTeardownOutcomeCleanQuitSurfacesNothing: a clean TUI exit (human quit) with no bridge
// cause returns nil and prints no cause line — but still flushes the app-server.
func TestTeardownOutcomeCleanQuitSurfacesNothing(t *testing.T) {
	buf := &bytes.Buffer{}
	kills := 0
	killServer := func() { kills++; buf.WriteString("APPSERVER-DOWN\n") }

	err := teardownOutcome(nil, make(chan error, 1), killServer, buf, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("clean quit must return nil, got %v", err)
	}
	if strings.Contains(buf.String(), "codex-bridge") {
		t.Errorf("clean quit must print no bridge cause, got %q", buf.String())
	}
	if kills != 1 {
		t.Errorf("killServer called %d times, want exactly 1 (flush always runs)", kills)
	}
}

// TestTeardownOutcomeAbnormalWaitsForCause: the TUI self-exits abnormally (the app-server
// reset that killed it will down the bridge a beat later); the teardown waits the bounded
// grace for the bridge cause, then prints it LAST, after the flush.
func TestTeardownOutcomeAbnormalWaitsForCause(t *testing.T) {
	buf := &bytes.Buffer{}
	killServer := func() { buf.WriteString("APPSERVER-DOWN\n") }

	bridgeExit := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond) // bridge reports a beat after the TUI died
		bridgeExit <- errors.New("conn reset")
	}()

	err := teardownOutcome(errors.New("tui reset"), bridgeExit, killServer, buf, time.Second)
	if err == nil || !strings.Contains(err.Error(), "codex-bridge failed") {
		t.Fatalf("want the bridge cause surfaced after the grace wait, got %v", err)
	}
	out := buf.String()
	if di, ci := strings.Index(out, "APPSERVER-DOWN"), strings.Index(out, "codex-bridge failed"); di < 0 || ci < 0 || di > ci {
		t.Fatalf("cause must print last, after the flush; got %q", out)
	}
}

// TestTeardownOutcomeGraceNeverSuppressesFlush: when the grace expires with no bridge cause
// (a genuinely healthy bridge behind an abnormal TUI exit), the teardown still flushes the
// app-server and returns the TUI's own error — the grace bounds the WAIT, it never suppresses
// the flush.
func TestTeardownOutcomeGraceNeverSuppressesFlush(t *testing.T) {
	buf := &bytes.Buffer{}
	kills := 0
	killServer := func() { kills++; buf.WriteString("APPSERVER-DOWN\n") }

	werr := errors.New("tui reset")
	got := teardownOutcome(werr, make(chan error, 1), killServer, buf, 30*time.Millisecond)
	if got != werr {
		t.Fatalf("grace expiry must return the TUI error, got %v", got)
	}
	if kills != 1 {
		t.Errorf("killServer called %d times, want exactly 1 even on grace expiry", kills)
	}
}
