//go:build darwin || linux

package client

import (
	"os"
	"syscall"
)

func openClaudeTranscript(path string) (*os.File, error) {
	// A replaced symlink/FIFO must neither redirect nor block caller capture.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// Receipt evidence must not have another writable name or a foreign owner.
// Read-only group/other access is allowed; external write access is not.
func trustedClaudeTranscriptInfo(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func ClaudeConnectIdentity() (ClaudeConnectBinding, error) {
	return claudeConnectIdentity(func() (int, string, error) {
		pid, err := claudeCallerPID(os.Getppid(), procLookup())
		if err != nil {
			return 0, "", err
		}
		start, err := procStartTime(pid)
		return pid, start, err
	})
}
