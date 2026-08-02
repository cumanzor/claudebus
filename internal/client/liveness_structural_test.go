package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// structuralFixture is one peer dir with an inbox, plus three processes:
// live    — alive, stands in for the follower
// other   — alive, a DIFFERENT process with its own start token
// deadPid — reaped
//
// live and other differ only by identity now. They once differed by argv, back when the
// predicate could grep a follower's command line for the inbox path; that clause is
// deleted (liveness.go:108), so neither process needs to hold the inbox and neither
// should — an open handle on windows would block the tempdir cleanup.
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
	return &structuralFixture{
		metaPath: filepath.Join(dir, "meta.json"),
		inbox:    inbox,
		live:     liveProc(t),
		other:    liveProc(t), // liveProc separates spawns so these cannot share a token (F2)
		deadPid:  deadProc(t),
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
