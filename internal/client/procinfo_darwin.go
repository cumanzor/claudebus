//go:build darwin

package client

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// darwin process inspection WITHOUT spawning ps: argv via sysctl KERN_PROCARGS2,
// parent/comm via the proc_info syscall. Zero ps-spawns (Decision 1a).

const (
	_CTL_KERN       = 1
	_KERN_PROCARGS2 = 49

	// proc_info(2) — <sys/proc_info.h>
	_SYS_proc_info     = 336
	_PROC_CALL_PIDINFO = 2
	_PROC_PIDTBSDINFO  = 3
	// proc_bsdinfo field offsets (stable 64-bit ABI)
	_off_pbi_status = 4  // uint32 pbi_status
	_off_pbi_ppid   = 16 // uint32 pbi_ppid
	_off_pbi_comm   = 48 // char pbi_comm[MAXCOMLEN=16]
	// start time lives past pbi_name[32], pbi_nfiles, pbi_pgid, pbi_pjobc, e_tdev,
	// e_tpgid, pbi_nice. Offsets verified against `ps -o lstart` on live pids rather
	// than trusted from the header layout (returned size is 136).
	_off_pbi_start_tvsec  = 120 // uint64 pbi_start_tvsec
	_off_pbi_start_tvusec = 128 // uint64 pbi_start_tvusec

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

// procStartTime returns pid's start time as an OPAQUE identity token
// ("<tvsec>.<tvusec>"), the structural half of (pid, starttime). Callers compare it
// by byte equality and never parse it as a clock: it is a witness that the pid is
// still the same process, not a timestamp. An error (ESRCH/EPERM, short read) makes
// the caller read the listener DEAD — a probe that cannot answer never answers alive.
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
	if int(r) < _off_pbi_start_tvusec+8 {
		return "", syscall.EINVAL
	}
	sec := binary.LittleEndian.Uint64(buf[_off_pbi_start_tvsec:])
	usec := binary.LittleEndian.Uint64(buf[_off_pbi_start_tvusec:])
	return strconv.FormatUint(sec, 10) + "." + strconv.FormatUint(usec, 10), nil
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
