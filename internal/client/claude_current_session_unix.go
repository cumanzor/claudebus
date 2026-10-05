//go:build !windows

package client

import (
	"os"
	"syscall"
)

const noFollowOpen = syscall.O_NOFOLLOW

func claudeRegistryInfo(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular() && st.Nlink == 1
}
