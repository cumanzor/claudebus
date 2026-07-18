package main

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the child in its own session so it outlives this process — the
// update-check poll must survive the primary command returning (a fast verb can exit
// in 20ms while gh takes ~450ms to answer). Unix-only, matching the release matrix
// (cbus-7sg D25); bdx carries the windows variant if one is ever needed.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
