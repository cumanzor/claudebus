//go:build !windows

package client

import (
	"os"
	"syscall"
)

// O_NONBLOCK keeps a FIFO swapped in for a regular file from hanging the open.
const nonBlockingOpen = syscall.O_NONBLOCK

func privateClaudeCredentialInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0o700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && stat.Nlink == 1
}
