//go:build linux

package client

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestProcStartTimeFieldSanity is the field-index check the stability and distinctness
// tests cannot make: a WRONG index into /proc/<pid>/stat can still yield a value that
// is stable per-process and differs between processes (utime, a memory address), so
// those tests would pass while the identity witness read garbage. starttime is
// monotonic in spawn order, so a child spawned after this test binary MUST carry a
// larger jiffies value. A misindexed field has no reason to obey that.
//
// Test-only arithmetic. Production treats the token as opaque and compares it by byte
// equality; splitting it here is a property assertion, not a sanctioned use (R2).
func TestProcStartTimeFieldSanity(t *testing.T) {
	self := jiffiesOf(t, os.Getpid())
	child := jiffiesOf(t, startedChild(t))
	if child <= self {
		t.Errorf("child starttime %d not after self %d — field index likely wrong", child, self)
	}
}

// TestProcStartTimeCarriesBootID pins the boot-id prefix, the guard against a
// post-reboot pid byte-matching a pre-reboot record (jiffies alone are boot-relative
// and $CBUS_DIR outlives a reboot).
func TestProcStartTimeCarriesBootID(t *testing.T) {
	id := bootID()
	if id == "" {
		t.Skip("no readable boot_id on this kernel — token degrades to jiffies by design")
	}
	tok, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	if !strings.HasPrefix(tok, id+":") {
		t.Errorf("token %q does not carry boot id %q", tok, id)
	}
}

func jiffiesOf(t *testing.T, pid int) uint64 {
	t.Helper()
	tok, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime(%d): %v", pid, err)
	}
	if _, after, ok := strings.Cut(tok, ":"); ok {
		tok = after
	}
	v, err := strconv.ParseUint(tok, 10, 64)
	if err != nil {
		t.Fatalf("jiffies %q is not an integer: %v", tok, err)
	}
	return v
}
