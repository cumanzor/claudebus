//go:build darwin || linux

package client

import "syscall"

// procWalkRoot is the pid the ancestor walk stops above. init is pid 1 and is never a
// harness, so the walk never inspects it.
const procWalkRoot = 1

// procLookup reads one process record per pid for the harness walk.
//
// Created is left 0, which makes the walk's parent-age check inert here. That is
// correct rather than unimplemented: unix reparents an orphan to init, so a ppid always
// names a live process and the walk cannot follow a dangling link into a foreign tree.
// The hazard the check exists for does not exist on this platform.
func procLookup() func(int) (procRecord, bool) {
	return func(pid int) (procRecord, bool) {
		comm, ppid, err := procParent(pid)
		if err != nil {
			return procRecord{}, false
		}
		argv, _ := procArgs(pid) // "" on error: the argv identity clause simply does not match
		return procRecord{PPid: ppid, Comm: comm, Argv: argv}, true
	}
}

// pidAlive is `kill -0`: the process exists (EPERM still means it exists).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
