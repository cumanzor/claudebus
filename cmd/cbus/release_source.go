package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Releases are public, so gh is optional: an installed, authenticated gh is used as
// before; without one, the anonymous HTTPS path fetches the same assets and refuses
// any binary SHA256SUMS does not vouch for.
var (
	ghUsableFn    = ghUsable
	httpLatestTag = func(slug string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpLatestTagCtx(ctx, slug)
	}
	httpDownload = httpDownloadVerified
)

// releaseBaseURL is github.com unless CBUS_RELEASE_BASE_URL points at a mirror or a
// test fixture serving the same /<slug>/releases/... layout. It must be https, except
// plain http to this machine for a local fixture: the sums come from the same base,
// so a cleartext remote base could swap binary and sums together.
func releaseBaseURL() (string, error) {
	v := strings.TrimRight(os.Getenv("CBUS_RELEASE_BASE_URL"), "/")
	if v == "" {
		return "https://github.com", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("CBUS_RELEASE_BASE_URL %q is not a URL", v)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"):
	default:
		return "", fmt.Errorf("CBUS_RELEASE_BASE_URL %q must be https:// (plain http only for 127.0.0.1, localhost or [::1])", v)
	}
	return v, nil
}

// ghUsable: gh is on PATH and authenticated. Anything short of that selects HTTPS; a
// failure after gh was chosen surfaces instead of falling back.
func ghUsable() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	return exec.Command("gh", "auth", "status").Run() == nil
}

// httpLatestTagCtx reads the tag from the redirect /releases/latest answers with,
// which needs no API token and has no API rate limit.
func httpLatestTagCtx(ctx context.Context, slug string) (string, error) {
	base, err := releaseBaseURL()
	if err != nil {
		return "", err
	}
	u := base + "/" + slug + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("no release found in %s", slug)
	}
	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
		return "", fmt.Errorf("resolve latest release: %s answered %s without a redirect", u, resp.Status)
	}
	const marker = "/releases/tag/"
	i := strings.LastIndex(loc, marker)
	if i < 0 {
		return "", fmt.Errorf("resolve latest release: unexpected redirect %q", loc)
	}
	tag, err := url.PathUnescape(strings.Trim(loc[i+len(marker):], "/"))
	if err != nil || tag == "" || strings.Contains(tag, "/") {
		return "", fmt.Errorf("resolve latest release: unexpected redirect %q", loc)
	}
	return tag, nil
}

// httpDownloadVerified fetches SHA256SUMS first, then streams the asset to out while
// hashing it. A missing sums file, a missing line or a mismatch is a refusal and
// removes the partial download, so nothing unverified can reach the swap.
func httpDownloadVerified(slug, tag, asset, out string) error {
	root, err := releaseBaseURL()
	if err != nil {
		return err
	}
	base := root + "/" + slug + "/releases/download/" + url.PathEscape(tag) + "/"
	client := &http.Client{Timeout: 120 * time.Second}
	sums, err := httpGetBytes(client, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return noSumsError(tag, err)
	}
	want, err := expectedSum(sums, tag, asset)
	if err != nil {
		return err
	}
	resp, err := client.Get(base + url.PathEscape(asset))
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", asset, resp.Status)
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 256<<20))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(out)
		return fmt.Errorf("download %s: %v", asset, firstErr(copyErr, closeErr))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(out)
		return mismatchError(asset, tag, want, got)
	}
	return nil
}

// verifyFileAgainstSums is the gh path's check: the same exact-line rule and the same
// refusals as the HTTPS path, on a file gh already wrote.
func verifyFileAgainstSums(path string, sums []byte, tag, asset string) error {
	want, err := expectedSum(sums, tag, asset)
	if err != nil {
		os.Remove(path)
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if got := sha(b); got != want {
		os.Remove(path)
		return mismatchError(asset, tag, want, got)
	}
	return nil
}

func expectedSum(sums []byte, tag, asset string) (string, error) {
	want, ok := sumFor(sums, asset)
	if !ok {
		return "", fmt.Errorf("SHA256SUMS of %s has no line for %s — refusing to install a binary it cannot verify", tag, asset)
	}
	return want, nil
}

// noSumsError: a release with no SHA256SUMS carries no verifiable binaries (older
// releases were republished as notes only).
func noSumsError(tag string, err error) error {
	return fmt.Errorf("release %s has no SHA256SUMS (%v): it carries no verifiable binaries, refusing to install", tag, err)
}

func mismatchError(asset, tag, want, got string) error {
	return fmt.Errorf("checksum mismatch for %s in %s: SHA256SUMS says %s, download is %s — refusing to install it", asset, tag, want, got)
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func httpGetBytes(client *http.Client, u string, limit int64) ([]byte, error) {
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// sumFor finds asset's hash in sha256sum/shasum output ("<hex>  <name>", or
// "<hex> *<name>" in binary mode); the name must match exactly.
func sumFor(sums []byte, asset string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != asset {
			continue
		}
		if b, err := hex.DecodeString(f[0]); err == nil && len(b) == sha256.Size {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
