package client

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Gate 4 is the TWO-PROCESS half of the mint lock (cbus-que.2): exclusion, non-blocking,
// and crash-release when the holder dies without unlocking. One process cannot prove
// crash-release against itself, so the holder is a real second process — a self-re-exec
// of this test binary (D39), mirroring TestReclaimLockDiesWithItsHolder. The subject is
// the primitive tryLockExclusive (flock on unix, LockFileEx on windows): acquireMintLock
// spin-retries ~4s, so only the primitive shows the immediate contended signal. On darwin
// these exercise flock; the LockFileEx half runs on windows from the candidate binary.

// gate4Held pins the holder child's lock closure (and the *os.File it captures) for the
// process lifetime. Without a live reference the os.File finalizer could close the fd and
// drop the lock before the parent's kill, making crash-release vacuous.
var gate4Held func()

func mintPath(ch string) string       { return filepath.Join(ledgerRoot(), "."+ch+".mint") }
func mintReadyMarker(ch string) string { return mintPath(ch) + ".held" }

// TestMintLockGate4Child is the holder half, env-guarded so it is inert in a normal run
// (it joins the reason-carrying skip table). Driven only by the two drivers below, which
// re-exec this binary with CBUS_GATE4_HOLDER set. The -test.run they pass is anchored, so
// the "Child" name must not be a prefix of a driver name or the child would run a driver
// and fork.
func TestMintLockGate4Child(t *testing.T) {
	if os.Getenv("CBUS_GATE4_HOLDER") == "" {
		t.Skip("subprocess holder for the Gate 4 two-process mint-lock tests; driven by " +
			"TestMintLockGate4Exclusion and TestMintLockGate4HolderDies via self-re-exec")
	}
	ch := os.Getenv("CBUS_GATE4_CHANNEL")
	release, ok := acquireMintLock(ch)
	if !ok {
		t.Fatalf("holder child could not take a free mint lock for %q", ch)
	}
	gate4Held = release // pin: never released here — crash-release is the property, the parent SIGKILLs us
	if err := os.WriteFile(mintReadyMarker(ch), nil, 0o644); err != nil {
		t.Fatalf("holder child could not write its ready marker: %v", err)
	}
	select {} // block until killed; a normal return would Close and release, making crash-release vacuous
}

func startHolder(t *testing.T, ch string) *exec.Cmd {
	t.Helper()
	// Go argv, no shell: the anchored -test.run runs ONLY the child, never a driver.
	cmd := exec.Command(os.Args[0], "-test.run=^TestMintLockGate4Child$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), "CBUS_GATE4_HOLDER=1", "CBUS_GATE4_CHANNEL="+ch)
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the holder child: %v", err)
	}
	return cmd
}

// waitForMarker fails on its own deadline, so a child that never acquires reds HERE as a
// start/acquire failure rather than wedging the run or masquerading as an exclusion result.
func waitForMarker(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !fileExists(path) {
		if time.Now().After(deadline) {
			t.Fatalf("holder child never reported holding the lock (no marker at %s within %s): "+
				"a start/acquire failure, not an exclusion result", path, within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// tryLockBounded runs the single non-blocking attempt under a timer, so a blocking-lock
// mutation reds the timer as its own outcome instead of wedging the whole binary.
func tryLockBounded(f *os.File, bound time.Duration) (err error, timedOut bool) {
	done := make(chan error, 1)
	go func() { done <- tryLockExclusive(f) }()
	select {
	case e := <-done:
		return e, false
	case <-time.After(bound):
		return nil, true
	}
}

// TestMintLockGate4Exclusion: while a second process holds the lock, this process's single
// tryLockExclusive must return errLockContended (exclusion) IMMEDIATELY (non-blocking), and
// the three outcomes stay distinct — acquired(nil), contended(errLockContended), other-error
// — with the timer as a fourth. The 50ms bound is the logos-measured basis (25x the ~2ms
// default-timer tick); no timeBeginPeriod anywhere in this process.
func TestMintLockGate4Exclusion(t *testing.T) {
	setupStore(t)
	const ch = "gate4x"
	cmd := startHolder(t, ch)
	defer func() { _ = cmd.Process.Kill() }() // harness-tracked pid only
	waitForMarker(t, mintReadyMarker(ch), 20*time.Second)

	// contender opens its own fd on the production path with production flags, AFTER the hold is observed
	f, err := os.OpenFile(mintPath(ch), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("contender could not open the mint file: %v", err)
	}
	defer f.Close()

	lockErr, timedOut := tryLockBounded(f, 50*time.Millisecond)
	switch {
	case timedOut:
		t.Fatalf("NON-BLOCKING failed: contender BLOCKED past 50ms while the holder held " +
			"(outcome=blocked/timer); want immediate errLockContended " +
			"[outcomes: acquired(nil) / contended(errLockContended) / other-error / blocked(timer)]")
	case lockErr == nil:
		t.Fatalf("EXCLUSION failed: contender ACQUIRED the lock (outcome=acquired/nil) while another " +
			"process held it [outcomes: acquired(nil) / contended(errLockContended) / other-error / blocked(timer)]")
	case !errors.Is(lockErr, errLockContended):
		t.Fatalf("DISTINCTNESS failed: contender got an UNEXPECTED error (outcome=other): %v; want "+
			"errLockContended, distinct from acquired(nil) and blocked(timer)", lockErr)
	}
	// contended AND within 50ms: exclusion + non-blocking proven, all outcomes separated
}

// TestMintLockGate4HolderDies: the holder is SIGKILLed while holding, with no unlock and no
// Close on its side, and a fresh contender must then acquire — proof the kernel drops the
// lock on process death. The contended-while-ALIVE check is an AIMED assertion, not a
// precondition: without it a lock that never excludes would trivially "acquire after death"
// and the crash-release claim would be vacuous (same shape asserted at launch_intent_test.go
// :310-312). The alive check is a raw synchronous attempt: a blocking-lock mutation wedges
// it here and the per-test -test.timeout bounds that, the pre-registered timing-mutant signal.
func TestMintLockGate4HolderDies(t *testing.T) {
	setupStore(t)
	const ch = "gate4d"
	cmd := startHolder(t, ch)
	killed := false
	defer func() {
		if !killed {
			_ = cmd.Process.Kill() // harness-tracked pid only
		}
	}()
	waitForMarker(t, mintReadyMarker(ch), 20*time.Second)

	f, err := os.OpenFile(mintPath(ch), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("contender could not open the mint file: %v", err)
	}
	if e := tryLockExclusive(f); !errors.Is(e, errLockContended) {
		_ = f.Close()
		t.Fatalf("CONTENDED-WHILE-ALIVE failed: want errLockContended while the holder holds, got %v "+
			"(outcome not contended); the crash-release assertion would be vacuous without this", e)
	}
	_ = f.Close()

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("could not SIGKILL the holder child: %v", err)
	}
	killed = true
	_ = cmd.Wait()

	f2, err := os.OpenFile(mintPath(ch), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("post-kill open failed: %v", err)
	}
	defer f2.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		e := tryLockExclusive(f2)
		if e == nil {
			_ = unlockFile(f2) // orderly release; the deferred Close would also drop it
			return             // crash-release proven
		}
		if !errors.Is(e, errLockContended) {
			t.Fatalf("post-kill acquire got an UNEXPECTED error: %v; want acquired(nil) "+
				"[outcomes: acquired(nil) / contended / other]", e)
		}
		if time.Now().After(deadline) {
			t.Fatalf("HOLDER-DIES failed: the lock SURVIVED its holder's death — still errLockContended " +
				"2s after SIGKILL+Wait; the kernel did not crash-release it (the hand-rolled-lock regression)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
