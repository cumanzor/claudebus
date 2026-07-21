package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeSessionTree builds a two-level process tree shaped like the bug that made
// ClosePeer false-succeed: a shell whose ARGV[0] basename is `name` but whose kernel
// comm is the real binary's ("bash"), plus a sleeping child standing in for an armed
// listener. perl does the exec because POSIX sh cannot set argv[0], and rewriting
// argv[0] is the whole point — a fake that also reported comm "claude" would pass
// through the comm fallback and prove nothing about the fix.
//
// Three properties make this safe to run a SIGNAL test against, and all three are
// asserted rather than assumed:
//   - the owner is ORPHANED to PID 1 (the launching shell exits immediately), so the
//     walk terminates at init and can never climb into the real claude session
//     running these tests. That is the barrier: it holds even if the match fails,
//     which is exactly when an unbarriered walk would find the developer's own
//     session and SIGTERM it.
//   - it is setsid'd, so it has no controlling tty and the surface sweep finds
//     nothing to close.
//   - both pids are killed on cleanup.
//
// listenerArg is placed in the CHILD's argv. Nothing reads it any more — identity is
// (pid, starttime) against the meta's own record — so it survives as scene-setting:
// a candidate that LOOKS like this peer's follower in every visible way, which is what
// makes the guard's refusals meaningful rather than lucky.
func fakeSessionTree(t *testing.T, name, listenerArg string) (ownerPid, childPid int) {
	t.Helper()
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl needed to rewrite argv[0]")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, name)
	if err := os.Symlink("/bin/sh", fake); err != nil {
		t.Fatal(err)
	}
	// the child is a script rather than an inline -c so its argv carries listenerArg
	// without a second level of shell quoting
	child := filepath.Join(dir, "listener.sh")
	if err := os.WriteFile(child, []byte("#!/bin/sh\n/bin/sleep 60 & wait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pids := filepath.Join(dir, "pids")
	// written to a tmp path and renamed so the reader never sees one pid of two
	inner := fmt.Sprintf(`echo $$ > %s.tmp; %s %s & echo $! >> %s.tmp; mv %s.tmp %s; wait`,
		pids, shQuote(child), shQuote(listenerArg), pids, pids, pids)
	launch := exec.Command("/bin/sh", "-c",
		fmt.Sprintf(`perl -e 'exec {"/bin/sh"} ($ARGV[0], "-c", $ARGV[1])' %s %s &`,
			shQuote(fake), shQuote(inner)))
	launch.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := launch.Run(); err != nil {
		t.Fatalf("launching the fake tree: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var parsed []int
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pids)
		if err == nil {
			parsed = parsed[:0]
			for _, f := range strings.Fields(string(b)) {
				if n, cerr := strconv.Atoi(f); cerr == nil {
					parsed = append(parsed, n)
				}
			}
			if len(parsed) == 2 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(parsed) != 2 {
		t.Fatal("the fake tree never reported its pids")
	}
	ownerPid, childPid = parsed[0], parsed[1]
	t.Cleanup(func() {
		_ = syscall.Kill(ownerPid, syscall.SIGKILL)
		_ = syscall.Kill(childPid, syscall.SIGKILL)
	})

	// the barrier, asserted: without it a failed match walks into the real session
	if _, ppid, err := procParent(ownerPid); err != nil || ppid != 1 {
		t.Fatalf("fake owner ppid = %d (err %v), want 1 — refusing to run a signal "+
			"test whose walk could reach a real claude session", ppid, err)
	}
	return ownerPid, childPid
}

// TestOwnerFromPidMatchesArgv0 is the regression for the blocker: the owning session
// is found by argv[0]'s basename, NOT by kernel comm. The fake's comm is the real
// binary's name, reproducing the bun CLI whose accounting name is its version string
// ("2.1.214") — under the old comm-only walk this returns nothing, which is what
// starved every registration of its ownerPid and made close report success on a live
// peer.
func TestOwnerFromPidMatchesArgv0(t *testing.T) {
	owner, child := fakeSessionTree(t, "claude", "")

	// the premise: comm does NOT identify this process as claude
	if comm, _, err := procParent(owner); err != nil {
		t.Fatal(err)
	} else if isHarnessComm(commBase(comm)) {
		t.Skipf("this platform reports comm %q, so the fake cannot exercise the argv path", comm)
	}

	got, ok := ownerFromPid(child)
	if !ok || got != owner {
		t.Errorf("ownerFromPid(child) = %d,%v; want the owner %d — the argv[0] walk missed", got, ok, owner)
	}
	// the walk inspects the starting pid itself, not only its ancestors
	if got, ok := ownerFromPid(owner); !ok || got != owner {
		t.Errorf("ownerFromPid(owner) = %d,%v; want %d", got, ok, owner)
	}
}

// TestOwnerFromPidIgnoresANonClaudeAncestor: an identically-shaped tree whose name is
// not claude yields no owner. Pins that the match is the NAME and not merely "some
// ancestor exists" — and the orphaning is what makes the negative deterministic
// rather than dependent on whatever launched the test.
func TestOwnerFromPidIgnoresANonClaudeAncestor(t *testing.T) {
	_, child := fakeSessionTree(t, "notclaude", "")
	if got, ok := ownerFromPid(child); ok {
		t.Errorf("ownerFromPid = %d,%v; want no match for a non-claude tree", got, ok)
	}
}

// TestClosePeerDerivesOwnerFromNullOwnerPid is the end-to-end blocker regression: a
// registration written before the fix records ownerPid null, and close must derive
// the owner from the armed listener's ancestry and END it — not report the cheerful
// "already gone" that left live sessions running.
func TestClosePeerDerivesOwnerFromNullOwnerPid(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	// a listener that looks like THIS peer's follower in every visible way; what makes
	// it genuine is the recorded witness seeded below, the same identity test
	// MetaListenerAlive applies
	needle := filepath.Join(root, "ch", "peer", "inbox.jsonl")
	owner, child := fakeSessionTree(t, "claude", needle)
	start, err := procStartTime(child)
	if err != nil {
		t.Fatalf("procStartTime(child): %v", err)
	}
	seedClosePeerWithStart(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(child), start)

	rep := ClosePeer("ch", "peer", false)
	if !rep.Ok {
		t.Fatalf("close failed: %s", rep.Detail)
	}
	if strings.Contains(rep.Detail, "already gone") {
		t.Fatalf("close false-succeeded on a LIVE peer: %s", rep.Detail)
	}
	if pidAlive(owner) && !procZombie(owner) {
		t.Errorf("owner %d survived the close", owner)
	}
}

// TestClosePeerStillReportsGoneWhenTheListenerIsDead: the null-ownerPid fallback must
// not invent an owner. With no live listener to walk from there is genuinely nothing
// to end, and that is a success — the case my own CLI matrix covered while missing
// that null was ALSO the universal live case.
func TestClosePeerStillReportsGoneWhenTheListenerIsDead(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	// a pid that has certainly exited: spawn and reap one
	dead := exec.Command("/bin/sh", "-c", "exit 0")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	seedClosePeer(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(dead.Process.Pid))

	rep := ClosePeer("ch", "peer", false)
	if !rep.Ok || !strings.Contains(rep.Detail, "already gone") {
		t.Errorf("want an already-gone success, got ok=%v %q", rep.Ok, rep.Detail)
	}
}

func seedClosePeer(t *testing.T, root, ch, al, sid, ownerPid, listenerPid string) {
	t.Helper()
	dir := filepath.Join(root, ch, al)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"alias":%q,"channel":%q,"sessionId":%q,"cwd":"/w","listenerPid":%s,"ownerPid":%s,"host":"h","ts":"2026-07-18T00:00:00Z"}`,
		al, ch, sid, listenerPid, ownerPid)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeBins puts stub executables on PATH (name -> shell body), so each subprocess the
// sweep shells out to can be made slow, silent or talkative deterministically. The
// sweep is defined entirely by what those three commands say, so faking them is the
// only way to exercise it without a terminal.
func fakeBins(t *testing.T, bins map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range bins {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// TestSweepSurfaceTimesOutRatherThanStalling: the sweep is best-effort cleanup that
// runs AFTER the process is already dead, so a wedged Apple Event queue must cost a
// bounded wait, not the ~45s stall observed in a detached shell.
func TestSweepSurfaceTimesOutRatherThanStalling(t *testing.T) {
	prev := surfaceSweepBudget
	surfaceSweepBudget = 100 * time.Millisecond
	t.Cleanup(func() { surfaceSweepBudget = prev })
	fakeBins(t, map[string]string{"ps": "sleep 30"})

	start := time.Now()
	got := sweepSurface("ttys999")
	if !strings.Contains(got, "timed out") {
		t.Errorf("sweep = %q, want a timeout report", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("sweep took %v — the deadline did not bound it", elapsed)
	}
}

// TestSweepSurfaceTimesOutOnAWedgedOsascript is the case the deadline was actually
// filed for: the tty probes answer instantly and it is the Apple Event that hangs.
// Without the trailing deadline check this reports "surface already closed" — a
// cleanup we never performed, claimed as done.
func TestSweepSurfaceTimesOutOnAWedgedOsascript(t *testing.T) {
	prev := surfaceSweepBudget
	surfaceSweepBudget = 150 * time.Millisecond
	t.Cleanup(func() { surfaceSweepBudget = prev })
	fakeBins(t, map[string]string{
		"ps":        "exit 1", // dead tty: nothing on it
		"tmux":      "exit 1", // no multiplexer
		"osascript": "sleep 30",
	})

	start := time.Now()
	got := sweepSurface("ttys999")
	if strings.Contains(got, "already closed") {
		t.Fatalf("sweep claimed %q while osascript was still wedged", got)
	}
	if !strings.Contains(got, "timed out") {
		t.Errorf("sweep = %q, want a timeout report", got)
	}
	// this assertion was missing, and its absence hid the same defect the sibling test
	// caught: the report string was right while the sweep still ran the fake's full 30s.
	// A timeout that is only reported and not enforced is the bug wearing the fix's face.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("sweep took %v — the deadline did not bound it", elapsed)
	}
}

// TestSweepSurfaceLeavesABusyTTYAlone: any live process on the tty means either a
// TERM-survivor or a recycled tty hosting a stranger. Both must keep their surface —
// this is the guard that stops a close from taking someone else's window with it.
func TestSweepSurfaceLeavesABusyTTYAlone(t *testing.T) {
	fakeBins(t, map[string]string{"ps": "echo 4242"}) // a live pid on that tty
	if got := sweepSurface("ttys999"); !strings.Contains(got, "busy") {
		t.Errorf("sweep = %q, want the busy-tty refusal", got)
	}
}

// TestSweepSurfaceNeedsATTY: with no tty captured there is nothing to locate, and the
// sweep must say so rather than probe with an empty device path.
func TestSweepSurfaceNeedsATTY(t *testing.T) {
	if got := sweepSurface(""); !strings.Contains(got, "no tty") {
		t.Errorf("sweep = %q, want the no-tty report", got)
	}
}

// TestClosePeerIgnoresARecycledListenerPidStructurally is the highest-consequence use
// of the identity test anywhere in the codebase: a wrong answer here hands an innocent
// session's pid to SIGTERM and closes a window nobody asked to close.
//
// The setup is deliberately the hardest one for the guard. The listener pid is alive,
// IS descended from a claude-shaped owner, and its argv even carries this peer's inbox
// path, so every surface clue says "this is my follower". Only the recorded
// listenerStart disagrees, and that single mismatch must stop the close.
//
// The inbox path in the child's argv is now decoration: nothing reads it, since
// identity is (pid, starttime) against this meta's own record. It is kept because the
// adversarial STORY is the point — a candidate that looks right in every visible way
// must still be refused. It is also why the foreign-listener case is not a separate
// test any more: a foreign live follower wearing the recorded pid and a recycled
// stranger wearing it are the same case, witness mismatch, and pinning it twice under
// two names would be fake coverage.
func TestClosePeerIgnoresARecycledListenerPidStructurally(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	needle := filepath.Join(root, "ch", "peer", "inbox.jsonl")
	owner, child := fakeSessionTree(t, "claude", needle)

	// a start token that is well-formed but belongs to some other process
	seedClosePeerWithStart(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(child), "1.1")

	rep := ClosePeer("ch", "peer", false)
	if !rep.Ok || !strings.Contains(rep.Detail, "already gone") {
		t.Errorf("a recycled listener must read as nothing-to-close, got ok=%v %q", rep.Ok, rep.Detail)
	}
	if !pidAlive(owner) || procZombie(owner) {
		t.Error("the innocent session was SIGTERMed — the structural guard did not hold")
	}
}

// TestClosePeerAcceptsAMatchingStructuralListener is the other side: with a listener
// whose recorded token really is its own, close must still derive the owner and end
// it. Without this, a guard that simply refused everything would look correct.
func TestClosePeerAcceptsAMatchingStructuralListener(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	needle := filepath.Join(root, "ch", "peer", "inbox.jsonl")
	owner, child := fakeSessionTree(t, "claude", needle)
	start, err := procStartTime(child)
	if err != nil {
		t.Fatalf("procStartTime(child): %v", err)
	}
	seedClosePeerWithStart(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(child), start)

	rep := ClosePeer("ch", "peer", false)
	if !rep.Ok {
		t.Fatalf("close failed: %s", rep.Detail)
	}
	if strings.Contains(rep.Detail, "already gone") {
		t.Fatalf("close false-succeeded on a LIVE peer: %s", rep.Detail)
	}
	if pidAlive(owner) && !procZombie(owner) {
		t.Errorf("owner %d survived the close", owner)
	}
}

func seedClosePeerWithStart(t *testing.T, root, ch, al, sid, ownerPid, listenerPid, listenerStart string) {
	t.Helper()
	dir := filepath.Join(root, ch, al)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"alias":%q,"channel":%q,"sessionId":%q,"cwd":"/w","listenerPid":%s,"ownerPid":%s,`+
			`"listenerStart":%q,"host":"h","ts":"2026-07-18T00:00:00Z"}`,
		al, ch, sid, listenerPid, ownerPid, listenerStart)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
