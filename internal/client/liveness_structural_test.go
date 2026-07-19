package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// structuralFixture is one peer dir with an inbox, plus three processes:
// live    — argv carries the inbox path (what a pre-P3 follower looks like)
// other   — alive, argv does NOT carry the inbox path
// deadPid — reaped
type structuralFixture struct {
	metaPath string
	inbox    string
	live     int
	other    int
	deadPid  int
}

func newStructuralFixture(t *testing.T) *structuralFixture {
	t.Helper()
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	spawn := func(name string, arg ...string) int {
		// same USER_HZ separation startedChild needs (F2): linux starttime is 10ms
		// ticks, so live and other spawned back-to-back would share a token and the
		// "another live process's start" case would silently stop being a mismatch.
		time.Sleep(50 * time.Millisecond)
		cmd := exec.Command(name, arg...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		})
		return cmd.Process.Pid
	}
	dead := exec.Command("true")
	_ = dead.Run()
	return &structuralFixture{
		metaPath: filepath.Join(dir, "meta.json"),
		inbox:    inbox,
		live:     spawn("tail", "-f", inbox),
		other:    spawn("sleep", "30"),
		deadPid:  dead.Process.Pid,
	}
}

func (f *structuralFixture) write(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(f.metaPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *structuralFixture) startOf(t *testing.T, pid int) string {
	t.Helper()
	tok, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime(%d): %v", pid, err)
	}
	return tok
}

// TestPredicateStructuralBranch is the starttime-present half. The "matches" case
// only proves wiring (it writes with the same primitive it reads with); the load
// bearing cases are the MISMATCHES, which are what pid reuse actually looks like.
func TestPredicateStructuralBranch(t *testing.T) {
	f := newStructuralFixture(t)
	liveStart := f.startOf(t, f.live)
	otherStart := f.startOf(t, f.other)

	cases := []struct {
		name  string
		meta  string
		alive bool
	}{
		{"live pid, matching start", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, f.live, liveStart), true},
		// the recycle guard, expressed the way the field produces it: the pid is alive
		// and it is a REAL process, it is simply not the process that armed. This is a
		// recycled pid after a reboot or a pid-space wrap, not a corrupted file.
		{"live pid, another live process's start", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, f.live, otherStart), false},
		{"live pid, garbage start", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":"0.0"}`, f.live), false},
		{"dead pid, matching-shaped start", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, f.deadPid, liveStart), false},
		{"null pid with a start recorded", fmt.Sprintf(`{"listenerPid":null,"listenerStart":%q}`, liveStart), false},
		{"live+match, live owner", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q,"ownerPid":%d}`, f.live, liveStart, os.Getpid()), true},
		{"live+match, dead owner", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q,"ownerPid":%d}`, f.live, liveStart, f.deadPid), false},
		{"live+match, null owner", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q,"ownerPid":null}`, f.live, liveStart), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.write(t, c.meta)
			if got := MetaListenerAlive(f.metaPath); got != c.alive {
				t.Errorf("MetaListenerAlive = %v, want %v", got, c.alive)
			}
		})
	}
}

// TestPredicateTransitionBranch is the starttime-absent half (R3): a follower armed
// by a pre-upgrade binary has no listenerStart, and its --inbox argv is ground truth
// about it. Without this branch the upgrade reaps every live peer.
func TestPredicateTransitionBranch(t *testing.T) {
	f := newStructuralFixture(t)
	cases := []struct {
		name  string
		meta  string
		alive bool
	}{
		{"no start field, argv carries the inbox", fmt.Sprintf(`{"listenerPid":%d}`, f.live), true},
		{"empty start field is treated as absent", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":""}`, f.live), true},
		{"no start field, argv lacks the inbox", fmt.Sprintf(`{"listenerPid":%d}`, f.other), false},
		{"no start field, dead pid", fmt.Sprintf(`{"listenerPid":%d}`, f.deadPid), false},
		{"no start field, dead owner", fmt.Sprintf(`{"listenerPid":%d,"ownerPid":%d}`, f.live, f.deadPid), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.write(t, c.meta)
			if got := MetaListenerAlive(f.metaPath); got != c.alive {
				t.Errorf("MetaListenerAlive = %v, want %v", got, c.alive)
			}
		})
	}
}

// TestPredicateStructuralDoesNotFallBack is the anti-fallback gate, and the one case
// the rest of the matrix cannot catch. An implementation written as
// `structural(m) || argv(m)` passes every other test in this file. Here the argv
// clause WOULD say alive (the pid's argv really does carry the inbox) while the
// recorded starttime says this is not that process. Structural must win outright.
//
// If this ever fails, the shim has become a resurrection path for recycled pids,
// which is the exact hole the milestone exists to close.
func TestPredicateStructuralDoesNotFallBack(t *testing.T) {
	f := newStructuralFixture(t)
	f.write(t, fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, f.live, f.startOf(t, f.other)))
	if MetaListenerAlive(f.metaPath) {
		t.Error("a recorded starttime that mismatches must read dead even when argv would match — no fallback")
	}
}

// jiffiesPart strips a linux token's "<boot_id>:" prefix, leaving the boot-relative
// counter. A darwin token has no prefix and is returned whole.
func jiffiesPart(token string) string {
	if i := strings.LastIndexByte(token, ':'); i >= 0 {
		return token[i+1:]
	}
	return token
}

// TestPredicateRejectsSameCounterDifferentBoot is B4 at the predicate level. On linux
// the token is "<boot_id>:<jiffies>" and jiffies are boot-relative, while $CBUS_DIR
// (~/.claude-bus) outlives a reboot — so without the boot component a post-reboot pid
// could byte-match a pre-reboot record and a stranger would read as the armed
// listener. Swapping ONLY the boot id keeps the counter identical, which is the exact
// collision the prefix exists to break.
//
// On darwin the token is absolute microseconds with no boot component, so here this
// degenerates into a generic mismatch; the boot-specific proof on this host is
// TestLinuxStartTokenBootIDVariation through the B1 composer seam, and the container
// run exercises the real linux path.
func TestPredicateRejectsSameCounterDifferentBoot(t *testing.T) {
	f := newStructuralFixture(t)
	real := f.startOf(t, f.live)
	variant := "00000000-0000-0000-0000-000000000000:" + jiffiesPart(real)
	if variant == real {
		t.Fatalf("variant is identical to the real token %q — the case proves nothing", real)
	}
	f.write(t, fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, f.live, variant))
	if MetaListenerAlive(f.metaPath) {
		t.Error("same pid and counter under a DIFFERENT boot must read dead")
	}
}

// TestPredicateStructuralZombieReadsDead is F3: a dead-but-unreaped follower must not
// read alive. It is the one edge where the structural witness is WEAKER than the argv
// clause it replaced, so it needs its own case rather than riding the matrix.
//
// On linux /proc/<pid>/stat stays readable at state=Z with the ORIGINAL starttime
// intact, and kill -0 still succeeds for a zombie, so the recorded token byte-matches a
// process that has already exited. Without an explicit zombie guard the peer passes the
// send gate, survives prune and keeps receiving broadcasts, while nothing is listening.
// The argv clause read this DEAD for free (a zombie's cmdline is empty), and the port
// pinned zombie=dead in TestArgvClauseZombieDead, so this is a regression of a pinned
// edge and not a new requirement.
//
// darwin degenerates here in the B4 sense: it is already safe, but by accident rather
// than by the guard. proc_pidinfo errors for a zombie, so procStartTime fails and R2's
// probe-error-is-dead rule catches it. The guard is what makes the behavior INTENDED on
// both platforms instead of load-bearing on a libproc implementation detail.
func TestPredicateStructuralZombieReadsDead(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta.json")

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Wait() }) // reap at the end, never before the assertion

	// capture the token while the process is genuinely ALIVE — this is what armMeta
	// would have recorded, so the fixture is what a real armed follower leaves behind.
	start, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime on a live child: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}

	// wait for it to become a zombie: exited, not reaped, kill -0 still succeeding.
	// two independent zombie signals, because neither is portable alone: linux reports
	// state=Z via procZombie, while a darwin zombie's args EINVAL out of KERN_PROCARGS2
	// (procZombie there is a documented fail-open that never fires).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, argvErr := procArgs(pid); procZombie(pid) || argvErr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pidAlive(pid) {
		t.Skip("child was reaped before the zombie window could be asserted (kernel timing)")
	}

	if err := os.WriteFile(metaPath,
		[]byte(fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, pid, start)), 0o644); err != nil {
		t.Fatal(err)
	}
	if MetaListenerAlive(metaPath) {
		t.Error("a zombie listener must read DEAD — its token still matches, but nothing is listening")
	}
	if !PeerDead(metaPath) {
		t.Error("a zombie listener's peer must be PeerDead, or prune and the send gate will treat it as live")
	}
}
