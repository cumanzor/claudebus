//go:build darwin || linux

package client

import (
	"os"
	"syscall"
)

// File identity for the cursor and the rotation check: (dev, ino) plus the size the
// same stat saw. Size travels WITH the identity because every caller needs both and
// a second stat to fetch it would open a window where the two disagree.

// fileIdentity is the identity of the file at path.
func fileIdentity(path string) (dev, ino uint64, size int64, ok bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, false
	}
	return statIdentity(fi)
}

// fileIdentityOf is the identity of an already-open file.
func fileIdentityOf(f *os.File) (dev, ino uint64, size int64, ok bool) {
	fi, err := f.Stat()
	if err != nil {
		return 0, 0, 0, false
	}
	return statIdentity(fi)
}

func statIdentity(fi os.FileInfo) (dev, ino uint64, size int64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), fi.Size(), true
}
