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
