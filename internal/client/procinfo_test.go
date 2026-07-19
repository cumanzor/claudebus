package client

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestProcArgsSelfParse proves the platform argv reader is correct by reading our
// OWN process and comparing to os.Args (Decision 1 condition i).
func TestProcArgsSelfParse(t *testing.T) {
	got, err := procArgs(os.Getpid())
	if err != nil {
		t.Fatalf("procArgs(self): %v", err)
	}
	if want := strings.Join(os.Args, " "); got != want {
		t.Fatalf("procArgs(self)\n got  %q\n want %q", got, want)
	}
}

func TestProcParentSelf(t *testing.T) {
	comm, ppid, err := procParent(os.Getpid())
	if err != nil {
		t.Fatalf("procParent(self): %v", err)
	}
	if ppid != os.Getppid() {
		t.Errorf("procParent ppid = %d, want %d", ppid, os.Getppid())
	}
	if comm == "" {
		t.Error("procParent comm is empty")
	}
	t.Logf("self: comm=%q ppid=%d", comm, ppid)
}

// TestProcArgsDeadPid: a nonexistent pid errors, so the argv liveness clause
// reads DEAD (reviewer edge D1 — same direction as ps going empty).
func TestProcArgsDeadPid(t *testing.T) {
	if _, err := procArgs(0x7fffffff); err == nil {
		t.Error("procArgs(nonexistent pid) should return an error")
	}
}

// startedChild spawns a long-lived child and returns its pid, killed at test end.
//
// The leading sleep is load-bearing on linux, where starttime is USER_HZ ticks
// (10ms) rather than darwin's microseconds: a child spawned in the same tick as the
// test binary or as a previously-spawned sibling gets a byte-identical token, and
// the ordering and distinctness assertions fail spuriously. Separating the spawns is
// the right fix rather than relaxing those comparisons — a >= ordering would pass on
// a constant wrong field, which is the exact failure the tests exist to catch.
func startedChild(t *testing.T) int {
	t.Helper()
	time.Sleep(50 * time.Millisecond) // ~5 ticks at USER_HZ=100
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

// TestProcStartTimeSelfStable: the token is non-empty and byte-stable across reads.
// Stability is what makes it usable as a recorded witness at all.
func TestProcStartTimeSelfStable(t *testing.T) {
	first, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	if first == "" {
		t.Fatal("procStartTime(self) is empty")
	}
	second, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self) second read: %v", err)
	}
	if first != second {
		t.Errorf("procStartTime(self) not stable: %q then %q", first, second)
	}
	t.Logf("self start token: %q", first)
}

// TestProcStartTimeDistinctPerProcess: two live processes must not share a token.
// This is the property the recycled-pid guard rests on — a field that read the same
// for everyone (a constant at a wrong offset) would still pass the stability test.
func TestProcStartTimeDistinctPerProcess(t *testing.T) {
	self, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	child, err := procStartTime(startedChild(t))
	if err != nil {
		t.Fatalf("procStartTime(child): %v", err)
	}
	if self == child {
		t.Errorf("self and child share start token %q — not an identity witness", self)
	}
}

// TestProcStartTimeDeadPid: an unprobeable pid errors, so the caller reads DEAD
// (R2 — proc-probe failure is dead, distinct from R1's meta-file read leniency).
func TestProcStartTimeDeadPid(t *testing.T) {
	if _, err := procStartTime(0x7fffffff); err == nil {
		t.Error("procStartTime(nonexistent pid) should return an error")
	}
}

// TestProcStartTimeNonPositivePid: pid 0 is the "never armed" sentinel in meta.json
// and must never be probed into an answer.
func TestProcStartTimeNonPositivePid(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := procStartTime(pid); err == nil {
			t.Errorf("procStartTime(%d) should return an error", pid)
		}
	}
}
