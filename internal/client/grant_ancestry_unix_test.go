//go:build darwin || linux

package client

import (
	"errors"
	"syscall"
	"testing"
)

// a login-shaped tree: zsh under a root-owned login the user cannot inspect.
type fakeProc struct {
	comm string
	ppid int
	err  error
	argv string
}

func lookupOver(procs map[int]fakeProc, ppidOnly func(int) (int, error)) func(int) (procRecord, bool) {
	parent := func(pid int) (string, int, error) {
		p, ok := procs[pid]
		if !ok {
			return "", 0, syscall.ESRCH
		}
		return p.comm, p.ppid, p.err
	}
	args := func(pid int) (string, error) { return procs[pid].argv, nil }
	return grantLookupFrom(parent, args, ppidOnly)
}

func TestGrantWalkCrossesAnUninspectableAncestor(t *testing.T) {
	procs := map[int]fakeProc{
		10: {comm: "zsh", ppid: 9, argv: "-zsh"},
		9:  {err: syscall.EPERM},
		8:  {comm: "iTermServer-3.7", ppid: 1, argv: "iTermServer"},
	}
	kinfo := func(pid int) (int, error) {
		if pid == 9 {
			return 8, nil
		}
		return 0, errors.New("unexpected kinfo read")
	}
	chain, complete := ancestorChain(10, 1, lookupOver(procs, kinfo))
	if !complete || len(chain) != 3 {
		t.Fatalf("an EPERM ancestor with a readable parent must not truncate: %+v complete=%v", chain, complete)
	}
	if id := procIdentity(chain[1]); id != "?(pid 9: operation not permitted)" {
		t.Errorf("the uninspectable ancestor must be recorded as unknown with its errno, got %q", id)
	}
	if h := chainHarness(chain); h != "" {
		t.Errorf("no harness in a plain terminal tree, got %q", h)
	}
}

func TestGrantWalkStillCatchesAHarnessAboveAnUninspectableHop(t *testing.T) {
	procs := map[int]fakeProc{
		10: {comm: "zsh", ppid: 9, argv: "zsh"},
		9:  {err: syscall.EPERM},
		8:  {comm: "2.1.286", ppid: 1, argv: "/u/.local/bin/claude --model m"},
	}
	chain, complete := ancestorChain(10, 1, lookupOver(procs, func(int) (int, error) { return 8, nil }))
	if !complete || chainHarness(chain) != "claude" {
		t.Fatalf("a harness above the EPERM hop must be caught: %+v complete=%v", chain, complete)
	}
}

func TestGrantWalkTruncatesOnOtherFailures(t *testing.T) {
	// the sysctl read would succeed (parent is init), so only the errno check stops it
	other := map[int]fakeProc{10: {comm: "zsh", ppid: 9}, 9: {err: syscall.EINVAL}}
	if _, complete := ancestorChain(10, 1, lookupOver(other, func(int) (int, error) { return 1, nil })); complete {
		t.Error("a non-EPERM lookup failure must truncate")
	}
	perm := map[int]fakeProc{10: {comm: "zsh", ppid: 9}, 9: {err: syscall.EPERM}}
	if _, complete := ancestorChain(10, 1, lookupOver(perm, func(int) (int, error) { return 0, syscall.EINVAL })); complete {
		t.Error("an EPERM ancestor whose parent cannot be read either must truncate")
	}
}

func TestGrantWalkCatchesAHarnessAboveASudoHop(t *testing.T) {
	// sudo runs as root, so its hop is EPERM-shaped to the user; some hosts run sudo
	// without a password, so it must not launder a harness below it
	procs := map[int]fakeProc{
		20: {err: syscall.EPERM},
		19: {comm: "zsh", ppid: 18, argv: "/bin/zsh -c sudo cbus grant"},
		18: {comm: "2.1.286", ppid: 1, argv: "/u/.local/bin/claude"},
	}
	chain, complete := ancestorChain(20, 1, lookupOver(procs, func(pid int) (int, error) { return 19, nil }))
	if !complete || chainHarness(chain) != "claude" {
		t.Fatalf("a harness above a sudo hop must be caught: %+v complete=%v", chain, complete)
	}
}

func TestGrantWalkTruncatesOnACycle(t *testing.T) {
	procs := map[int]fakeProc{10: {comm: "a", ppid: 11}, 11: {comm: "b", ppid: 10}}
	chain, complete := ancestorChain(10, 1, lookupOver(procs, nil))
	if complete || len(chain) != 2 {
		t.Fatalf("a cycle must truncate at the first revisit: %d hops complete=%v", len(chain), complete)
	}
}

func TestGrantWalkTruncatesAtTheDepthCap(t *testing.T) {
	procs := map[int]fakeProc{}
	for pid := 100; pid < 100+maxWalkDepth+4; pid++ {
		procs[pid] = fakeProc{comm: "sh", ppid: pid + 1}
	}
	if chain, complete := ancestorChain(100, 1, lookupOver(procs, nil)); complete || len(chain) != maxWalkDepth {
		t.Fatalf("a chain deeper than the cap must truncate at the cap: %d hops complete=%v", len(chain), complete)
	}
}
