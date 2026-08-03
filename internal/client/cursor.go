package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// The durable replay cursor (D4). It records how far a peer's inbox has actually been
// READ, replacing the listenerPid tri-state as the local replay decision. The tri-state
// asked "was this peer ever armed" and could only answer byte-0-or-END; a re-arm that
// seeks END silently discards everything that arrived while nobody held the file, which
// is cbus-8no (the rename window) and the --force-into-dead-gap hole wearing two hats.
// Resuming from a cursor fixes both without either being named in the code.
//
// It is a SIDECAR, not a meta.json field, for one decisive reason: meta.json is
// read-modify-written as a whole struct by armMeta and renameMeta, so a follower
// updating a cursor field there would race a lost update against every other field —
// including the identity tuple the follower itself depends on. The sidecar has exactly
// one writer. It also keeps the cursor clear of the undeclared-key drop hazard
// (cbus-5is) and off a file that other code rewrites on a different cadence.
//
// Local only. The wire, the relay and remote tail have no cursor; remote replay is the
// relay's business and is untouched by any of this.

const cursorFile = ".cursor"

// resumePoint is where a follower should begin reading.
//
// seekEnd resolves at OPEN, not at decision time, and the honest cost of that is a
// SKIP: anything appended between resolveResume deciding and openFollow seeking lands
// before the new position and is never delivered. Recording size-at-decision instead
// would deliver it. The window is microseconds, it is reachable ONLY through the
// migration rule, and it therefore happens at most once per peer, ever — after which a
// cursor exists and this branch is unreachable. It also reproduces v0.4.0's behavior
// exactly, which is the point of the migration rule.
type resumePoint struct {
	seekEnd bool
	offset  int64
}

func cursorPath(peerDir string) string { return filepath.Join(peerDir, cursorFile) }

// cursorState distinguishes the two ways a cursor can fail to give an answer. They are
// NOT the same state and must not resolve the same way: ABSENT means this peer has
// never been read by a cursor-aware binary, which is the migration case; CORRUPT means
// it has, and the record is damaged, so we genuinely cannot know where it got to.
// Collapsing them sends a damaged cursor down the migration path and seeks END, which
// is the one outcome the table forbids for an unknown position.
type cursorState int

const (
	cursorAbsent cursorState = iota
	cursorCorrupt
	cursorValid
)

// readCursor reads the recorded dev, inode and offset.
func readCursor(peerDir string) (dev, ino uint64, off int64, state cursorState) {
	b, err := os.ReadFile(cursorPath(peerDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, 0, cursorAbsent
		}
		// Present but unreadable (EACCES, EIO, a directory in its place). This is NOT
		// absent: absent means "no cursor-aware binary has read this peer" and sends an
		// ever-armed peer down the migration path to seek END, which would silently skip
		// everything queued while it was away. A cursor we cannot read is a position we
		// do not know, which is the corrupt case: replay and pay duplicates.
		return 0, 0, 0, cursorCorrupt
	}
	f := strings.Fields(strings.TrimSpace(string(b)))
	if len(f) != 3 {
		return 0, 0, 0, cursorCorrupt
	}
	dev, derr := strconv.ParseUint(f[0], 10, 64)
	ino, ierr := strconv.ParseUint(f[1], 10, 64)
	off, oerr := strconv.ParseInt(f[2], 10, 64)
	if derr != nil || ierr != nil || oerr != nil || off < 0 {
		return 0, 0, 0, cursorCorrupt
	}
	return dev, ino, off, cursorValid
}

// writeCursor records the position atomically (temp+rename, the writeMeta idiom) so a
// concurrent reader sees old-or-new and never a torn line. Best-effort by design: a
// failed write costs duplicates on the next arm, which is the trade this whole
// mechanism makes, so it must never break the follower.
//
// No fsync. A crash between the last emit and this write re-delivers a few frames; the
// alternative is an fsync per drain batch to buy a guarantee we have already said we do
// not need.
// writeCursor returns its failure instead of swallowing it. The error names WHICH half
// failed and carries the errno, which is what the caller needs to decide whether the
// recorded position actually moved. cbus-que.13: the loop write used to advance its
// lastSaved regardless, so ONE denied rename froze the cursor for the life of the arm and
// the next arm resumed from a stale offset, re-delivering everything since.
func writeCursor(peerDir string, dev, ino uint64, off int64) error {
	tmp := filepath.Join(peerDir, ".cursor.tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d %d %d\n", dev, ino, off)), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, cursorPath(peerDir)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// resolveResume is the replay decision table. It must run BEFORE armMeta overwrites
// listenerPid, because the migration rule reads the PREVIOUS value.
//
//	cursor valid (inode matches, offset within the file) -> resume at the offset.
//	  This one row covers re-arm, re-arm after a dead gap, post-rename and post-steal.
//	  None of them is a special case: the gap leaves no trace, and resuming is simply
//	  correct for all four. An if-branch naming any of them would mean this is wrong.
//	cursor dev+ino mismatch -> byte 0. The inbox was recreated (a rejoin's rm+recreate),
//	  so the old offset points into a file that no longer exists; join truncated the new
//	  one, so a full replay loses nothing.
//	cursor offset past EOF -> byte 0. Truncate-in-place; the same reasoning.
//	cursor CORRUPT -> byte 0, explicitly NOT the migration rule. A damaged record means
//	  the position is unknown, and END would silently discard whatever it could not
//	  account for.
//	no cursor, peer EVER armed -> seek END. The migration rule: every peer alive at
//	  upgrade is in exactly this state, and byte 0 would replay each peer's entire inbox
//	  history into its window on first arm. Reproduces v0.4.0 semantics once, for the
//	  only case where we genuinely have no better information, then self-heals.
//	no cursor, never armed -> byte 0. A first arm, unchanged from the tri-state.
func resolveResume(inbox, metaPath string) resumePoint {
	peerDir := filepath.Dir(inbox)
	switch dev, ino, off, state := readCursor(peerDir); state {
	case cursorValid:
		if curDev, curIno, size, ok := fileIdentity(inbox); ok {
			if curDev == dev && curIno == ino && off <= size {
				return resumePoint{offset: off}
			}
		}
		return resumePoint{offset: 0}
	case cursorCorrupt:
		return resumePoint{offset: 0} // cannot know: replay costs duplicates, END costs silence
	}
	if m, ok := ReadPeerMeta(metaPath); ok && m.ListenerPid != 0 {
		return resumePoint{seekEnd: true} // migration: cursor-less but ever-armed
	}
	return resumePoint{offset: 0}
}

// warnCursorWriteOnce reports the FIRST cursor-write failure per peer and stays quiet
// after, because the retry is unbounded and a message per tick would bury the signal in
// its own noise. One line is what separates a permanent condition from a process that
// merely looks busy — the silent-forever class this port keeps finding.
var cursorWarned sync.Map

func warnCursorWriteOnce(peerDir string, err error) {
	if _, seen := cursorWarned.LoadOrStore(peerDir, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr, "cbus: cursor write failed for %s, retrying every poll: %v\n", peerDir, err)
}
