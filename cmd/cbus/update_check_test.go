package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	f()
	_ = w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestUpdateCheckCacheRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := updateCheckCachePath()
	if path == "" {
		t.Fatal("no cache path")
	}
	want := updateCheckCache{CheckedAt: time.Now().UTC().Truncate(time.Second), LatestKnown: "v0.3.0"}
	if err := writeUpdateCheckCache(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readUpdateCheckCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestKnown != "v0.3.0" || !got.CheckedAt.Equal(want.CheckedAt) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// TestUpdateCheckHint drives the synchronous hint decision. A FRESH cache is written
// so the stale-branch never spawns a detached poll during the test.
func TestUpdateCheckHint(t *testing.T) {
	writeFreshCache := func(t *testing.T, latest string) {
		t.Setenv("HOME", t.TempDir())
		p := updateCheckCachePath()
		if err := writeUpdateCheckCache(p, updateCheckCache{CheckedAt: time.Now().UTC(), LatestKnown: latest}); err != nil {
			t.Fatal(err)
		}
	}
	setVersion := func(t *testing.T, v string) {
		prev := version
		version = v
		t.Cleanup(func() { version = prev })
	}

	t.Run("opt-out: no hint without CBUS_UPDATE_CHECK", func(t *testing.T) {
		writeFreshCache(t, "v0.3.0")
		setVersion(t, "v0.1.0")
		t.Setenv("CBUS_REPO", "owner/repo")
		t.Setenv("CBUS_UPDATE_CHECK", "")
		out := captureStderr(t, func() { maybeStartUpdateCheck("list", false) })
		if out != "" {
			t.Errorf("opt-out must be silent, got %q", out)
		}
	})

	t.Run("newer stable -> one hint", func(t *testing.T) {
		writeFreshCache(t, "v0.3.0")
		setVersion(t, "v0.1.0")
		t.Setenv("CBUS_REPO", "owner/repo")
		t.Setenv("CBUS_UPDATE_CHECK", "1")
		out := captureStderr(t, func() { maybeStartUpdateCheck("list", false) })
		if !strings.Contains(out, "0.3.0 available") || !strings.Contains(out, "cbus selfupdate") {
			t.Errorf("hint = %q", out)
		}
		if strings.Count(out, "\n") != 1 {
			t.Errorf("exactly one line expected, got %q", out)
		}
	})

	t.Run("dev build -> no hint", func(t *testing.T) {
		writeFreshCache(t, "v0.3.0")
		setVersion(t, "dev")
		t.Setenv("CBUS_REPO", "owner/repo")
		t.Setenv("CBUS_UPDATE_CHECK", "1")
		if out := captureStderr(t, func() { maybeStartUpdateCheck("list", false) }); out != "" {
			t.Errorf("a dev build must not be nagged, got %q", out)
		}
	})

	t.Run("prerelease cached -> no hint", func(t *testing.T) {
		writeFreshCache(t, "v0.3.0-rc1")
		setVersion(t, "v0.1.0")
		t.Setenv("CBUS_REPO", "owner/repo")
		t.Setenv("CBUS_UPDATE_CHECK", "1")
		if out := captureStderr(t, func() { maybeStartUpdateCheck("list", false) }); out != "" {
			t.Errorf("a prerelease must stay invisible, got %q", out)
		}
	})

	t.Run("json mode + selfupdate + no-slug are skipped", func(t *testing.T) {
		writeFreshCache(t, "v0.3.0")
		setVersion(t, "v0.1.0")
		t.Setenv("CBUS_UPDATE_CHECK", "1")
		t.Setenv("CBUS_REPO", "owner/repo")
		if out := captureStderr(t, func() { maybeStartUpdateCheck("list", true) }); out != "" {
			t.Errorf("--json must be silent, got %q", out)
		}
		if out := captureStderr(t, func() { maybeStartUpdateCheck("selfupdate", false) }); out != "" {
			t.Errorf("selfupdate must be skipped, got %q", out)
		}
		t.Setenv("CBUS_REPO", "")
		if out := captureStderr(t, func() { maybeStartUpdateCheck("list", false) }); out != "" {
			t.Errorf("no slug must be silent, got %q", out)
		}
	})
}

func TestHasJSONFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"list", "--json"}, true},
		{[]string{"list", "-json"}, true},
		{[]string{"list"}, false},
		{[]string{"send", "--", "--json"}, false}, // after -- it is a message body
	} {
		if got := hasJSONFlag(tc.args); got != tc.want {
			t.Errorf("hasJSONFlag(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestUpdateCheckSubcmdDispatch: the hidden refresh verb returns 0 and never recurses.
func TestUpdateCheckSubcmdDispatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CBUS_REPO", "") // no slug -> refresh no-ops without touching gh
	if rc := run([]string{updateCheckSubcmd}); rc != 0 {
		t.Errorf("hidden refresh must return 0, got %d", rc)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "cbus", "update-check.json")); err == nil {
		t.Error("with no slug the refresh should not write a cache")
	}
}
