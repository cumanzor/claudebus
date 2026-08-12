package client

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx and UnlockFileEx are absent from package syscall, so they are reached
// through kernel32 directly rather than by taking a dependency: the module declares zero
// of them and there is no go.sum. NewLazyDLL defers the load until the first call.
var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	_LOCKFILE_FAIL_IMMEDIATELY = 0x00000001
	_LOCKFILE_EXCLUSIVE_LOCK   = 0x00000002

	// ERROR_LOCK_VIOLATION is what LockFileEx reports when FAIL_IMMEDIATELY finds the
	// range already held. Absent from package syscall, so it is declared here.
	_ERROR_LOCK_VIOLATION = syscall.Errno(33)
)

// lockAllBytes locks the maximum range rather than a byte. Windows locks a REGION, not a
// file, so exclusion only holds between callers naming the same region — locking
// everything removes the chance of a future caller picking a different one and silently
// not excluding anything.
const lockAllBytes = ^uint32(0)

// tryLockExclusive takes a non-blocking exclusive LockFileEx on f. Contention is mapped
// to the shared errLockContended sentinel; every other error is returned as-is, so the
// caller can spin on the first and refuse to spin on the second.
//
// The return convention is the trap here: Call always hands back a non-nil error holding
// GetLastError, which is Errno(0) — "the operation completed successfully" — on success.
// Reading that error without checking r1 first would treat every acquire as a failure.
func tryLockExclusive(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procLockFileEx.Call(
		uintptr(syscall.Handle(f.Fd())),
		uintptr(_LOCKFILE_EXCLUSIVE_LOCK|_LOCKFILE_FAIL_IMMEDIATELY),
		0, // reserved, must be zero
		uintptr(lockAllBytes),
		uintptr(lockAllBytes),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 != 0 {
		return nil
	}
	if errors.Is(err, _ERROR_LOCK_VIOLATION) {
		return errLockContended
	}
	return err
}

// unlockFile drops the lock f holds. Closing f would release it too — the kernel drops
// locks with the handle — so this is the orderly path, not the only one.
func unlockFile(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procUnlockFileEx.Call(
		uintptr(syscall.Handle(f.Fd())),
		0, // reserved, must be zero
		uintptr(lockAllBytes),
		uintptr(lockAllBytes),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 != 0 {
		return nil
	}
	return err
}
