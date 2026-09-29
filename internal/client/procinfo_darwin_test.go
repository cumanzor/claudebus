//go:build darwin

package client

import (
	"encoding/binary"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// TestProcStartTimeWrapperFeedsComposer closes the seam B1 opened: the composer is
// unit-tested on synthetic bytes and the syscall is exercised on a live pid, but
// nothing yet proved the wrapper hands the composer the RIGHT bytes. A wrapper that
// passed a wrong slice bound would still produce a plausible stable token.
func TestProcStartTimeWrapperFeedsComposer(t *testing.T) {
	var buf [256]byte
	r, _, errno := syscall.Syscall6(_SYS_proc_info,
		_PROC_CALL_PIDINFO, uintptr(os.Getpid()), _PROC_PIDTBSDINFO, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		t.Fatalf("proc_pidinfo(self): %v", errno)
	}
	want, err := darwinStartToken(buf[:r])
	if err != nil {
		t.Fatalf("darwinStartToken: %v", err)
	}
	got, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	if got != want {
		t.Errorf("procStartTime = %q but composer over the same bytes = %q", got, want)
	}
}

// TestProcStartTimeOffsetSanity is the offset check the stability and distinctness
// tests cannot make: a WRONG offset into proc_bsdinfo can still yield a value that is
// stable per-process and differs between processes (a pointer, a pid-derived field),
// so those tests would pass while the identity witness read garbage. Anchoring the
// tvsec component to wall-clock time for a JUST-spawned child is what actually proves
// the offset points at the start time.
//
// Test-only arithmetic. Production treats the token as opaque and compares it by byte
// equality; parsing it here is a property assertion, not a sanctioned use (R2).
func TestProcStartTimeOffsetSanity(t *testing.T) {
	pid := startedChild(t)
	tok, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime(child): %v", err)
	}
	sec, _, ok := strings.Cut(tok, ".")
	if !ok {
		t.Fatalf("token %q is not <tvsec>.<tvusec>", tok)
	}
	started, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		t.Fatalf("tvsec %q is not an integer: %v", sec, err)
	}
	// generous window: the assertion is "this is a recent wall clock", not a precise
	// spawn time. A wrong offset lands astronomically outside it.
	if delta := time.Since(time.Unix(started, 0)); delta < -2*time.Minute || delta > 2*time.Minute {
		t.Errorf("child tvsec %d is %v from now — offset likely wrong (token %q)", started, delta, tok)
	}
}

// rootOwnedParents lists up to three root-owned pids with a parent, as ps reads them.
func rootOwnedParents(t *testing.T) map[int]int {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads every pid; the refusal under test needs an ordinary user")
	}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,uid=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	found := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[2] != "0" || f[1] == "0" || len(found) == 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		found[pid], _ = strconv.Atoi(f[1])
	}
	if len(found) == 0 {
		t.Fatal("precondition: ps listed no root-owned process with a parent")
	}
	return found
}

// proc_info refuses a root-owned pid to an ordinary user; the ancestry walk
// crosses one (login) in a session started through /usr/bin/login.
func TestProcPPIDReadsRootOwnedProcess(t *testing.T) {
	checked := 0
	for pid, want := range rootOwnedParents(t) {
		got, err := procPPID(pid)
		if err == syscall.ESRCH {
			continue // exited since ps ran
		}
		if err != nil || got != want {
			t.Errorf("procPPID(root-owned pid %d) = %d, %v; ps says ppid %d", pid, got, err, want)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("precondition: every listed root-owned process exited before it was read")
	}
	if got, err := procPPID(os.Getpid()); err != nil || got != os.Getppid() {
		t.Errorf("procPPID(self) = %d, %v; want %d", got, err, os.Getppid())
	}
}

// the walk reads through the seam, so the seam itself must read a root-owned step
func TestAncestryParentReadsRootOwnedProcess(t *testing.T) {
	checked := 0
	for pid, want := range rootOwnedParents(t) {
		got, err := ancestryParent(pid)
		if err == syscall.ESRCH {
			continue // exited since ps ran
		}
		if err != nil || got != want {
			t.Errorf("ancestryParent(root-owned pid %d) = %d, %v; ps says ppid %d", pid, got, err, want)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("precondition: every listed root-owned process exited before it was read")
	}
}

// sysctl answers a missing pid with success and no data; a ppid of 0 there
// would end the ancestry walk as "not an ancestor".
func TestProcPPIDMissingPidIsAnError(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	gone := cmd.Process.Pid
	if got, err := procPPID(gone); err == nil {
		t.Fatalf("procPPID(exited pid %d) = %d with no error", gone, got)
	}
}

// only an empty reply means the pid is gone; any other wrong size is a layout
// mismatch and must say so rather than read as "no such process"
func TestKinfoRecordPPIDSizes(t *testing.T) {
	buf := make([]byte, _sizeof_kinfo_proc)
	binary.LittleEndian.PutUint32(buf[_off_kp_e_ppid:], 4242)
	if got, err := kinfoRecordPPID(buf, _sizeof_kinfo_proc); err != nil || got != 4242 {
		t.Errorf("whole record = %d, %v; want 4242", got, err)
	}
	if _, err := kinfoRecordPPID(buf, 0); err != syscall.ESRCH {
		t.Errorf("empty reply = %v; want ESRCH", err)
	}
	for _, size := range []uintptr{4, _sizeof_kinfo_proc - 8, _sizeof_kinfo_proc + 8} {
		_, err := kinfoRecordPPID(buf, size)
		if err == nil || err == syscall.ESRCH || !strings.Contains(err.Error(), strconv.Itoa(int(size))+" bytes") {
			t.Errorf("%d-byte reply = %v; want a layout error naming the size", size, err)
		}
	}
}
