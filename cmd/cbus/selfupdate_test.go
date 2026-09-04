package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestAssetNameMatrix pins the asset name selfupdate hands gh as --pattern against a
// literal matrix for every build platform, including the windows .exe. Pure and
// dependency-free, so it runs everywhere, the D8 gate host included. The Makefile/get.sh
// cross-check is TestAssetNameMatchesBuildScripts.
func TestAssetNameMatrix(t *testing.T) {
	// wants are literal on purpose: recomputing them the way assetNameFor does would
	// pass under any rule, including a dropped .exe.
	matrix := []struct{ os, arch, want string }{
		{"darwin", "amd64", "cbus-darwin-amd64"},
		{"darwin", "arm64", "cbus-darwin-arm64"},
		{"linux", "amd64", "cbus-linux-amd64"},
		{"linux", "arm64", "cbus-linux-arm64"},
		{"windows", "amd64", "cbus-windows-amd64.exe"},
	}
	for _, m := range matrix {
		if got := assetNameFor(m.os, m.arch); got != m.want {
			t.Errorf("assetNameFor(%s,%s) = %q, want %q", m.os, m.arch, got, m.want)
		}
	}
}

// TestAssetNameMatchesBuildScripts cross-checks the pinned names against the two OTHER
// places they live -- the Makefile and get.sh -- so a format change in either cannot
// silently diverge from selfupdate's --pattern. It needs the source tree, so on a host
// that runs only the built test binary with no repo checked out (the D8 gate) it SKIPS
// with a reason rather than reds; the names are covered everywhere by TestAssetNameMatrix.
func TestAssetNameMatchesBuildScripts(t *testing.T) {
	// cross-check the Makefile still builds names as cbus-<os>-<arch><ext> (BINARY=cbus,
	// out=$(DIST)/$(BINARY)-$$os-$$arch$$ext), so this pin tracks the real source.
	mk := readRepoFile(t, "Makefile")
	if !regexp.MustCompile(`BINARY\s*:=\s*cbus\b`).Match(mk) {
		t.Error("Makefile BINARY is no longer 'cbus' — the asset-name pin is stale")
	}
	if !strings.Contains(string(mk), "$(BINARY)-$$os-$$arch$$ext") {
		t.Error("Makefile no longer builds names as $(BINARY)-$os-$arch$ext — pin is stale")
	}
	if !strings.Contains(string(mk), "windows) ext=.exe") {
		t.Error("Makefile no longer appends .exe for windows — selfupdate would ask gh for an asset the build never wrote")
	}
	// anchored to a list line: an unanchored substring match is also satisfied by the
	// word appearing in the comment above PLATFORMS.
	if !regexp.MustCompile(`(?m)^\s+windows/amd64\s*\\?\s*$`).Match(mk) {
		t.Error("Makefile PLATFORMS no longer builds windows/amd64 — selfupdate on windows would ask gh for an asset no release carries")
	}
	// the bootstrap script is the THIRD place the asset name lives (c8); pin it too so
	// a format change cannot silently break get.sh's download.
	gs := readRepoFile(t, "get.sh")
	if !strings.Contains(string(gs), `BIN="cbus-${OS}-${ARCH}"`) {
		t.Error("get.sh no longer builds names as cbus-${OS}-${ARCH} — the asset-name pin is stale")
	}
}

// readRepoFile returns the contents of a repo-root file (Makefile, get.sh), or SKIPS the
// test only when the file is genuinely not present. It resolves the path from THIS test
// file's ABSOLUTE location (module root is two dirs above cmd/cbus); under -trimpath the
// recorded caller path is module-relative, so that branch is skipped -- joining a relative
// "claudebus/Makefile" could otherwise resolve into a neighbouring clone -- leaving only
// the CWD-relative ../../ fallback, which resolves when go test runs from the package dir.
// A present-but-unreadable file is a broken checkout and FAILS; only a not-found on every
// candidate (a binary run on a host with no source tree, e.g. the D8 gate) is the
// reason-carrying skip, since the names are pinned everywhere by TestAssetNameMatrix.
func readRepoFile(t *testing.T, name string) []byte {
	t.Helper()
	var tried []string
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		root := filepath.Dir(filepath.Dir(filepath.Dir(file))) // cmd/cbus -> cmd -> root
		tried = append(tried, filepath.Join(root, name))
	}
	tried = append(tried, filepath.Join("..", "..", name))
	for _, p := range tried {
		b, err := os.ReadFile(p)
		if err == nil {
			return b
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read %s: %v (present but unreadable is a broken checkout, not a skip)", p, err)
		}
	}
	t.Skipf("%s not found (looked in %v): this cross-check needs the source tree; the asset names are pinned everywhere by TestAssetNameMatrix", name, tried)
	return nil
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
	if runtime.GOOS == "windows" {
		t.Skip("the good fixture is a #!/bin/sh script, unrunnable on windows, so verifyDownloaded's " +
			"run-check cannot pass a valid binary here; successor: a Go self-exec or windows-native " +
			"fixture preserving match/mismatch/unrunnable (cbus-que.11)")
	}
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
