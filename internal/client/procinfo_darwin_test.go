//go:build darwin

package client

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

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
