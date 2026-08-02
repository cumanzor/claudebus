package client

import (
	"os"
	"syscall"
)

// File identity for the cursor and the rotation check. NTFS answers the same question
// (dev, ino) asks on unix: dwVolumeSerialNumber identifies the volume and the 64-bit
// file index identifies the file on it. Both fit the uint64 pair the cursor already
// persists, so nothing about the on-disk format changes. Size travels with the
// identity because GetFileInformationByHandle returns it in the same call.

// fileIdentity is the identity of the file at path.
//
// It goes through openSharedRead rather than os.Open, and the share mode is the whole
// reason: a stdlib handle blocks deletion of its file while held, which turns an
// identity PROBE into a lock. The mask and the argument for it live in
// openshared_windows.go, so the follower's open and this one cannot drift apart (D65).
func fileIdentity(path string) (dev, ino uint64, size int64, ok bool) {
	f, err := openSharedRead(path)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	return fileIdentityOf(f)
}

// fileIdentityOf is the identity of an already-open file. The share mode here belongs to
// whoever opened f, not to this call — so a caller that opened with os.Open is holding a
// deletion-blocking handle regardless of what this function does with it.
func fileIdentityOf(f *os.File) (dev, ino uint64, size int64, ok bool) {
	return handleIdentity(syscall.Handle(f.Fd()))
}

func handleIdentity(h syscall.Handle) (dev, ino uint64, size int64, ok bool) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return 0, 0, 0, false
	}
	dev = uint64(info.VolumeSerialNumber)
	ino = uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	size = int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	return dev, ino, size, true
}
