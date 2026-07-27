//go:build darwin || linux

package client

import (
	"os"
	"syscall"
)

// tryLockExclusive takes a non-blocking exclusive flock(2) on f. Contention is mapped to
// the shared errLockContended sentinel; every other error is returned as-is, so the
// caller can spin on the first and refuse to spin on the second.
func tryLockExclusive(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK {
		return errLockContended
	}
	return err
}

// unlockFile drops the lock f holds. Closing f would release it too — the kernel drops
// locks with the open file description — so this is the orderly path, not the only one.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
