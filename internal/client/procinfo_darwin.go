//go:build darwin

package client

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// darwin process inspection WITHOUT spawning ps: argv via sysctl KERN_PROCARGS2,
// parent/comm via the proc_info syscall, and the ancestry walk's ppid via sysctl
// KERN_PROC_PID. Zero ps-spawns (Decision 1a).

const (
	_CTL_KERN       = 1
	_KERN_PROCARGS2 = 49
	_KERN_PROC      = 14
	_KERN_PROC_PID  = 1

	// struct kinfo_proc, identical on darwin amd64 and arm64
	_sizeof_kinfo_proc = 648
	_off_kp_e_ppid     = 560 // kp_eproc.e_ppid

	// proc_info(2) — <sys/proc_info.h>
	_SYS_proc_info     = 336
	_PROC_CALL_PIDINFO = 2
	_PROC_PIDTBSDINFO  = 3
	// proc_bsdinfo field offsets (stable 64-bit ABI)
	_off_pbi_status = 4  // uint32 pbi_status
	_off_pbi_ppid   = 16 // uint32 pbi_ppid
	_off_pbi_comm   = 48 // char pbi_comm[MAXCOMLEN=16]

	_SZOMB = 5 // process status: zombie (<sys/proc.h>)
)

// procArgs returns pid's argv joined by spaces (as `ps -o args=` renders it),
// read via sysctl KERN_PROCARGS2. ESRCH/EPERM/EINVAL are returned so the argv
// liveness clause reads DEAD. ZOMBIE handling (F1): on current kernels a real
// zombie makes KERN_PROCARGS2 itself EINVAL (its args are reclaimed), so the read
// naturally fails and the clause reads dead — matching bash's "<defunct>" and
// linux's empty /proc/cmdline. procZombie is a fail-open belt-and-braces hedge for
// any kernel/window where the argv read might still succeed for a not-yet-reaped
// process; in practice it never fires (see its note). The two-call probe handles
// the KERN_ARGMAX-bounded buffer sizing.
func procArgs(pid int) (string, error) {
	if procZombie(pid) {
		return "", syscall.ESRCH
	}
	mib := [3]int32{_CTL_KERN, _KERN_PROCARGS2, int32(pid)}
	var size uintptr
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3, 0, uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return "", errno
	}
	if size < 4 {
		return "", syscall.ESRCH
	}
	buf := make([]byte, size)
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return "", errno
	}
	buf = buf[:size]
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	p := buf[4:]
	// skip the exec path (null-terminated) then its null padding
	if i := bytes.IndexByte(p, 0); i >= 0 {
		p = p[i:]
	}
	for len(p) > 0 && p[0] == 0 {
		p = p[1:]
	}
	args := make([]string, 0, argc)
	for n := 0; n < argc && len(p) > 0; n++ {
		i := bytes.IndexByte(p, 0)
		if i < 0 {
			args = append(args, string(p))
			break
		}
		args = append(args, string(p[:i]))
		p = p[i+1:]
	}
	return strings.Join(args, " "), nil
}

// procStartTime reads pid's proc_bsdinfo and hands the raw bytes to the shared
// composer. Syscall wrapper only: the token's format lives in starttime.go so the
// writer and the prober cannot drift. An error (ESRCH/EPERM, short read) makes the
// caller read the listener DEAD — a probe that cannot answer never answers alive.
func procStartTime(pid int) (string, error) {
	if pid <= 0 {
		return "", syscall.ESRCH
	}
	var buf [256]byte
	r, _, errno := syscall.Syscall6(_SYS_proc_info,
		_PROC_CALL_PIDINFO, uintptr(pid), _PROC_PIDTBSDINFO, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", errno
	}
	return darwinStartToken(buf[:r])
}

// procParent returns pid's command accounting name (p_comm, as `ps -o comm=`) and
// parent pid, via proc_pidinfo(PROC_PIDTBSDINFO). Error on ESRCH/EPERM.
func procParent(pid int) (comm string, ppid int, err error) {
	var buf [256]byte // >= sizeof(struct proc_bsdinfo)
	r, _, errno := syscall.Syscall6(_SYS_proc_info,
		_PROC_CALL_PIDINFO, uintptr(pid), _PROC_PIDTBSDINFO, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", 0, errno
	}
	if int(r) < _off_pbi_comm+16 {
		return "", 0, syscall.EINVAL
	}
	ppid = int(binary.LittleEndian.Uint32(buf[_off_pbi_ppid:]))
	c := buf[_off_pbi_comm : _off_pbi_comm+16]
	if i := bytes.IndexByte(c, 0); i >= 0 {
		c = c[:i]
	}
	return string(c), ppid, nil
}

// kinfoLayoutOK is set once the hand-rolled kinfo_proc layout has read this
// process's own ppid correctly; until then every read rechecks, so a moved
// field fails closed and a reparenting race is not cached as a failure.
var kinfoLayoutOK atomic.Bool

func kinfoLayoutErr() error {
	if kinfoLayoutOK.Load() {
		return nil
	}
	got, err := kinfoPPID(os.Getpid())
	if err != nil {
		return fmt.Errorf("kinfo_proc layout check: %w", err)
	}
	if want := os.Getppid(); got != want {
		return fmt.Errorf("kinfo_proc layout check: e_ppid reads %d, runtime ppid is %d", got, want)
	}
	kinfoLayoutOK.Store(true)
	return nil
}

// procPPID returns pid's parent via sysctl KERN_PROC_PID. Unlike proc_info it
// reads a root-owned process (login) for an ordinary user.
func procPPID(pid int) (int, error) {
	if err := kinfoLayoutErr(); err != nil {
		return 0, err
	}
	return kinfoPPID(pid)
}

// kinfoPPID: a missing pid comes back as success with no data, so anything
// but a whole record is ESRCH.
func kinfoPPID(pid int) (int, error) {
	mib := [4]int32{_CTL_KERN, _KERN_PROC, _KERN_PROC_PID, int32(pid)}
	var buf [_sizeof_kinfo_proc]byte
	size := uintptr(len(buf))
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 4, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return 0, errno
	}
	if size != _sizeof_kinfo_proc {
		return 0, syscall.ESRCH
	}
	return int(int32(binary.LittleEndian.Uint32(buf[_off_kp_e_ppid:]))), nil
}

// procZombie reports whether pid is a zombie (pbi_status == SZOMB). It is a
// fail-open hedge: PROC_PIDTBSDINFO itself errors for a real zombie, so this
// returns false and never actually fires in practice — the KERN_PROCARGS2 EINVAL
// in procArgs is the real guard. Kept as belt-and-braces for any kernel/window
// where the info read might still succeed for a not-yet-reaped process.
func procZombie(pid int) bool {
	var buf [256]byte
	r, _, errno := syscall.Syscall6(_SYS_proc_info,
		_PROC_CALL_PIDINFO, uintptr(pid), _PROC_PIDTBSDINFO, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 || int(r) < _off_pbi_status+4 {
		return false
	}
	return binary.LittleEndian.Uint32(buf[_off_pbi_status:]) == _SZOMB
}
