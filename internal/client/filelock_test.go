package client

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// File-lock seam tests. Untagged on purpose: every case here is a property the seam must
// hold on BOTH platforms, and the whole point of the sentinel is that the caller cannot
// tell which one it is running on. These are the LAPTOP HALVES — they exercise two
// handles inside one process. The two-PROCESS exclusion case and the real
// holder-dies-without-unlocking case need a process boundary and belong to the logos
// gate, not here.

func openLockFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// TestContentionMapsToTheSharedSentinel is the gate-5 case and the one most worth having.
// A platform that reported contention as its own raw errno would still make this file
// compile and would still lock correctly; what it would break is the caller's ability to
// TELL contention from a real error, and the caller expresses that question only through
// errLockContended. Asserting the sentinel rather than "some error" is the whole test:
// an unmapped errno is also a non-nil error and would satisfy a weaker assertion.
func TestContentionMapsToTheSharedSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mint")
	a := openLockFile(t, path)
	b := openLockFile(t, path)

	if err := tryLockExclusive(a); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	err := tryLockExclusive(b)
	if err == nil {
		t.Fatal("second handle acquired a lock the first already holds")
	}
	if !errors.Is(err, errLockContended) {
		t.Fatalf("contention returned %v, which errors.Is(errLockContended) rejects; the retry loop would read this as an unexpected error and give up on the first contended try", err)
	}
}

// TestUnlockAllowsReacquire: the lock is released by the orderly path.
func TestUnlockAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mint")
	a := openLockFile(t, path)
	b := openLockFile(t, path)

	if err := tryLockExclusive(a); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := unlockFile(a); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}
	if err := tryLockExclusive(b); err != nil {
		t.Fatalf("acquire after unlock failed: %v", err)
	}
}

// TestCloseWithoutUnlockReleases is the laptop half of crash-release, and it is a PROXY:
// it proves the kernel drops the lock when the handle closes WITHOUT an unlock call,
// which is the mechanism process death relies on, since death is a bulk close. It does
// NOT prove release on process death — that needs a process boundary and is the tester's
// half. Named as a proxy so nobody reads it as the real thing.
func TestCloseWithoutUnlockReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mint")
	a, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := tryLockExclusive(a); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	a.Close() // deliberately NO unlockFile

	b := openLockFile(t, path)
	if err := tryLockExclusive(b); err != nil {
		t.Fatalf("lock survived the holder's handle closing: %v", err)
	}
}

// TestAcquireMintLockExcludesThenAdmits drives the real verb rather than the seam, so the
// retry loop and the sentinel comparison are both exercised through the door a caller
// uses. It deliberately does not wait out the full patience: the point is that a
// contended acquire STAYS blocked while held and succeeds once released, which is
// observable in milliseconds.
func TestAcquireMintLockExcludesThenAdmits(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())

	release, ok := acquireMintLock("ch")
	if !ok {
		t.Fatal("first acquireMintLock failed")
	}

	got := make(chan bool, 1)
	go func() {
		r2, ok2 := acquireMintLock("ch")
		if ok2 {
			r2()
		}
		got <- ok2
	}()

	// Two different defects both return early here and they must not share a message:
	// ok=true means exclusion is broken outright, ok=false means the retry loop gave up
	// on the first contended try, which is what an unmapped contention errno causes.
	select {
	case ok2 := <-got:
		if ok2 {
			t.Fatal("a second acquireMintLock SUCCEEDED while the lock was held: exclusion is broken")
		}
		t.Fatal("a second acquireMintLock returned ok=false while the lock was held: it gave up instead of retrying, which is what contention not mapping to errLockContended does")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case ok2 := <-got:
		if !ok2 {
			t.Error("acquireMintLock did not succeed after the holder released")
		}
	case <-time.After(4 * time.Second):
		t.Error("acquireMintLock never returned after the holder released")
	}
}
