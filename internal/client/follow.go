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

// ReplayMode is the first-arm vs re-arm distinction, keyed off the TRI-STATE of the
// meta's recorded listenerPid (absent / null / int): a never-armed peer (absent OR
// null) replays the whole inbox from byte 0 — join truncated it, so nothing is lost;
// any re-arm (an int was recorded, even a stale/dead pid) seeks EOF and replays
// nothing. Mirrors bin/cbus:514.
type ReplayMode int

const (
	ReplayFromStart ReplayMode = iota // never-armed listenerPid -> from byte 0 (bash from_line "+1")
	ReplaySeekEnd                     // a listenerPid was recorded -> seek EOF (bash from_line "0")
)

// InboxPath is a peer's inbox under the live CBUS_DIR.
//
// The bash-era raw concatenation (no filepath.Clean) is gone with the argv it fed:
// nothing greps this string to judge a peer any more. The one surviving grep is
// metaInboxNeedle in liveness_transition.go, which reads PRE-P3 metas and rebuilds the
// raw spelling itself, independent of this function.
func InboxPath(ch, al string) string {
	return filepath.Join(CBUSDir(), ch, al, "inbox.jsonl")
}

// ArmLocalTail resolves target, records THIS process as the peer's listener, and runs
// the blocking follower IN THIS PROCESS. It returns only on failure.
//
// The Decision 2 re-exec is gone. It existed so the follower's argv would carry the
// inbox path for a grep-based liveness predicate; identity is structural now, so there
// is nothing to put in an argv and no reason to replace the process image. The pid
// recorded just below is still the follower's pid, for the simpler reason that it
// never stopped being this process.
func ArmLocalTail(target string) error {
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
	// tri-state replay decision, read BEFORE we overwrite listenerPid.
	mode := ReplayFromStart
	if pm, ok := ReadPeerMeta(metaPath); ok && pm.ListenerPid != 0 {
		mode = ReplaySeekEnd
	}
	armMeta(metaPath) // best-effort: listenerPid=own pid, listenerStart, ownerPid, lastActivity
	RunFollower(inbox, mode)
	return nil // unreachable: the follower never self-exits (see RunFollower)
}

// armMeta records this process as the peer's listener: listenerPid=own pid,
// ownerPid=owning session pid (null if none), and refreshes lastActivity (D3 — the
// same grace-clock refresh join does). Best-effort and field-preserving: a missing or
// torn meta is left untouched (bash `jset || true` no-ops when meta.json is absent),
// and every other field round-trips verbatim (raw pids, byte-for-byte).
func armMeta(metaPath string) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return
	}
	var m peerMeta
	if json.Unmarshal(b, &m) != nil {
		return
	}
	m.ListenerPid = json.RawMessage(strconv.Itoa(os.Getpid()))
	// structural identity witness (P3). Best-effort like the rest of armMeta: if the
	// probe fails we record no witness rather than a wrong one, and this peer is judged
	// on the TRANSITION argv branch — which, for a follower this binary armed, has no
	// inbox in its argv and so reads dead. Failing closed is correct; a listener we
	// cannot identify must not be trusted as alive.
	if start, err := procStartTime(os.Getpid()); err == nil {
		m.ListenerStart = start
	}
	if owner, ok := OwnerPID(); ok {
		m.OwnerPid = json.RawMessage(strconv.Itoa(owner))
	} else {
		m.OwnerPid = jsonNull
	}
	m.LastActivity = Now()
	_ = writeMeta(filepath.Dir(metaPath), m)
}

// RunFollower is the blocking local tail: it streams framed inbox events to stdout,
// one write per frame, FOREVER. ArmLocalTail calls it directly, in this process; the
// Monitor tool stops it by killing the process — the follower NEVER self-exits (a
// vanished inbox is polled until it returns; a rotation is followed like tail -F).
func RunFollower(inbox string, mode ReplayMode) {
	follow(inbox, mode, os.Stdout, followPoll, nil)
}

// follow is the follower loop. It is the counterpart of the embedded python follower
// (bin/cbus:552-577): open + optional seek-to-EOF, then readline/pend/emit with a
// poll+rotation tail. Framing is core.LocalEmit — one write+flush per frame (the
// Monitor batches the frame's lines into one notification). out/poll/stop are seams:
// production passes os.Stdout, followPoll, and a nil stop (never stops); tests inject
// a buffer, a fast poll, and a stop channel.
func follow(inbox string, mode ReplayMode, out io.Writer, poll time.Duration, stop <-chan struct{}) {
	f, ok := openFollow(inbox, mode, poll, stop)
	if !ok {
		return // only reachable via stop (test); production openFollow retries forever
	}
	defer func() { f.Close() }()
	dev, ino := devInoOf(f)
	consumed := offsetOf(f)
	r := bufio.NewReader(f)
	pend := ""
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
		time.Sleep(poll)
		st, err := os.Stat(inbox)
		if err != nil {
			continue // inbox vanished — keep the old fd and keep polling; never self-exit
		}
		if rotated(dev, ino, consumed, st) {
			f.Close()
			nf, ok := reopenUntilSuccess(inbox, poll, stop)
			if !ok {
				return
			}
			// reopen reads from byte 0 (survives a rejoin's rm+recreate or truncate).
			f = nf
			dev, ino = devInoOf(f)
			consumed = 0
			r = bufio.NewReader(f)
			pend = ""
		}
	}
}

// openFollow opens the inbox for the initial read and applies the replay mode: a
// re-arm seeks EOF (no replay), a first arm stays at byte 0 (replay the whole inbox).
// It retries until the open succeeds — the arm just verified the inbox exists, so a
// vanish race between arm and follower must not kill the follower.
func openFollow(inbox string, mode ReplayMode, poll time.Duration, stop <-chan struct{}) (*os.File, bool) {
	f, ok := reopenUntilSuccess(inbox, poll, stop)
	if !ok {
		return nil, false
	}
	if mode == ReplaySeekEnd {
		_, _ = f.Seek(0, io.SeekEnd)
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
