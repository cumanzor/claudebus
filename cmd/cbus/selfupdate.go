package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ghLatestTag and ghDownload are seams: real gh calls in production, injectable in
// tests so the flow around them (version-gate, swap, refusals) can be exercised
// without a live release. The gh round-trip itself needs a real release and rides the
// post-release checklist (S10).
var (
	ghLatestTag = ghLatestTagImpl
	ghDownload  = ghDownloadImpl
	requireGhFn = requireGh
)

// runSelfupdate: cbus selfupdate [--check] [--force]
func runSelfupdate(args []string) int {
	p, err := splitVerbArgs(args, nil, map[string]bool{"--check": true, "--force": true}, true)
	if err != nil {
		return die("%v (usage: cbus selfupdate [--check] [--force])", err)
	}
	if err := noExtra(p.pos, 0, "usage: cbus selfupdate [--check] [--force]"); err != nil {
		return die("%v", err)
	}
	checkOnly, force := p.flags["--check"], p.flags["--force"]

	slug, ok := resolveRepoSlug()
	if !ok {
		return die("%s", repoSlugRemedy)
	}
	if err := requireGhFn(); err != nil {
		return die("%v", err)
	}
	latest, err := ghLatestTag(slug)
	if err != nil {
		return die("%v", err)
	}

	current := normalizeVersion(version)
	latestNorm := normalizeVersion(latest)
	isRelease := isReleaseVersion(current)

	if checkOnly {
		switch {
		case !isRelease:
			fmt.Printf("cbus: %s (dev/local build) — latest release: %s\n", current, latestNorm)
		case current == latestNorm:
			fmt.Printf("cbus: %s — already on latest\n", current)
		default:
			fmt.Printf("cbus: %s -> %s available\n", current, latestNorm)
		}
		return 0
	}

	if !isRelease && !force {
		return die("selfupdate: running a dev/local build (%s) — refusing to overwrite; pass --force to install %s", current, latestNorm)
	}
	if current == latestNorm && !force {
		fmt.Printf("cbus: already on latest (%s)\n", current)
		return 0
	}

	exePath, err := os.Executable()
	if err != nil {
		return die("locate current binary: %v", err)
	}
	exePath, _ = filepath.EvalSymlinks(exePath)

	asset := assetName()
	tmpDir, err := os.MkdirTemp("", "cbus-selfupdate-")
	if err != nil {
		return die("%v", err)
	}
	defer os.RemoveAll(tmpDir)
	tmpBin := filepath.Join(tmpDir, asset)

	fmt.Printf("cbus: downloading %s %s from %s...\n", latest, asset, slug)
	if err := ghDownload(slug, latest, asset, tmpBin); err != nil {
		return die("gh release download: %v", err)
	}
	// zero-assets-matched must be LOUD, never a quiet no-update (S5): gh can succeed
	// having written nothing when the pattern matched no asset.
	if fi, err := os.Stat(tmpBin); err != nil || fi.Size() == 0 {
		return die("no asset named %q in release %s of %s — that platform's binary is missing from the release", asset, latest, slug)
	}
	_ = os.Chmod(tmpBin, 0o755)

	// VERSION-GATE (S4): the download must report the version we asked for BEFORE it
	// is allowed near the install. A corrupt or wrong-asset binary never swaps in.
	if err := verifyDownloaded(tmpBin, latest); err != nil {
		return die("%v", err)
	}

	if err := swapBinary(tmpBin, exePath); err != nil {
		return die("%v", err)
	}
	fmt.Printf("updated %s -> %s\n", current, latestNorm)

	// refresh the embedded commands + roles via the NEW binary (its embed carries the
	// new assets). Best-effort but NEVER silent (D27): the verbs report per file, and
	// a refresh that cannot run says so.
	refreshAssets(exePath)
	return 0
}

// verifyDownloaded runs the downloaded binary's --version and gates on it matching
// the requested tag. Any failure returns an error and NOTHING is swapped.
func verifyDownloaded(binPath, wantTag string) error {
	out, err := exec.Command(binPath, "--version").Output()
	if err != nil {
		return fmt.Errorf("verify download: the downloaded binary would not run (%v) — refusing to install it", err)
	}
	reported := parseVersionOutput(string(out))
	if !versionMatchesTag(reported, wantTag) {
		return fmt.Errorf("verify download: got version %q, expected %s — wrong or corrupt asset, refusing to install it",
			reported, normalizeVersion(wantTag))
	}
	return nil
}

// refreshAssets execs the (now swapped-in) binary to reinstall commands and roles.
func refreshAssets(exePath string) {
	for _, verb := range []string{"install-commands", "install-roles"} {
		cmd := exec.Command(exePath, verb, "--force")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// non-zero exit here is a per-file skip/fail the verb already printed, or a
			// real run failure — either way it is surfaced, not swallowed.
			fmt.Fprintf(os.Stderr, "cbus: note: %s refresh reported problems (see above)\n", verb)
		}
	}
}

// requireGh surfaces actionable hints because the repo is private and selfupdate
// cannot work without an authenticated gh.
func requireGh() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found — install from https://cli.github.com/ then run 'gh auth login'")
	}
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		return fmt.Errorf("gh is not authenticated — run 'gh auth login'")
	}
	return nil
}

func ghLatestTagImpl(slug string) (string, error) {
	cmd := exec.Command("gh", "release", "view", "--repo", slug, "--json", "tagName")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// surface gh's own message ("release not found", "could not resolve to a
		// Repository") — for the private-repo flow this is the error users meet (c6).
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("gh release view: %s", msg)
		}
		return "", fmt.Errorf("gh release view: %w", err)
	}
	var resp struct {
		TagName string `json:"tagName"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("parse gh response: %w", err)
	}
	if resp.TagName == "" {
		return "", fmt.Errorf("gh returned an empty tag — no release found in %s", slug)
	}
	return resp.TagName, nil
}

func ghDownloadImpl(slug, tag, asset, out string) error {
	cmd := exec.Command("gh", "release", "download", tag,
		"--repo", slug, "--pattern", asset, "--output", out, "--clobber")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// swapBinary replaces dst with src on unix, leaving the running binary untouched on
// any failure (S3). os.Rename works in place while the binary runs (the kernel keeps
// the open inode alive). A cross-filesystem src (/tmp on tmpfs) is staged into a
// SIBLING of dst on the same filesystem, then atomically renamed — a direct copy onto
// dst would O_TRUNC the running binary and trip ETXTBSY.
func swapBinary(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	sibling := dst + ".new." + strconv.Itoa(os.Getpid())
	if err := copyFile(src, sibling); err != nil {
		_ = os.Remove(sibling)
		return fmt.Errorf("stage new binary beside %s: %w", dst, err)
	}
	if err := os.Rename(sibling, dst); err != nil {
		_ = os.Remove(sibling)
		return fmt.Errorf("swap new binary into %s: %w", dst, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
