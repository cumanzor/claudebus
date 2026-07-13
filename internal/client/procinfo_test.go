package client

import (
	"os"
	"strings"
	"testing"
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
