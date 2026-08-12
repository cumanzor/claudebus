package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	swapHelperEnv   = "CBUS_SWAP_HELPER"
	swapHelperReady = "CBUS_SWAP_HELPER_READY"
)

// TestSwapHelperSleep is the child process for TestSwapBinaryReplacesLiveImage, not a
// test of its own: it exists so the loader holds a real handle on the image being
// swapped. It touches its ready file and then idles until the parent kills it.
func TestSwapHelperSleep(t *testing.T) {
	if os.Getenv(swapHelperEnv) != "1" {
		t.Skip("helper process for TestSwapBinaryReplacesLiveImage, not run directly")
	}
	if err := os.WriteFile(os.Getenv(swapHelperReady), []byte("ready"), 0o644); err != nil {
		t.Fatalf("helper could not signal ready: %v", err)
	}
	time.Sleep(30 * time.Second)
}

// displacedCount reports how many images swapBinary has parked beside exe.
func displacedCount(t *testing.T, exe string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(exe)+displacedSuffix) {
			n++
		}
	}
	return n
}

// TestSwapBinaryReplacesLiveImage is the windows half of S3, and the case the unix
// implementation cannot reach: the target is an executable a live process is running
// from, so MOVEFILE_REPLACE_EXISTING onto it would be denied. The helper child is what
// makes the target loader-held; without it this would only prove a rename over an
// idle file.
func TestSwapBinaryReplacesLiveImage(t *testing.T) {
	dir := t.TempDir() // registered first, so the kill below unwinds before removal

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	live := filepath.Join(dir, "live.exe")
	if err := copyFile(self, live); err != nil {
		t.Fatalf("copy test binary: %v", err)
	}

	ready := filepath.Join(dir, "ready")
	cmd := exec.Command(live, "-test.run=TestSwapHelperSleep")
	cmd.Env = append(os.Environ(), swapHelperEnv+"=1", swapHelperReady+"="+ready)
	// stdout and stderr are left nil (os.DevNull): a pipe nobody drains fills at ~4KB
	// on this platform and stalls the child, which is que.11's mechanism.
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited // the image stays loader-held until the process is reaped
	})

	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper never signalled ready — the target was never loader-held, so this test would prove nothing")
		}
		time.Sleep(25 * time.Millisecond)
	}
	// verify the precondition before reading any result: a helper that already exited
	// leaves an idle file, and the swap below would pass for the wrong reason.
	select {
	case <-exited:
		t.Fatal("helper exited before the swap — the target was not a running image")
	default:
	}

	src := filepath.Join(dir, "new.bin")
	if err := os.WriteFile(src, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(src, live); err != nil {
		t.Fatalf("swap onto a running image must succeed via the move-aside path: %v", err)
	}
	if b, _ := os.ReadFile(live); string(b) != "NEW" {
		t.Errorf("target holds %q, want the new binary", string(b))
	}
	if n := displacedCount(t, live); n != 1 {
		t.Errorf("displaced images beside the target = %d, want exactly 1", n)
	}
	// the process must still be running out of the displaced image; that it survives is
	// the property that makes this shape usable at all.
	select {
	case <-exited:
		t.Error("the helper died when its image was moved aside — the process must keep executing from the moved file")
	default:
	}
}

// TestSwapBinaryRollsBackOnFailure covers the failure half of S3 on windows: once dst
// has been vacated, a failure to place the new binary must put the original back.
// The target here is idle on purpose — this exercises rollback, not liveness.
func TestSwapBinaryRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "cbus.exe")
	if err := os.WriteFile(dst, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(filepath.Join(dir, "nonexistent"), dst); err == nil {
		t.Fatal("a swap from a missing src must error")
	}
	if b, _ := os.ReadFile(dst); string(b) != "OLD" {
		t.Errorf("target holds %q after a failed swap, want the original restored", string(b))
	}
	if n := displacedCount(t, dst); n != 0 {
		t.Errorf("displaced images left after rollback = %d, want 0", n)
	}
}

// TestCleanDisplacedRemovesOnlyDisplaced pins the cleanup to its own artifacts. The
// unrelated files are the discriminating input: a cleanup that removed the directory
// contents wholesale would pass without them.
func TestCleanDisplacedRemovesOnlyDisplaced(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "cbus.exe")
	keep := []string{exe, filepath.Join(dir, "unrelated.exe"), filepath.Join(dir, "cbus.exe.new.7")}
	for _, p := range keep {
		if err := os.WriteFile(p, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{exe + displacedSuffix + "111", exe + displacedSuffix + "222"} {
		if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if stuck := cleanDisplaced(exe); len(stuck) != 0 {
		t.Errorf("reported %v as unremovable, want nothing stuck", stuck)
	}

	if n := displacedCount(t, exe); n != 0 {
		t.Errorf("displaced images remaining = %d, want 0", n)
	}
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("cleanup removed %s, which is not a displaced image", filepath.Base(p))
		}
	}
}

// TestSelfupdateReportsStuckDisplaced pins the D27 WIRING, which is a different claim
// from TestCleanDisplacedReportsStuck: that one proves cleanDisplaced RETURNS stuck
// names, this one proves runSelfupdate PRINTS them. Deleting the if-block in
// runSelfupdate satisfies the former and fails here.
//
// It drives the real verb rather than calling cleanDisplaced, and plants the leftover
// beside the resolved os.Executable path the code actually reads, because a plant
// anywhere else would pass while pinning nothing. The download seam fails so the verb
// stops just past the note, which also keeps captured stderr to a few hundred bytes:
// captureStderr drains only after the callback returns, and an unbounded writer would
// deadlock on the ~4KB pipe buffer here (que.11).
func TestSelfupdateReportsStuckDisplaced(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ = filepath.EvalSymlinks(exe) // match what runSelfupdate resolves

	planted := exe + displacedSuffix + "999"
	if err := os.WriteFile(planted, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the open handle is what makes removal fail; go omits FILE_SHARE_DELETE.
	held, err := os.Open(planted)
	if err != nil {
		os.Remove(planted)
		t.Fatal(err)
	}
	// lives beside the test binary, outside t.TempDir, so it must go on every exit path
	t.Cleanup(func() {
		held.Close()
		os.Remove(planted)
	})

	defer func(f func() error) { requireGhFn = f }(requireGhFn)
	requireGhFn = func() error { return nil }
	defer func(f func(string) (string, error)) { ghLatestTag = f }(ghLatestTag)
	ghLatestTag = func(string) (string, error) { return "v9.9.9", nil }
	defer func(f func(string, string, string, string) error) { ghDownload = f }(ghDownload)
	ghDownload = func(_, _, _, _ string) error { return errors.New("stubbed: stop just past the note") }
	t.Setenv("CBUS_REPO", "owner/repo")
	prev := version
	version = "v0.1.0" // a release older than latest, so the verb proceeds without --force
	t.Cleanup(func() { version = prev })

	out := captureStderr(t, func() {
		if rc := runSelfupdate(nil); rc == 0 {
			t.Error("the stubbed download must fail, so the verb stops just past the note")
		}
	})

	if !strings.Contains(out, filepath.Base(planted)) {
		t.Errorf("selfupdate did not report the stuck displaced image %q; stderr = %q", filepath.Base(planted), out)
	}
}

// TestCleanDisplacedReportsStuck is D27: a leftover that cannot be removed must be
// named, not swallowed. The open handle is the real mechanism rather than a stand-in —
// go omits FILE_SHARE_DELETE, so an open file resists removal exactly as a displaced
// image does while another cbus still runs from it.
func TestCleanDisplacedReportsStuck(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "cbus.exe")
	if err := os.WriteFile(exe, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	held := exe + displacedSuffix + "111"
	free := exe + displacedSuffix + "222"
	for _, p := range []string{held, free} {
		if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(held)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	stuck := cleanDisplaced(exe)

	if len(stuck) != 1 || stuck[0] != filepath.Base(held) {
		t.Errorf("stuck = %v, want exactly [%s]", stuck, filepath.Base(held))
	}
	// the removable one must still have been removed: one held file cannot abort the sweep
	if _, err := os.Stat(free); err == nil {
		t.Error("a held leftover stopped the sweep; the removable one survived")
	}
}
