package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAssetNameMatchesMakefile is S5: the asset name selfupdate hands gh as --pattern
// must equal the Makefile's dist output for every matrix platform. If either side
// changes format, this fails — the names cannot silently diverge into a download that
// matches nothing.
func TestAssetNameMatchesMakefile(t *testing.T) {
	matrix := []struct{ os, arch string }{
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"linux", "amd64"}, {"linux", "arm64"},
	}
	for _, m := range matrix {
		want := "cbus-" + m.os + "-" + m.arch // the exact string the Makefile dist rule writes
		if got := assetNameFor(m.os, m.arch); got != want {
			t.Errorf("assetNameFor(%s,%s) = %q, want %q", m.os, m.arch, got, want)
		}
	}
	// cross-check the Makefile still builds names as cbus-<os>-<arch> (BINARY=cbus,
	// out=$(DIST)/$(BINARY)-$$os-$$arch), so this pin tracks the real source.
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !regexp.MustCompile(`BINARY\s*:=\s*cbus\b`).Match(mk) {
		t.Error("Makefile BINARY is no longer 'cbus' — the asset-name pin is stale")
	}
	if !strings.Contains(string(mk), "$(BINARY)-$$os-$$arch") {
		t.Error("Makefile no longer builds names as $(BINARY)-$os-$arch — pin is stale")
	}
}

// fixtureBinary writes an executable that prints the given --version line, standing in
// for a downloaded release binary.
func fixtureBinary(t *testing.T, dir, versionLine string) string {
	t.Helper()
	p := filepath.Join(dir, "cbus-fixture")
	script := "#!/bin/sh\necho '" + versionLine + "'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestVerifyDownloadedGate is S4 end: a wrong-version (or unrunnable) fixture is
// REFUSED, and only an exact match passes — the gate that stands between a bad
// download and the swap.
func TestVerifyDownloadedGate(t *testing.T) {
	dir := t.TempDir()

	good := fixtureBinary(t, dir, "cbus-go v0.3.0")
	if err := verifyDownloaded(good, "v0.3.0"); err != nil {
		t.Errorf("matching version must pass the gate: %v", err)
	}
	// wrong version -> refused, with a clear reason
	if err := verifyDownloaded(good, "v0.4.0"); err == nil || !strings.Contains(err.Error(), "wrong or corrupt") {
		t.Errorf("mismatched version must be refused, got %v", err)
	}
	// a binary that will not run -> refused
	bad := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(bad, []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDownloaded(bad, "v0.3.0"); err == nil || !strings.Contains(err.Error(), "would not run") {
		t.Errorf("an unrunnable download must be refused, got %v", err)
	}
}

// TestSwapBinarySameFsAndFailureLeavesDst is S3: a same-fs swap replaces the target;
// a failed swap leaves the existing target untouched.
func TestSwapBinaryLeavesDstOnFailure(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "cbus")
	if err := os.WriteFile(dst, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	// success: same-fs rename replaces it.
	src := filepath.Join(dir, "new")
	if err := os.WriteFile(src, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(src, dst); err != nil {
		t.Fatalf("same-fs swap: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "NEW" {
		t.Error("swap did not replace the target")
	}
	// failure: a missing src leaves the (now NEW) target untouched, no partial write.
	if err := swapBinary(filepath.Join(dir, "nonexistent"), dst); err == nil {
		t.Error("swap of a missing src must error")
	}
	if b, _ := os.ReadFile(dst); string(b) != "NEW" {
		t.Error("a failed swap must leave the running binary untouched")
	}
}

// TestSelfupdateEarlyPaths covers the branches that return BEFORE any download or
// swap: no slug configured, --check reporting, dev-build refusal, already-latest.
// The download+swap path is not driven here — it would replace the test binary; its
// pieces (verifyDownloaded, swapBinary) are tested in isolation and the live wiring
// is the S10 checklist.
func TestSelfupdateEarlyPaths(t *testing.T) {
	// no slug -> the deferred M1 empty-slug remedy, printed here.
	t.Run("no slug", func(t *testing.T) {
		defer func(s string) { repoSlug = s }(repoSlug)
		repoSlug = ""
		t.Setenv("CBUS_REPO", "")
		if rc := runSelfupdate([]string{"--check"}); rc == 0 {
			t.Error("no slug must fail")
		}
	})

	// stub gh + the latest tag for the reporting branches.
	defer func(f func() error) { requireGhFn = f }(requireGhFn)
	requireGhFn = func() error { return nil }
	defer func(f func(string) (string, error)) { ghLatestTag = f }(ghLatestTag)

	withSlug := func(t *testing.T) {
		t.Setenv("CBUS_REPO", "owner/repo")
	}
	setVersion := func(t *testing.T, v string) {
		prev := version
		version = v
		t.Cleanup(func() { version = prev })
	}

	t.Run("check: dev build", func(t *testing.T) {
		withSlug(t)
		setVersion(t, "dev")
		ghLatestTag = func(string) (string, error) { return "v0.2.0", nil }
		out := captureStdout(t, func() {
			if rc := runSelfupdate([]string{"--check"}); rc != 0 {
				t.Fatalf("rc=%d", rc)
			}
		})
		if !strings.Contains(out, "dev/local build") || !strings.Contains(out, "0.2.0") {
			t.Errorf("check/dev output = %q", out)
		}
	})

	t.Run("check: update available", func(t *testing.T) {
		withSlug(t)
		setVersion(t, "v0.1.0")
		ghLatestTag = func(string) (string, error) { return "v0.2.0", nil }
		out := captureStdout(t, func() { runSelfupdate([]string{"--check"}) })
		if !strings.Contains(out, "0.1.0 -> 0.2.0 available") {
			t.Errorf("check/available output = %q", out)
		}
	})

	t.Run("check: already latest", func(t *testing.T) {
		withSlug(t)
		setVersion(t, "v0.2.0")
		ghLatestTag = func(string) (string, error) { return "v0.2.0", nil }
		out := captureStdout(t, func() { runSelfupdate([]string{"--check"}) })
		if !strings.Contains(out, "already on latest") {
			t.Errorf("check/latest output = %q", out)
		}
	})

	t.Run("apply: dev build refused without --force", func(t *testing.T) {
		withSlug(t)
		setVersion(t, "dev")
		ghLatestTag = func(string) (string, error) { return "v0.2.0", nil }
		if rc := runSelfupdate(nil); rc == 0 {
			t.Error("a dev build must refuse to overwrite without --force")
		}
	})

	t.Run("apply: already latest, no work", func(t *testing.T) {
		withSlug(t)
		setVersion(t, "v0.2.0")
		ghLatestTag = func(string) (string, error) { return "v0.2.0", nil }
		out := captureStdout(t, func() {
			if rc := runSelfupdate(nil); rc != 0 {
				t.Fatalf("rc=%d", rc)
			}
		})
		if !strings.Contains(out, "already on latest") {
			t.Errorf("apply/latest output = %q", out)
		}
	})
}
