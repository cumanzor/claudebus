package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// waitForOwnCursor is the wait the three cbus-que.10 members share. All three blocked on
// the SAME predicate — readCursor giving cursorValid at a non-zero offset — so they are
// one unmet condition seen from three fixtures rather than three timeout shapes.
//
// The loop is byte-for-byte waitFor's: same deadline, same interval, same success. It
// decides NOTHING differently. Everything added here runs only after the timeout has
// already been reached, and it exists because the old failure said "timeout waiting for
// X" and threw away every signal that separates the candidate mechanisms.
//
// Three signals, captured together so ONE scoped run splits the hypotheses per member
// instead of three sequential probe rounds:
//
//   - cursorState. The predicate computed it and discarded it. ABSENT means no cursor
//     file was ever created, so the stall is on the WRITE side. CORRUPT means the file
//     EXISTS and the read failed, which is the cbus-que.12 transient-reads class and the
//     only result that makes the shared mechanism between those beads real.
//   - running. The follower checks its identity BEFORE writing, and on failure emits a
//     dormant marker and returns. A follower that is gone when the wait expires means it
//     took that door and no cursor write was ever attempted.
//   - a leftover .cursor.tmp.<pid>. writeCursor discards both its errors: WriteFile
//     failing returns silently, Rename failing removes the tmp and returns silently. A
//     surviving tmp means the write landed and the RENAME failed, which is the windows
//     shape where a rename cannot replace a file something still holds open.
//
// running may be nil for a fixture that did not keep the handle; the report says so
// rather than pretending the signal was measured.
func waitForOwnCursor(t *testing.T, peer string, running func() bool, frames func() string, msg string) {
	t.Helper()
	mode, poll := probeVariant()
	// STAT-ONLY holds poll FREQUENCY constant and varies only the HANDLE, which is the one
	// variable H8 names. Widening the interval instead moves handle-frequency and cpu-burn
	// together, so a landing cursor would be equally consistent with handle contention and
	// with a 3ms spin starving the follower goroutine — a confirmation that would go to
	// whichever story the reader already held.
	//
	// os.Stat does not open the target (GetFileAttributesEx on windows), so this variant
	// polls at the same rate while holding zero handles on .cursor. Success is an mtime
	// that differs from the one the arm-time write left, since any loop write replaces it.
	deadline := time.Now().Add(2 * time.Second)

	// EXISTENCE PRE-PHASE. The arm-time writeCursor runs in the follower GOROUTINE
	// (follow_test.go:137), so whether .cursor exists when the baseline is taken is a
	// race. Capturing an absent file left baseMTime at the ZERO time, which made the
	// arrival of the ARM write satisfy "mtime changed" — the wait then PASSED with the
	// offset still at 0, which is the symptom this probe exists to explain.
	//
	// The wait is measured, not merely avoided: if the cursor routinely appears late that
	// is data about follower start-up on windows, and it costs one field.
	var baseMTime time.Time
	var baseSize int64
	waited, haveBase := time.Duration(0), false
	if mode == "stat" {
		start := time.Now()
		for time.Now().Before(deadline) {
			if fi, err := os.Stat(cursorPath(peer)); err == nil {
				baseMTime, baseSize, haveBase = fi.ModTime(), fi.Size(), true
				break
			}
			time.Sleep(poll)
		}
		waited = time.Since(start)
		if !haveBase {
			// distinct from a plain timeout: a cursor that is NEVER CREATED is its own
			// finding, and reporting it as "the offset did not advance" would hide it.
			t.Logf("%s member=%q variant=%s pollms=%d outcome=NOCURSOR waitedms=%d %s",
				cursorWaitMarker, t.Name(), mode, poll.Milliseconds(), waited.Milliseconds(),
				cursorWaitSignals(peer, running, frames))
			t.Fatalf("no cursor file ever appeared for %s — the arm-time write never landed, which is NOT the offset-never-advanced case", msg)
		}
	}

	// both signals, never one: a windows mtime coarser than the gap between the arm write
	// and the first loop write would hide a real change, and a size that happens to match
	// would hide it the other way. Disagreement is informative and both are logged.
	landed := func() bool {
		if mode == "stat" {
			fi, err := os.Stat(cursorPath(peer))
			return err == nil && (!fi.ModTime().Equal(baseMTime) || fi.Size() != baseSize)
		}
		_, _, off, st := readCursor(peer)
		return st == cursorValid && off > 0
	}
	for time.Now().Before(deadline) {
		if landed() {
			t.Logf("%s member=%q variant=%s pollms=%d outcome=PASS waitedms=%d %s", cursorWaitMarker, t.Name(), mode, poll.Milliseconds(), waited.Milliseconds(), cursorWaitSignals(peer, running, frames))
			return
		}
		time.Sleep(poll)
	}
	// COLLAPSED, never TIMEOUT: in stat mode a baseline taken after the loop write already
	// landed can never observe a change, so the probe is blind rather than the cursor
	// stuck. off > 0 proves the cursor advanced. Reporting that as a timeout would read as
	// evidence against a mechanism that demonstrably worked.
	outcome := "TIMEOUT"
	if _, _, off, _ := readCursor(peer); mode == "stat" && off > 0 {
		outcome = "COLLAPSED"
	}
	t.Logf("%s member=%q variant=%s pollms=%d outcome=%s waitedms=%d %s", cursorWaitMarker, t.Name(), mode, poll.Milliseconds(), outcome, waited.Milliseconds(), cursorWaitSignals(peer, running, frames))
	if outcome == "COLLAPSED" {
		t.Skipf("stat baseline collapsed (captured after the loop write; off>0 so the cursor DID advance) — no data for %s, not a failure", msg)
	}
	t.Fatalf("timeout waiting for %s — %s", msg, cursorWaitDiagnosis(peer, running, frames))
}

// cursorWaitMarker is emitted once per member per run on BOTH outcomes, so a rate table
// can COUNT observations instead of inferring them from the absence of failures. Those
// are not the same thing: a member that never executed, a binary that wedged before
// reaching it, and a member that passed all produce no failure line, and only a counted
// marker separates the last from the first two. Expected count is members x runs; short
// means data is missing, not that everything was fine.
const cursorWaitMarker = "CURSORWAIT"

// probeVariant reads the D75 matrix from the environment so the DEFAULT is exactly the
// behaviour the 32 existing observations were taken under: read-based, 3ms. A variant is
// opted into, never inherited, so an unset environment cannot silently produce numbers
// that are not comparable with the ones already in the record.
func probeVariant() (mode string, poll time.Duration) {
	mode = os.Getenv("CBUS_PROBE_MODE")
	if mode != "stat" {
		mode = "read"
	}
	poll = 3 * time.Millisecond
	if v := os.Getenv("CBUS_PROBE_POLL_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			poll = time.Duration(n) * time.Millisecond
		}
	}
	return mode, poll
}

// cursorWaitSignals is the COUNTED half: every signal as bare values, on the marker line,
// on BOTH outcomes. It exists because zero is the load-bearing row of the primary probe —
// zero reopens kills two hypotheses — and a signal that is only emitted when non-zero
// makes "counted zero" and "instrument never ran" the same output. That is the inverted
// twin of an empty results file reading as all-passed. Expected emissions are members x
// runs exactly; a short count is missing data, never a clean result.
func cursorWaitSignals(peer string, running func() bool, frames func() string) string {
	_, _, off, st := readCursor(peer)
	live, n := -1, -1
	if running != nil {
		if running() {
			live = 1
		} else {
			live = 0
		}
	}
	if frames != nil {
		n = 0
		for _, ln := range strings.Split(frames(), "\n") {
			if strings.TrimSpace(ln) != "" {
				n++
			}
		}
	}
	tmp := 0
	if _, err := os.Stat(filepath.Join(peer, ".cursor.tmp."+strconv.Itoa(os.Getpid()))); err == nil {
		tmp = 1
	}
	return fmt.Sprintf("off=%d state=%d running=%d lines=%d tmp=%d", off, int(st), live, n, tmp)
}

// cursorWaitDiagnosis renders the three signals. Read-only: it stats and reads, and
// changes nothing, so it cannot alter the state it is reporting on.
func cursorWaitDiagnosis(peer string, running func() bool, frames func() string) string {
	_, _, off, st := readCursor(peer)
	state := map[cursorState]string{
		cursorAbsent:  "ABSENT (no cursor file was ever created — the stall is write-side: H1 identity gate, H2 silent write failure, or H3 the bytes never arrived)",
		cursorCorrupt: "CORRUPT (the file EXISTS and the read failed — H4, the que.12 transient-reads class, and the shared mechanism is real for this member)",
		cursorValid:   "VALID (so the offset is what failed, not the read)",
	}[st]

	live := "running=UNMEASURED (this fixture kept no handle)"
	if running != nil {
		if running() {
			live = "running=YES (the follower is alive and looping, so it did not take the dormancy door — H1 is dead for this member)"
		} else {
			live = "running=NO (the follower exited — it went dormant before writing, which is H1 and kills H2 and H3)"
		}
	}

	tmp := filepath.Join(peer, ".cursor.tmp."+strconv.Itoa(os.Getpid()))
	leftover := "no .cursor.tmp (the write path was never reached, or its cleanup ran)"
	if _, err := os.Stat(tmp); err == nil {
		leftover = "LEFTOVER " + tmp + " (WriteFile succeeded and the RENAME failed — H2, and it fails silently in production)"
	}

	// P2, the PRIMARY discriminator, and it needs no production counter: a rotation resets
	// consumed to 0 and re-reads from byte 0, so every reopen RE-EMITS everything already
	// delivered. The sink's frame count therefore measures the consequence the sawtooth
	// would have to produce, rather than inferring it from consumed values sampled once
	// per iteration — which read 0 under both the flat-zero stall and the sawtooth.
	//   0 frames        the follower never yielded a byte: read-side stall
	//   ~1 per appended the read worked and the WRITE failed: H2, the row a missing tmp
	//                   cannot rule out, since a failed rename deletes its tmp and a
	//                   failed WriteFile never makes one
	//   many duplicates rotation is firing every tick: H5/H6, the sawtooth
	emitted := "lines=UNMEASURED (this fixture kept no sink)"
	if frames != nil {
		n := 0
		for _, ln := range strings.Split(frames(), "\n") {
			if strings.TrimSpace(ln) != "" {
				n++
			}
		}
		emitted = fmt.Sprintf("lines=%d (0 = read-side stall; ~1 per appended line = read fine and the WRITE failed, H2; many = reopen sawtooth, H5/H6)", n)
	}
	return fmt.Sprintf("cursor off=%d state=%s; %s; %s; %s", off, state, live, leftover, emitted)
}
