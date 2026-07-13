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

// wire is the follower --from value, byte-identical to bash's from_line so the
// replay semantics ("0" => seek end; anything else => from start) match exactly.
func (m ReplayMode) wire() string {
	if m == ReplaySeekEnd {
		return "0"
	}
	return "+1"
}

func replayFromWire(s string) ReplayMode {
	if s == "0" {
		return ReplaySeekEnd
	}
	return ReplayFromStart
}

// Hidden follower flags for the re-exec'd argv. The --inbox VALUE is the Decision 2
// compat surface (bash-era meta_listener_alive greps `ps -o args=` for the inbox
// path); the flag name/position are free but kept stable.
const (
	tailFlagInbox = "--inbox"
	tailFlagFrom  = "--from"
)

// InboxPath is a peer's inbox file: $CBUS_DIR/<ch>/<al>/inbox.jsonl, byte-equal to
// bash inbox_path() (bin/cbus:27) for the same CBUS_DIR spelling. It does NOT resolve
// symlinks or absolutize (no EvalSymlinks/Abs): the exact string is the Decision 2
// compat surface — it goes verbatim into the follower's argv where bash greps it —
// and it must also equal the needle MetaListenerAlive builds (filepath.Join off the
// meta dir), so both Go and bash liveness read a Go follower as alive.
func InboxPath(ch, al string) string {
	return filepath.Join(CBUSDir(), ch, al, "inbox.jsonl")
}

// TailArgv is the re-exec'd follower's argv: `<self> tail --inbox <inbox> --from
// <+1|0>`. self is argv[0]; the inbox path appears verbatim so bash-era liveness
// recognizes this Go follower regardless of the binary name (cbus-go).
func TailArgv(self, inbox string, mode ReplayMode) []string {
	return []string{self, "tail", tailFlagInbox, inbox, tailFlagFrom, mode.wire()}
}

// ParseTailFollower extracts the follower's inbox + replay mode when a `tail` verb
// carries the hidden --inbox (i.e. this process IS the re-exec'd follower). ok=false
// means this is an arm invocation (bare `tail <ch>/<al>`), not the follower.
func ParseTailFollower(args []string) (inbox string, mode ReplayMode, ok bool) {
	from := "+1"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case tailFlagInbox:
			if i+1 < len(args) {
				inbox = args[i+1]
				i++
			}
		case tailFlagFrom:
			if i+1 < len(args) {
				from = args[i+1]
				i++
			}
		}
	}
	if inbox == "" {
		return "", ReplayFromStart, false
	}
	return inbox, replayFromWire(from), true
}

// ArmLocalTail resolves target, records THIS process as the peer's listener, and
// re-execs (image-replacing) into the blocking follower. It returns only on failure;
// on success syscall.Exec never returns.
//
// COMPAT (Decision 2 — dies with P3 structural liveness): the re-exec carries the
// resolved inbox path as a hidden --inbox arg so the bash-era liveness predicate
// (meta_listener_alive greps `ps -o args=` for the inbox) reads this Go follower as
// alive during coexistence. A TRUE syscall.Exec (image replace, not a child fork)
// keeps the pid, so the listenerPid recorded just below IS the follower's pid — no
// re-record, no window. os.Environ() is passed EXPLICITLY: a dropped CBUS_DIR would
// send the re-exec'd follower at the wrong inbox.
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
	armMeta(metaPath) // best-effort: listenerPid=own pid, ownerPid, lastActivity (D3)
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve self: %v", err)
	}
	return syscall.Exec(self, TailArgv(self, inbox, mode), os.Environ())
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
	if owner, ok := OwnerPID(); ok {
		m.OwnerPid = json.RawMessage(strconv.Itoa(owner))
	} else {
		m.OwnerPid = jsonNull
	}
	m.LastActivity = Now()
	_ = writeMeta(filepath.Dir(metaPath), m)
}

// RunFollower is the blocking local tail: it streams framed inbox events to stdout,
// one write per frame, FOREVER. It is the re-exec target of ArmLocalTail; the Monitor
// tool stops it by killing the process — the follower NEVER self-exits (a vanished
// inbox is polled until it returns; a rotation is followed like tail -F).
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
