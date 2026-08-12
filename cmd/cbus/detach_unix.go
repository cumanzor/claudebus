//go:build darwin || linux

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the child in its own session so it outlives this process — the
// update-check poll must survive the primary command returning (a fast verb can exit
// in 20ms while gh takes ~450ms to answer). Unix-only, matching the release matrix
// (cbus-7sg D25); the build tag is what enforces that, since `unix` is not a GOOS and
// the filename suffix constrains nothing on its own.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
