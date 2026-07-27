//go:build darwin || linux

package client

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcGroup puts cmd in its own process group, so killProcGroup reaches the
// descendants a bare child-directed kill would leave holding the inherited pipes.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcGroup SIGKILLs the whole group p leads.
func killProcGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}
