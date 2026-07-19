package client

import "fmt"

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
//   - post-rename stale tail: renameMeta cleared listenerStart, so the tuple stops
//     matching and "the old tail is stale, re-arm" is enforced rather than documented.
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
func (id *listenerIdentity) check() dormancyCause {
	m, ok := ReadPeerMeta(id.metaPath)
	if !ok {
		return causeGone
	}
	if m.ListenerPid == 0 {
		return causeRejoined
	}
	if m.ListenerPid != id.pid {
		return causeDisplaced
	}
	if m.ListenerStart != id.start {
		if m.ListenerStart == "" {
			return causeRenamed // renameMeta clears the witness and keeps the pid
		}
		// same pid, a DIFFERENT witness: impossible while we are alive, so the meta is
		// describing someone else. Treat as displacement rather than inventing a case.
		return causeDisplaced
	}
	return stillListener
}

// marker is the single line a dormant follower emits before exiting. It is visibly not
// a message frame (no from/to) so it cannot be mistaken for peer traffic, and it never
// crosses the wire — this is local stdout to the Monitor.
//
// Silence would be the wrong answer: doctrine already trains sessions to re-arm when a
// tail drops, and a Monitor reporting a bare exit gives the session nothing to act on.
func (c dormancyCause) marker() string {
	var why string
	switch c {
	case causeDisplaced:
		why = "displaced by another listener"
	case causeRejoined:
		why = "peer re-joined; this tail is stale"
	case causeRenamed:
		why = "alias was renamed; this tail is stale"
	case causeGone:
		why = "peer registration is gone"
	default:
		return ""
	}
	return fmt.Sprintf("◀ cbus tail ended: %s — re-arm to resume\n", why)
}

// identityCause is check() with the nil-identity test seam. Production always supplies
// an identity (ArmLocalTail refuses to arm without one, P4); nil is how follow() tests
// exercise the streaming loop without a meta on disk.
func identityCause(id *listenerIdentity) dormancyCause {
	if id == nil {
		return stillListener
	}
	return id.check()
}
