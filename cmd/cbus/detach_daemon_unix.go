//go:build !windows

package main

import "os/exec"

func detachDaemon(newCmd func() *exec.Cmd) error {
	cmd := newCmd()
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}
