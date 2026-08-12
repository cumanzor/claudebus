package client

import (
	"os"
	"strings"
	"testing"
)

// TestImageBaseNormalizes pins the normalization the shared harness matcher depends on.
// isHarnessComm matches "claude" exactly and "claude-" by prefix, so an unstripped
// "claude.exe" would miss on every join and every arm — silently, since a missed match
// just yields an empty harness name.
func TestImageBaseNormalizes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`claude.exe`, "claude"},
		{`C:\Users\cuman\.local\bin\claude.exe`, "claude"},
		{`C:\Users\cuman\.local\bin\CLAUDE.EXE`, "CLAUDE"},
		{`claude-dev.exe`, "claude-dev"},
		{`codex.exe`, "codex"},
		{`pwsh.exe`, "pwsh"},
		{`claude`, "claude"},
		{`.exe`, ".exe"}, // a name that IS the extension keeps it; stripping yields nothing
		{`a/b/node.exe`, "node"},
	}
	for _, c := range cases {
		if got := imageBase(c.in); got != c.want {
			t.Errorf("imageBase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestImageBaseFeedsTheHarnessMatcher closes the loop the normalization exists for:
// the value procParent hands back must satisfy isHarnessComm, not merely look tidy.
func TestImageBaseFeedsTheHarnessMatcher(t *testing.T) {
	for _, name := range []string{`C:\Users\cuman\.local\bin\claude.exe`, `claude.exe`, `codex.exe`} {
		if base := commBase(imageBase(name)); !isHarnessComm(base) {
			t.Errorf("%q normalized to %q, which isHarnessComm rejects", name, base)
		}
	}
}

// TestPidAliveSelf: the only liveness case reachable without a process fixture. The
// terminated-process case and the exit-code-259 case that separates WaitForSingleObject
// from GetExitCodeProcess both need a spawned child, which belongs to the platform test
// fixture seam and is not part of this milestone.
func TestPidAliveSelf(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("pidAlive(self) = false")
	}
	for _, pid := range []int{0, -1, -4242} {
		if pidAlive(pid) {
			t.Errorf("pidAlive(%d) = true, want false", pid)
		}
	}
}

// TestProcStartTimeSelfIsStable: the witness must be a fixed property of the process,
// not something that drifts between reads — two probes of the same pid that disagree
// would make every re-arm read its own listener dead.
func TestProcStartTimeSelfIsStable(t *testing.T) {
	first, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	if first == "" {
		t.Fatal("procStartTime(self) returned an empty token with no error")
	}
	second, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self) second read: %v", err)
	}
	if first != second {
		t.Errorf("witness drifted between reads: %q then %q", first, second)
	}
}

func TestProcStartTimeRejectsNonPositivePids(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := procStartTime(pid); err == nil {
			t.Errorf("procStartTime(%d) returned no error", pid)
		}
	}
}

// TestProcParentSelfNormalized checks both returns against facts the runtime already
// knows, and that the image name came back in the shape the shared matcher expects.
func TestProcParentSelfNormalized(t *testing.T) {
	comm, ppid, err := procParent(os.Getpid())
	if err != nil {
		t.Fatalf("procParent(self): %v", err)
	}
	if comm == "" {
		t.Error("procParent(self) returned an empty image name")
	}
	if strings.Contains(comm, `\`) || strings.HasSuffix(strings.ToLower(comm), ".exe") {
		t.Errorf("procParent(self) = %q: not normalized (path or extension survived)", comm)
	}
	if ppid != os.Getppid() {
		t.Errorf("procParent(self) ppid = %d, runtime says %d", ppid, os.Getppid())
	}
}

func TestProcParentRejectsNonPositivePids(t *testing.T) {
	if _, _, err := procParent(-1); err == nil {
		t.Error("procParent(-1) returned no error")
	}
}

// TestProcArgsIsAnHonestRefusal: an ERROR, never ("", nil). The argv liveness clause
// reads a failed argv read as DEAD; an empty string with no error would read as a live
// process that merely has no arguments.
func TestProcArgsIsAnHonestRefusal(t *testing.T) {
	got, err := procArgs(os.Getpid())
	if err == nil {
		t.Fatalf("procArgs(self) = %q with no error; it must refuse explicitly", got)
	}
	if got != "" {
		t.Errorf("procArgs(self) returned %q alongside its error", got)
	}
}

// TestProcZombieIsAlwaysFalse pins the documented decision. It is false because the
// state it would catch — a terminated process whose object is retained by an open
// handle — is already read dead by pidAlive, not because windows lacks the state.
func TestProcZombieIsAlwaysFalse(t *testing.T) {
	if procZombie(os.Getpid()) {
		t.Error("procZombie(self) = true")
	}
	if procZombie(-1) {
		t.Error("procZombie(-1) = true")
	}
}

// TestProcLookupFindsSelfAndFillsCreation exercises the snapshot path end to end: the
// walk's lookup must find this process, agree with the runtime about its parent, and
// supply the creation ordering value the parent-age check needs. A lookup that returned
// records with Created=0 would make that check inert on the one platform it exists for.
func TestProcLookupFindsSelfAndFillsCreation(t *testing.T) {
	lookup := procLookup()
	rec, ok := lookup(os.Getpid())
	if !ok {
		t.Fatal("procLookup missed this process")
	}
	if rec.PPid != os.Getppid() {
		t.Errorf("lookup ppid = %d, runtime says %d", rec.PPid, os.Getppid())
	}
	if rec.Created == 0 {
		t.Error("lookup left Created zero: the parent-age check would be inert")
	}
	if rec.Comm == "" {
		t.Error("lookup returned an empty image name")
	}
	if _, ok := lookup(-1); ok {
		t.Error("procLookup claimed to find pid -1")
	}
}
