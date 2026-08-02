package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"claudebus/internal/core"
)

// codexstophook is the Stop-hook fallback delivery path for a plain codex exec worker, where
// the app-server bridge is not available but hooks DO fire (cbus-6ij.4 tranche B). On a Stop
// event it long-polls THIS session's inbox within the codex hook timeout and, on new traffic,
// returns {"decision":"block","reason":<framed>} which codex injects as a continuation turn.
// timeout is a FAILURE, never a signal, so the wait must stay under the codex limit; no traffic
// => empty return => no stdout => allow the stop.

const (
	stopCursorFile = ".stop-cursor" // dot-prefixed sidecar; channel walkers skip it, and it is
	// distinct from the follower .cursor: two consumers must never share a cursor, and the
	// follower's seek-to-EOF re-arm semantics are wrong for replay-since-last-turn.
	// StopHookDefaultWait is safely under the codex 600s Stop timeout; the verb defaults to it.
	StopHookDefaultWait = 550 * time.Second
)

// StopHook long-polls this session's inbox and returns a Stop block JSON on new traffic, or ""
// to allow the stop. Identity comes from the lenient Stop stdin (session_id/sessionId), else
// the SessionID() env chain. Best-effort and silent: a missing id, no registration, or a
// malformed payload is a no-op. StopHookActive (a re-entry with nothing new) returns at once so
// a worker never re-blocks on ceremony.
func StopHook(stdin io.Reader, wait time.Duration) string {
	in := readHookInput(stdin)
	sid := in.sid()
	if sid == "" {
		sid = SessionID()
	}
	if sid == "" {
		return ""
	}
	defer OverrideSessionID(sid)()
	regs := ResolveSelf()
	if len(regs) == 0 {
		return ""
	}
	if frames := collectNewFrames(regs); len(frames) > 0 {
		return blockJSON(frames)
	}
	if in.StopHookActive {
		return "" // re-entry with nothing new: allow the stop, do not re-block on ceremony
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		time.Sleep(followPoll)
		if frames := collectNewFrames(regs); len(frames) > 0 {
			return blockJSON(frames)
		}
	}
	return "" // timed out with no traffic: allow the stop (timeout is failure, never a signal)
}

type timedFrame struct {
	ts    string
	frame string
}

// collectNewFrames drains new inbox lines since the .stop-cursor across ALL of this session's
// registrations (a worker hears every channel it joined), preserving per-inbox order and
// ordering across inboxes best-effort by ts. Presence/status lines are SKIPPED, as the
// app-server bridge skips them (frameKind): a continuation turn per join/leave/compact would
// burn model turns on ceremony. This mirrors the bridge's ruled presence-skip.
func collectNewFrames(regs []LocalReg) []timedFrame {
	var out []timedFrame
	for _, reg := range regs {
		peerDir := filepath.Join(CBUSDir(), reg.Channel, reg.Alias)
		out = append(out, readNewInboxFrames(filepath.Join(peerDir, "inbox.jsonl"), peerDir)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ts < out[j].ts })
	return out
}

// readNewInboxFrames returns the framed NEW chat lines in inbox since the .stop-cursor and
// advances the cursor over everything it consumed (presence included, so it is not re-read).
// The cursor advances only when something was consumed, so an empty poll leaves it untouched.
func readNewInboxFrames(inbox, peerDir string) []timedFrame {
	curDev, curIno, curSize, iok := fileIdentity(inbox)
	if !iok {
		return nil
	}
	start := int64(0)
	if dev, ino, off, ok := readStopCursor(peerDir); ok && dev == curDev && ino == curIno && off <= curSize {
		start = off // resume; a dev/ino mismatch or past-EOF offset means a rejoin truncated the
		// inbox, so byte 0 replays since join and loses nothing (join truncates).
	}
	f, err := openSharedRead(inbox) // never os.Open: a held stdlib handle blocks the inbox from being removed on windows
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	r := bufio.NewReader(f)
	var frames []timedFrame
	consumed := start
	for {
		line, rerr := r.ReadString('\n')
		if strings.HasSuffix(line, "\n") { // only a COMPLETE line advances the cursor
			consumed += int64(len(line))
			if frameKind([]byte(line)) == "" { // deliver chat only; skip presence/status
				if frame := core.LocalEmit([]byte(line)); frame != nil {
					frames = append(frames, timedFrame{ts: lineTS(line), frame: string(frame)})
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	if iok && consumed > start {
		writeStopCursor(peerDir, curDev, curIno, consumed)
	}
	return frames
}

// blockJSON renders the Stop block. Each message keeps its own LocalEmit frame inside the batch
// (joined by a blank-safe newline) so provenance survives; one block is one continuation turn.
func blockJSON(frames []timedFrame) string {
	parts := make([]string, len(frames))
	for i, f := range frames {
		parts[i] = f.frame
	}
	b, _ := json.Marshal(map[string]string{"decision": "block", "reason": strings.Join(parts, "\n")})
	return string(b)
}

// lineTS pulls the ts off a raw inbox line for cross-inbox ordering.
func lineTS(line string) string {
	var m struct {
		TS string `json:"ts"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(line)), &m)
	return m.TS
}

func readStopCursor(peerDir string) (dev, ino uint64, off int64, ok bool) {
	b, err := os.ReadFile(filepath.Join(peerDir, stopCursorFile))
	if err != nil {
		return 0, 0, 0, false // absent/unreadable: start from 0 (join truncated the inbox)
	}
	fields := strings.Fields(strings.TrimSpace(string(b)))
	if len(fields) != 3 {
		return 0, 0, 0, false
	}
	d, e1 := strconv.ParseUint(fields[0], 10, 64)
	i, e2 := strconv.ParseUint(fields[1], 10, 64)
	o, e3 := strconv.ParseInt(fields[2], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || o < 0 {
		return 0, 0, 0, false
	}
	return d, i, o, true
}

// writeStopCursor records the position atomically (temp+rename), best-effort: a failed write
// costs a re-delivery on the next Stop, never a broken hook.
func writeStopCursor(peerDir string, dev, ino uint64, off int64) {
	tmp := filepath.Join(peerDir, ".stop-cursor.tmp."+strconv.Itoa(os.Getpid()))
	if os.WriteFile(tmp, []byte(fmt.Sprintf("%d %d %d\n", dev, ino, off)), 0o644) != nil {
		return
	}
	if os.Rename(tmp, filepath.Join(peerDir, stopCursorFile)) != nil {
		_ = os.Remove(tmp)
	}
}
