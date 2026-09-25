package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseFixture serves /o/r/releases/latest and /o/r/releases/download/<tag>/<file>
// from files; a nil value is a 404.
func releaseFixture(t *testing.T, latest string, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/o/r/releases/latest":
			if latest == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Location", latest)
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/o/r/releases/download/"):
			b, ok := files[strings.TrimPrefix(r.URL.Path, "/o/r/releases/download/")]
			if !ok || b == nil {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CBUS_RELEASE_BASE_URL", srv.URL)
	return srv
}

func TestHTTPLatestTagFromRedirect(t *testing.T) {
	for _, tc := range []struct{ loc, want, errHas string }{
		{"/o/r/releases/tag/v1.2.3", "v1.2.3", ""},
		{"https://example.test/o/r/releases/tag/v0.15.0", "v0.15.0", ""},
		{"", "", "no release found"},
		{"/o/r/releases", "", "unexpected redirect"},
	} {
		releaseFixture(t, tc.loc, nil)
		got, err := httpLatestTag("o/r")
		if tc.errHas == "" && (err != nil || got != tc.want) {
			t.Errorf("Location %q: got %q, %v; want %q", tc.loc, got, err, tc.want)
		}
		if tc.errHas != "" && (err == nil || !strings.Contains(err.Error(), tc.errHas)) {
			t.Errorf("Location %q: got %q, %v; want an error containing %q", tc.loc, got, err, tc.errHas)
		}
	}
}

func TestHTTPDownloadVerifiesAgainstSHA256SUMS(t *testing.T) {
	asset := "cbus-linux-amd64"
	good := []byte("the real binary")
	cases := []struct {
		name   string
		files  map[string][]byte
		errHas string
	}{
		{"matching sum", map[string][]byte{"v1.0.0/" + asset: good, "v1.0.0/SHA256SUMS": []byte(sha(good) + "  " + asset + "\n")}, ""},
		{"binary-mode line", map[string][]byte{"v1.0.0/" + asset: good, "v1.0.0/SHA256SUMS": []byte(sha(good) + " *" + asset + "\n")}, ""},
		{"mismatch", map[string][]byte{"v1.0.0/" + asset: []byte("tampered"), "v1.0.0/SHA256SUMS": []byte(sha(good) + "  " + asset + "\n")}, "checksum mismatch"},
		{"no SHA256SUMS", map[string][]byte{"v1.0.0/" + asset: good}, "has no SHA256SUMS"},
		{"no line for the asset", map[string][]byte{"v1.0.0/" + asset: good, "v1.0.0/SHA256SUMS": []byte(sha(good) + "  cbus-darwin-arm64\n")}, "has no line for"},
		{"longer name must not match", map[string][]byte{"v1.0.0/" + asset: good, "v1.0.0/SHA256SUMS": []byte(sha(good) + "  " + asset + ".exe\n")}, "has no line for"},
		{"name that only ends with the asset must not match", map[string][]byte{"v1.0.0/" + asset: good, "v1.0.0/SHA256SUMS": []byte(sha(good) + "  x-" + asset + "\n")}, "has no line for"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			releaseFixture(t, "", tc.files)
			out := filepath.Join(t.TempDir(), asset)
			err := httpDownload("o/r", "v1.0.0", asset, out)
			if tc.errHas == "" {
				if err != nil {
					t.Fatalf("verified download refused: %v", err)
				}
				if b, _ := os.ReadFile(out); string(b) != string(good) {
					t.Fatalf("downloaded %q, want the asset", b)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errHas) {
				t.Fatalf("got %v, want a refusal containing %q", err, tc.errHas)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Errorf("a refused download left %s behind", out)
			}
		})
	}
}

func TestSelfupdateSourceChoice(t *testing.T) {
	t.Setenv("CBUS_REPO", "o/r")
	prev := version
	version = "v0.1.0"
	t.Cleanup(func() { version = prev })
	defer func(a, b, c, d, e interface{}) {
		ghUsableFn = a.(func() bool)
		ghLatestTag = b.(func(string) (string, error))
		ghDownload = c.(func(string, string, string, string) error)
		httpLatestTag = d.(func(string) (string, error))
		httpDownload = e.(func(string, string, string, string) error)
	}(ghUsableFn, ghLatestTag, ghDownload, httpLatestTag, httpDownload)

	var used []string
	ghLatestTag = func(string) (string, error) { used = append(used, "gh-latest"); return "v9.9.9", nil }
	ghDownload = func(_, _, _, _ string) error {
		used = append(used, "gh-download")
		return errors.New("gh network error")
	}
	httpLatestTag = func(string) (string, error) { used = append(used, "https-latest"); return "v9.9.9", nil }
	httpDownload = func(_, _, _, _ string) error {
		used = append(used, "https-download")
		return errors.New("stubbed refusal")
	}

	ghUsableFn = func() bool { return false }
	var rc int
	stderr := captureStderr(t, func() { rc = runSelfupdate(nil) })
	if rc == 0 || strings.Join(used, ",") != "https-latest,https-download" || !strings.Contains(stderr, "https release download") {
		t.Errorf("without gh: rc=%d used=%v stderr=%q; want the https path only", rc, used, stderr)
	}

	used = nil
	ghUsableFn = func() bool { return true }
	stderr = captureStderr(t, func() { rc = runSelfupdate(nil) })
	if rc == 0 || strings.Join(used, ",") != "gh-latest,gh-download" || !strings.Contains(stderr, "gh network error") {
		t.Errorf("with gh: rc=%d used=%v stderr=%q; want the gh error surfaced with no https fallback", rc, used, stderr)
	}
}

func TestReleaseBaseURLRequiresHTTPS(t *testing.T) {
	for v, ok := range map[string]bool{
		"":                                   true,
		"https://mirror.example":             true,
		"http://127.0.0.1:8080":              true,
		"http://localhost:9000/":             true,
		"http://[::1]:7000":                  true,
		"http://example.com":                 false,
		"http://127.0.0.2":                   false,
		"http://localhost.evil.com":          false,
		"ftp://mirror.example":               false,
		"not a url":                          false,
		"http://localhost:x@example.invalid": false,
		"http://127.0.0.1:x@example.invalid": false,
		"http://[::1]:x@example.invalid":     false,
		"https://user:pass@mirror.example":   false,
	} {
		t.Setenv("CBUS_RELEASE_BASE_URL", v)
		_, err := releaseBaseURL()
		if ok != (err == nil) {
			t.Errorf("CBUS_RELEASE_BASE_URL=%q: err=%v, want accepted=%v", v, err, ok)
		}
		if strings.Contains(v, "@") && (err == nil || !strings.Contains(err.Error(), "credentials")) {
			t.Errorf("CBUS_RELEASE_BASE_URL=%q: want the explicit credentials refusal, got %v", v, err)
		}
		if !ok {
			if _, err := httpLatestTag("o/r"); err == nil {
				t.Errorf("CBUS_RELEASE_BASE_URL=%q: latest-tag lookup did not refuse", v)
			}
			if err := httpDownload("o/r", "v1.0.0", "cbus-linux-amd64", filepath.Join(t.TempDir(), "x")); err == nil {
				t.Errorf("CBUS_RELEASE_BASE_URL=%q: download did not refuse", v)
			}
		}
	}
}

func TestSelfupdateGhPathVerifiesSHA256SUMS(t *testing.T) {
	t.Setenv("CBUS_REPO", "o/r")
	prev := version
	version = "v0.1.0"
	t.Cleanup(func() { version = prev })
	defer func(a, b, c interface{}) {
		ghUsableFn = a.(func() bool)
		ghLatestTag = b.(func(string) (string, error))
		ghDownload = c.(func(string, string, string, string) error)
	}(ghUsableFn, ghLatestTag, ghDownload)
	ghUsableFn = func() bool { return true }
	ghLatestTag = func(string) (string, error) { return "v9.9.9", nil }
	asset := assetName()
	body := []byte("gh-downloaded bytes")
	for _, tc := range []struct {
		name, sums, want string
	}{
		{"mismatch refuses", sha([]byte("other")) + "  " + asset + "\n", "checksum mismatch"},
		{"missing sums refuses", "", "has no SHA256SUMS"},
		{"no line refuses", sha(body) + "  cbus-other\n", "has no line for"},
		{"good sum reaches the version gate", sha(body) + "  " + asset + "\n", "verify download"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ghDownload = func(_, _, name, out string) error {
				if name == "SHA256SUMS" {
					if tc.sums == "" {
						return errors.New("release asset not found")
					}
					return os.WriteFile(out, []byte(tc.sums), 0o644)
				}
				return os.WriteFile(out, body, 0o755)
			}
			var rc int
			stderr := captureStderr(t, func() { rc = runSelfupdate(nil) })
			if rc == 0 || !strings.Contains(stderr, tc.want) {
				t.Errorf("rc=%d stderr=%q, want a refusal containing %q", rc, stderr, tc.want)
			}
		})
	}
}
