package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"claudebus/internal/core"
)

// followPoll is the follower's idle poll interval — how long it sleeps on a quiet
// inbox before re-stat'ing for appends or rotation (bin/cbus:564, `time.sleep(0.2)`).
const followPoll = 200 * time.Millisecond

// InboxPath is a peer's inbox under the live CBUS_DIR.
//
// The bash-era raw concatenation (no filepath.Clean) is gone with the argv it fed, and
// so is the last reader of that spelling: nothing greps this string to judge a peer.
func InboxPath(ch, al string) string {
	return filepath.Join(CBUSDir(), ch, al, "inbox.jsonl")
}

// ArmLocalTail resolves target, records THIS process as the peer's listener, and runs
// the blocking follower IN THIS PROCESS.
//
// It returns on failure OR on dormancy. The second case is new: a follower that stops
// being the recorded listener emits its marker and returns normally, so a nil return
// now means "the follower ended deliberately", not "unreachable".
//
// The Decision 2 re-exec is gone. It existed so the follower's argv would carry the
// inbox path for a grep-based liveness predicate; identity is structural now, so there
// is nothing to put in an argv and no reason to replace the process image. The pid
// recorded just below is still the follower's pid, for the simpler reason that it
// never stopped being this process.
func ArmLocalTail(target string, steal bool) error {
	ch, al, err := ParseLocal(target)
	if err != nil {
		return err
	}
	if ch == "" {
		rc, ok := FindPeerChannel(al)
		if !ok {
			return fmt.Errorf("use <channel>/<alias>")
		}
		ch = rc
	}
	inbox := InboxPath(ch, al)
	if !fileExists(inbox) {
		return fmt.Errorf("no such peer %q — join first", ch+"/"+al)
	}
	metaPath := filepath.Join(CBUSDir(), ch, al, "meta.json")
	// THE DISPLACEMENT GATE (D5). A second local listener on an already-armed alias is
	// refused by default, relay-style. The rule is uniform on purpose: it does not
	// exempt the same session, because a session arming over its own live tail is the
	// double-listener bug rather than a convenience — every message would be delivered
	// twice and meta would pin to the newest pid only.
	//
	// The gate is NOT atomic and deliberately takes no lock (R-B): two arms can both
	// pass it before either writes meta. That race self-corrects, because the loser's
	// identity check finds it is not the recorded listener and it goes dormant within
	// one interval. A lock would buy atomicity at the price of a wedged-alias recovery
	// path, which is the worse failure.
	if !steal {
		if m, ok := ReadPeerMeta(metaPath); ok && MetaListenerAlive(metaPath) {
			return fmt.Errorf("%s is already being tailed (listener pid %d) — use --steal to take over",
				ch+"/"+al, m.ListenerPid)
		}
	}
	// P4: establish the witness BEFORE anything else. The witness is now the ONLY thing
	// that can prove which listener this is, so arming without one would produce a tail
	// that is instantly and invisibly not the listener — armed, streaming, and read dead
	// by every peer. Refuse loudly instead of arming into that trap.
	start, err := procStartTime(os.Getpid())
	if err != nil {
		return fmt.Errorf("cannot establish listener identity: %v — refusing to arm", err)
	}
	// the replay decision, resolved BEFORE armMeta overwrites listenerPid — the
	// migration rule reads the PREVIOUS value to tell an upgraded peer from a fresh one.
	resume := resolveResume(inbox, metaPath)
	armMeta(metaPath, start) // listenerPid=own pid, listenerStart, ownerPid, lastActivity
	RunFollower(inbox, resume, &listenerIdentity{pid: os.Getpid(), start: start, metaPath: metaPath})
	return nil // the follower ended: displaced, renamed, re-joined or unregistered
}

// armMeta records this process as the peer's listener: listenerPid=own pid,
// ownerPid=owning session pid (null if none), and refreshes lastActivity (D3 — the
// same grace-clock refresh join does). Best-effort and field-preserving: a missing or
// torn meta is left untouched (bash `jset || true` no-ops when meta.json is absent),
// and every other field round-trips verbatim (raw pids, byte-for-byte).
func armMeta(metaPath, start string) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return
	}
	var m peerMeta
	if json.Unmarshal(b, &m) != nil {
		return
	}
	m.ListenerPid = json.RawMessage(strconv.Itoa(os.Getpid()))
	m.ListenerStart = start // the caller established it; arming without one is refused
	if owner, ok := OwnerPID(); ok {
		m.OwnerPid = json.RawMessage(strconv.Itoa(owner))
	} else {
		m.OwnerPid = jsonNull
	}
	m.LastActivity = Now()
	_ = writeMeta(filepath.Dir(metaPath), m)
}

// RunFollower is the blocking local tail: it streams framed inbox events to stdout,
// one write per frame. ArmLocalTail calls it directly, in this process.
//
// It returns in exactly one case: this process stopped being the recorded listener, so
// the follower went dormant (see listenerIdentity). Everything else is still followed
// forever — a vanished inbox is polled until it returns, a rotation is followed like
// tail -F, and the Monitor stopping it means killing the process.
func RunFollower(inbox string, resume resumePoint, id *listenerIdentity) {
	follow(inbox, resume, id, os.Stdout, followPoll, nil)
}

// follow is the follower loop. It is the counterpart of the embedded python follower
// (bin/cbus:552-577): open + optional seek-to-EOF, then readline/pend/emit with a
// poll+rotation tail. Framing is core.LocalEmit — one write+flush per frame (the
// Monitor batches the frame's lines into one notification). out/poll/stop are seams:
// production passes os.Stdout, followPoll, and a nil stop (never stops); tests inject
// a buffer, a fast poll, and a stop channel.
func follow(inbox string, resume resumePoint, id *listenerIdentity, out io.Writer, poll time.Duration, stop <-chan struct{}) {
	f, ok := openFollow(inbox, resume, poll, stop)
	if !ok {
		return // only reachable via stop (test); production openFollow retries forever
	}
	defer func() { f.Close() }()
	peerDir := filepath.Dir(inbox)
	dev, ino := devInoOf(f)
	consumed := offsetOf(f)
	// record the starting position immediately, so a crash before the first message
	// cannot make the next arm re-apply the migration rule and seek END a second time.
	writeCursor(peerDir, dev, ino, consumed)
	lastSaved := consumed
	r := bufio.NewReader(f)
	pend := ""
	// identityEvery bounds how long a displaced follower keeps reading: the check is one
	// small meta read, so it runs on a slow multiple of the poll rather than every tick.
	// Displacement announces itself in no other way — a steal does not rotate the inbox —
	// so this cadence IS the takeover latency.
	const identityEvery = 5 // ~1s at a 200ms poll
	idleTicks := 0
	for {
		if stopped(stop) {
			return
		}
		chunk, _ := r.ReadString('\n')
		if len(chunk) > 0 {
			consumed += int64(len(chunk))
			pend += chunk
			if strings.HasSuffix(pend, "\n") {
				if frame := core.LocalEmit([]byte(pend)); frame != nil {
					_, _ = out.Write(frame) // one write per frame
				}
				pend = ""
			}
			continue // drain all available data before sleeping (bash: `if chunk: ... continue`)
		}
		// the inbox is quiet. Two things happen here, and the order matters.
		//
		// P3: a cursor write is identity-conditional. A displaced or orphaned follower
		// must stop MOVING the cursor, not merely stop reading — an orphan whose peer dir
		// was recreated writes through the PATH into the new epoch's sidecar and would
		// corrupt a live peer's resume point. Verifying immediately before the write
		// leaves a residual TOCTOU of microseconds (a steal landing between the check and
		// the rename); that window is accepted, because closing it needs the lock the
		// gate deliberately does not take, and its worst case is one stale cursor write
		// that the next arm's dev+ino check or the stealer's own write corrects.
		// The cursor records the last FRAME BOUNDARY, not the last byte read.
		//
		// consumed counts bytes pulled off the fd, which includes a partial line still
		// sitting in pend. Persisting that would point the next arm INTO the middle of a
		// message: once the writer completes the line, the resuming follower starts
		// mid-frame, the head is lost forever and the tail surfaces as a raw fragment.
		// That is silent loss, which is the one outcome this whole mechanism trades
		// duplicates to avoid. Subtracting pend leaves the cursor on the last '\n' we
		// actually emitted, so the worst case stays a re-delivery.
		//
		// It also makes the write condition honest: a batch that only grew pend has not
		// advanced the boundary, so there is nothing to persist.
		boundary := consumed - int64(len(pend))
		idleTicks++
		if boundary != lastSaved || idleTicks >= identityEvery {
			if d := identityCause(id); d.cause != stillListener {
				_, _ = out.Write([]byte(d.marker()))
				return // one-way door (R14): dormancy is never re-entered
			}
			if boundary != lastSaved {
				writeCursor(peerDir, dev, ino, boundary)
				lastSaved = boundary
			}
			idleTicks = 0
		}
		time.Sleep(poll)
		st, err := os.Stat(inbox)
		if err != nil {
			continue // inbox vanished — keep the old fd and keep polling; never self-exit
		}
		if rotated(dev, ino, consumed, st) {
			// a rotation is the foreign-reopen trigger: the inbox we are about to follow
			// may belong to a DIFFERENT peer that reclaimed this path. Check before
			// reopening, never after, so a stranger's bytes are never read at all.
			if d := identityCause(id); d.cause != stillListener {
				_, _ = out.Write([]byte(d.marker()))
				return
			}
			f.Close()
			nf, ok := reopenUntilSuccess(inbox, poll, stop)
			if !ok {
				return
			}
			// reopen reads from byte 0 (survives a rejoin's rm+recreate or truncate).
			f = nf
			dev, ino = devInoOf(f)
			consumed = 0
			// the cursor is keyed to the inode, so a rotation must republish it against
			// the NEW file; leaving the old pair would make the next arm read a stale
			// inode, fall to byte 0, and replay what we are about to stream anyway.
			writeCursor(peerDir, dev, ino, consumed)
			lastSaved = 0
			r = bufio.NewReader(f)
			pend = ""
		}
	}
}

// openFollow opens the inbox for the initial read and applies the replay mode: a
// re-arm seeks EOF (no replay), a first arm stays at byte 0 (replay the whole inbox).
// It retries until the open succeeds — the arm just verified the inbox exists, so a
// vanish race between arm and follower must not kill the follower.
func openFollow(inbox string, resume resumePoint, poll time.Duration, stop <-chan struct{}) (*os.File, bool) {
	f, ok := reopenUntilSuccess(inbox, poll, stop)
	if !ok {
		return nil, false
	}
	if resume.seekEnd {
		_, _ = f.Seek(0, io.SeekEnd)
	} else if resume.offset > 0 {
		_, _ = f.Seek(resume.offset, io.SeekStart)
	}
	return f, true
}

// reopenUntilSuccess opens path, retrying every poll until it succeeds — the ruled
// fix for the bash follower's one self-termination bug (a vanish between a successful
// stat and the reopen open() left the file closed and the next readline threw,
// killing the follower). Retrying keeps the follower alive across a rejoin's
// rm+recreate; a permanently-gone inbox simply polls forever (never self-exit). The
// stop seam lets a test bound the retry.
func reopenUntilSuccess(path string, poll time.Duration, stop <-chan struct{}) (*os.File, bool) {
	for {
		if f, err := os.Open(path); err == nil {
			return f, true
		}
		if stopped(stop) {
			return nil, false
		}
		time.Sleep(poll)
	}
}

// rotated reports whether the inbox at cur is a different file than the open fd
// (dev+ino changed — a rejoin's rm+recreate) OR has shrunk below what we have read
// (size < consumed — a truncate-in-place). Either triggers a reopen-from-0. Mirrors
// bin/cbus:569 (`st.st_ino != ino or st.st_size < f.tell()`), with dev added to the
// inode check (a ruled delta over the bash ino-only compare).
func rotated(prevDev, prevIno uint64, consumed int64, cur os.FileInfo) bool {
	dev, ino, ok := statDevIno(cur)
	if !ok {
		return false
	}
	if dev != prevDev || ino != prevIno {
		return true
	}
	return cur.Size() < consumed
}

// statDevIno pulls dev+ino out of a FileInfo (unix only — the whole follower is).
func statDevIno(fi os.FileInfo) (dev, ino uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), true
}

func devInoOf(f *os.File) (uint64, uint64) {
	fi, err := f.Stat()
	if err != nil {
		return 0, 0
	}
	d, i, _ := statDevIno(fi)
	return d, i
}

// offsetOf is the fd's current byte offset (the seek position after openFollow) — the
// logical read cursor `consumed` starts from, for the size-regression rotation check.
func offsetOf(f *os.File) int64 {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0
	}
	return off
}

func stopped(stop <-chan struct{}) bool {
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}
