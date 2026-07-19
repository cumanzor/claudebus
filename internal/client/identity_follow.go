package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Follower self-identity (cbus-0r8, D5). A follower holds proof of WHICH listener it
// is and keeps checking that meta still says so. One predicate closes four things:
//
//   - --steal: the stealer overwrites the tuple, the loser stops matching.
//   - foreign reopen: a stranger's join writes listenerPid null, so an orphaned
//     follower stops instead of shadow-replaying that stranger's inbox to the old
//     Monitor. This is the confidentiality case; it is why the polarity below is what
//     it is.
//   - two arms racing the gate: the loser is not in meta and self-terminates, so the
//     race degrades to one interval of duplicates rather than two permanent listeners.
//   - post-rename stale tail: the peer DIRECTORY moves, so the old path stops resolving
//     entirely and the follower stops. (An earlier version of this comment claimed the
//     cleared listenerStart is what the old follower notices. It is not: that cleared
//     witness lands at the NEW path, which this follower never reads. The rename is
//     detected by findRenamed below, not by the tuple.)
//
// The identity needs no new state: (listenerPid, listenerStart) already exists, and
// only one process can hold a given (pid, starttime). The witness IS the generation
// number.

// dormancyCause is why a follower stopped, or stillListener if it has not. The causes
// are distinguished so the marker line can be TRUE in each: a tail that stopped
// because its alias was renamed must not claim it was displaced.
type dormancyCause int

const (
	stillListener dormancyCause = iota
	causeDisplaced
	causeRejoined
	causeRenamed
	causeGone
)

// dormancy is why a follower stopped, plus whatever the marker needs to name a remedy.
// addr is the peer's new address, set only for causeRenamed.
type dormancy struct {
	cause dormancyCause
	addr  string
}

// listenerIdentity is the proof a follower carries. metaPath is captured at arm; note
// it is a PATH, so a rename moves the peer out from under it and the follower reads the
// new occupant (or nothing) — which is precisely the staleness we want it to notice.
type listenerIdentity struct {
	pid      int
	start    string
	metaPath string
}

// check asks whether meta still records THIS process as the listener.
//
// POLARITY (R14, frozen): anything we cannot confirm reads NOT-MINE. That deliberately
// inverts R1's file-read leniency, and the two are not in tension because they protect
// against opposite failures. R1 protects a peer from being REAPED on a bad read, which
// is destructive and irreversible. Here the destructive direction is the other one: a
// false dormant costs a quiet window, a visible marker and a re-arm, while a false
// continue streams another session's traffic into someone else's terminal. One is an
// inconvenience, the other is a leak.
func (id *listenerIdentity) check() dormancy {
	m, ok := ReadPeerMeta(id.metaPath)
	if !ok {
		// Our registration is no longer where we left it, and two very different
		// realities look identical from this path: the peer was pruned or unregistered
		// (gone), or it was RENAMED and the whole directory moved. The remedies are
		// opposite — re-join resurrects a vacated alias, which is wrong after a rename —
		// so it is worth one directory read to tell them apart.
		if addr, renamed := id.findRenamed(); renamed {
			return dormancy{cause: causeRenamed, addr: addr}
		}
		return dormancy{cause: causeGone}
	}
	if m.ListenerPid == 0 {
		return dormancy{cause: causeRejoined}
	}
	if m.ListenerPid != id.pid {
		return dormancy{cause: causeDisplaced}
	}
	if m.ListenerStart != id.start {
		// same pid, a DIFFERENT witness: impossible while we are alive, so the meta is
		// describing someone else. Treat as displacement rather than inventing a case.
		return dormancy{cause: causeDisplaced}
	}
	return dormancy{cause: stillListener}
}

// findRenamed looks for this listener's peer under a different alias in the same
// channel, and returns its new address.
//
// The signature is exact: renameMeta moves the directory, keeps listenerPid, and clears
// listenerStart — so a sibling recording OUR pid with an empty witness is our peer,
// renamed. A pruned peer leaves no such sibling and correctly stays causeGone.
//
// The channel-wide read is affordable because of WHERE it sits: check() only reaches it
// once the meta at our own path has already failed to resolve, and dormancy is a one-way
// door, so this runs at most once in a follower's entire life — never on the poll path.
func (id *listenerIdentity) findRenamed() (string, bool) {
	peerDir := filepath.Dir(id.metaPath)
	chDir := filepath.Dir(peerDir)
	entries, err := os.ReadDir(chDir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") { // D2 dot-prefix skip
			continue
		}
		sibling := filepath.Join(chDir, e.Name())
		if sibling == peerDir {
			continue
		}
		if m, ok := ReadPeerMeta(filepath.Join(sibling, "meta.json")); ok &&
			m.ListenerPid == id.pid && m.ListenerStart == "" {
			return filepath.Base(chDir) + "/" + e.Name(), true
		}
	}
	return "", false
}

// marker is the single line a dormant follower emits before exiting. It is visibly not
// a message frame (no from/to) so it cannot be mistaken for peer traffic, and it never
// crosses the wire — this is local stdout to the Monitor.
//
// Silence would be the wrong answer: a Monitor reporting a bare exit gives the session
// nothing to act on. But the REMEDY has to be the one that state actually accepts, and
// it differs per cause — every line used to say "re-arm to resume" and three of the
// four were wrong. A pruned peer's re-arm fails "no such peer — join first"; a
// displaced peer's plain re-arm is refused by the displacement gate; a renamed peer
// only answers to its new alias. A confidently wrong instruction is worse than none,
// so each cause names what will actually work.
//
// TestMarkerRemedyMatchesBehavior pins this against the code rather than against my
// memory of it: for each cause it asserts the named remedy is the one that state
// accepts, so the text cannot drift away from the behavior again.
func (d dormancy) marker() string {
	var line string
	switch d.cause {
	case causeDisplaced:
		line = "displaced by another listener — it holds the tail now; re-arm with --steal to take it back"
	case causeRejoined:
		line = "peer re-joined; this tail is stale — re-arm to resume"
	case causeRenamed:
		line = "alias was renamed to " + d.addr + "; this tail is stale — re-arm as " + d.addr
	case causeGone:
		line = "peer registration is gone — re-join, then re-arm"
	default:
		return ""
	}
	return fmt.Sprintf("◀ cbus tail ended: %s\n", line)
}

// identityCause is check() with the nil-identity test seam. Production always supplies
// an identity (ArmLocalTail refuses to arm without one, P4); nil is how follow() tests
// exercise the streaming loop without a meta on disk.
func identityCause(id *listenerIdentity) dormancy {
	if id == nil {
		return dormancy{cause: stillListener}
	}
	return id.check()
}
