//go:build darwin || linux

package client

import (
	"fmt"
	"syscall"
)

// grantProcLookup is procLookup plus one fallback: a process this user may not
// inspect (EPERM, such as root-owned login on macOS) still yields its parent via
// sysctl, recorded under an unknown identity, so a plain terminal's walk can reach
// init. Any other failure stays a truncation.
func grantProcLookup() func(int) (procRecord, bool) {
	return grantLookupFrom(procParent, procArgs, procPPID)
}

func grantLookupFrom(parent func(int) (string, int, error), args func(int) (string, error), ppidOnly func(int) (int, error)) func(int) (procRecord, bool) {
	return func(pid int) (procRecord, bool) {
		comm, ppid, err := parent(pid)
		if err == syscall.EPERM {
			pp, perr := ppidOnly(pid)
			if perr != nil {
				return procRecord{}, false
			}
			return procRecord{PPid: pp, Comm: fmt.Sprintf("?(pid %d: %v)", pid, err)}, true
		}
		if err != nil {
			return procRecord{}, false
		}
		argv, _ := args(pid)
		return procRecord{PPid: ppid, Comm: comm, Argv: argv}, true
	}
}
