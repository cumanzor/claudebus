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
func fakeSessionTree(t *testing.T, name string) (ownerPid, childPid int) {
	t.Helper()
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl needed to rewrite argv[0]")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, name)
	if err := os.Symlink("/bin/sh", fake); err != nil {
		t.Fatal(err)
	}
	pids := filepath.Join(dir, "pids")
	// written to a tmp path and renamed so the reader never sees one pid of two
	inner := fmt.Sprintf(`echo $$ > %s.tmp; /bin/sleep 60 & echo $! >> %s.tmp; mv %s.tmp %s; wait`,
		pids, pids, pids, pids)
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
	owner, child := fakeSessionTree(t, "claude")

	// the premise: comm does NOT identify this process as claude
	if comm, _, err := procParent(owner); err != nil {
		t.Fatal(err)
	} else if isClaudeName(comm) {
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
	_, child := fakeSessionTree(t, "notclaude")
	if got, ok := ownerFromPid(child); ok {
		t.Errorf("ownerFromPid = %d,%v; want no match for a non-claude tree", got, ok)
	}
}

// TestClosePeerDerivesOwnerFromNullOwnerPid is the end-to-end blocker regression: a
// registration written before the fix records ownerPid null, and close must derive
// the owner from the armed listener's ancestry and END it — not report the cheerful
// "already gone" that left live sessions running.
func TestClosePeerDerivesOwnerFromNullOwnerPid(t *testing.T) {
	owner, child := fakeSessionTree(t, "claude")
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "some-other-session")
	seedClosePeer(t, root, "ch", "peer", "sid-peer", "null", strconv.Itoa(child))

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
