package client

import "sync"

// testOwnedPids maps each process this test binary started to its start token,
// so close can be confined to them: a broken identity guard must never reach a
// real session, and a recycled pid is not the process the tests started.
var testOwnedPids sync.Map

func ownTestPid(pid int) {
	if start, err := procStartTime(pid); err == nil {
		testOwnedPids.Store(pid, start)
	}
}

func disownTestPid(pid int) { testOwnedPids.Delete(pid) }

func isTestOwned(pid int) bool {
	start, ok := testOwnedPids.Load(pid)
	if !ok {
		return false
	}
	current, err := procStartTime(pid)
	return err == nil && current == start.(string)
}
