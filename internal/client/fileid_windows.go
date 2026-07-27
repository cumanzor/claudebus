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
// It opens with a hand-rolled CreateFile rather than os.Open, and the share mode is the
// whole reason. Go's os.Open passes FILE_SHARE_READ|FILE_SHARE_WRITE and NOTHING ELSE
// (syscall_windows.go, `sharemode := uint32(FILE_SHARE_READ | FILE_SHARE_WRITE)`), so a
// handle it returns BLOCKS DELETION of the file for as long as it is held. That turns an
// identity PROBE into a lock: a rejoin's rm+recreate of the inbox fails with "being used
// by another process" while any probe handle is open. Windows needs FILE_SHARE_DELETE
// named explicitly, and no stdlib open helper names it, so the call has to be made here.
//
// Verified rather than assumed, because the previous version of this comment asserted
// that os.Open passed all three flags, and it does not.
func fileIdentity(path string) (dev, ino uint64, size int64, ok bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, 0, false
	}
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return 0, 0, 0, false
	}
	defer syscall.CloseHandle(h)
	return handleIdentity(h)
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
