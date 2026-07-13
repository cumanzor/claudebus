//go:build darwin

package client

import (
	"bytes"
	"encoding/binary"
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
	_off_pbi_ppid = 16
	_off_pbi_comm = 48 // char pbi_comm[MAXCOMLEN=16]
)

// procArgs returns pid's argv joined by spaces (as `ps -o args=` renders it),
// read via sysctl KERN_PROCARGS2. Any error — ESRCH/EPERM, or a ZOMBIE whose
// args the kernel has reclaimed (KERN_PROCARGS2 errors while kill -0 still
// succeeds) — is returned so the argv liveness clause reads DEAD, matching
// `ps -o args=` going empty (reviewer edge D1). The two-call probe handles the
// KERN_ARGMAX-bounded buffer sizing.
func procArgs(pid int) (string, error) {
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
