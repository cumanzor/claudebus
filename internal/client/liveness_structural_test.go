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

// TestPredicateStamplessArmedReadsDead is what the transition branch collapsed into.
// A meta that records a listenerPid and NO listenerStart used to be judged on the argv
// clause; with that clause deleted there is no second opinion, so the peer reads dead.
//
// The pid is GENUINELY ALIVE, and that is the whole design of the case: it removes
// every other reason the predicate could answer dead, leaving the missing witness as
// the only possible cause. A dead pid here would pass for the wrong reason and pin
// nothing.
//
// The fixture is hand-staged and no current code path writes it: a pre-v0.4.0 binary
// did, before arming recorded a structural witness (port-map D1, and the header of the
// deleted liveness_transition.go). It is a historically-reachable state rather than an
// invented one, which is why it is still worth a test after its writer is gone.
func TestPredicateStamplessArmedReadsDead(t *testing.T) {
	f := newStructuralFixture(t)
	cases := []struct {
		name string
		meta string
	}{
		{"no start field at all", fmt.Sprintf(`{"listenerPid":%d}`, f.live)},
		{"empty start field is the same as absent", fmt.Sprintf(`{"listenerPid":%d,"listenerStart":""}`, f.live)},
		{"live owner does not rescue it", fmt.Sprintf(`{"listenerPid":%d,"ownerPid":%d}`, f.live, os.Getpid())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.write(t, c.meta)
			if MetaListenerAlive(f.metaPath) {
				t.Error("an armed meta with no structural witness must read dead — there is no other branch left to answer")
			}
		})
	}
}

// TestPredicateMismatchedWitnessReadsDead pins the primary structural rule: a recorded
// starttime that does not match the process now wearing that pid reads dead, which is
// exactly what a recycled pid looks like.
//
// It was TestPredicateStructuralDoesNotFallBack, and it earned that name against the
// argv-grep branch that used to sit beside this one — it was the one case a
// `structural(m) || argv(m)`
// implementation would have failed, since the argv clause would have said alive here.
// With that branch deleted there is nothing left to fall back TO, so the old name
// promised a guarantee about a second branch that no longer exists. The assertion is
// unchanged; only the claim it makes about why it matters is.
func TestPredicateMismatchedWitnessReadsDead(t *testing.T) {
	f := newStructuralFixture(t)
	f.write(t, fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, f.live, f.startOf(t, f.other)))
	if MetaListenerAlive(f.metaPath) {
		t.Error("a recorded starttime that mismatches the live process must read dead")
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
// The argv clause this replaced read a zombie DEAD for free (a zombie's cmdline is
// empty), and the port pinned that in TestArgvClauseZombieDead, deleted with the clause
// itself — so this test is where that pinned edge now lives, not a new requirement.
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
