package main

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
)

// the launching shell's job object (sshd, a terminal, a harness) kills its
// members when it closes, so the daemon leaves the job where the job allows it.
// a job without JOB_OBJECT_LIMIT_BREAKAWAY_OK refuses the flag outright.
func detachDaemon(newCmd func() *exec.Cmd) error {
	var err error
	for _, flags := range []uint32{
		createNewProcessGroup | detachedProcess | createBreakawayFromJob,
		createNewProcessGroup | detachedProcess,
	} {
		cmd := newCmd()
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err = cmd.Start(); err == nil {
			_ = cmd.Process.Release()
			return nil
		}
	}
	return err
}
