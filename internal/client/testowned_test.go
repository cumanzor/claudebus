package client

import "sync"

// testOwnedPids holds every process this test binary started, so close can be
// confined to them: a broken identity guard must never reach a real session.
var testOwnedPids sync.Map

func ownTestPid(pid int) { testOwnedPids.Store(pid, true) }

func isTestOwned(pid int) bool {
	_, ok := testOwnedPids.Load(pid)
	return ok
}
