package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/client"
	"claudebus/internal/core"
)

// captureStdout runs f with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestAuthSetAndStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	store := client.NewFileCredStore() // force the file backend on any OS

	out := captureStdout(t, func() {
		if rc := runAuthSet(store, []string{"nuc", "--token", "  abc123  ", "--cf-id", "cf-xyz"}, strings.NewReader("")); rc != 0 {
			t.Fatalf("runAuthSet rc=%d", rc)
		}
	})
	if !strings.Contains(out, "stored 2 credential(s) for nuc") {
		t.Errorf("set output = %q", out)
	}
	// whitespace stripped on write
	if b, _ := os.ReadFile(filepath.Join(dir, "cbus", "nuc", "token")); string(b) != "abc123" {
		t.Errorf("stored token = %q, want abc123", b)
	}

	st := captureStdout(t, func() { runAuthStatus(store, []string{"nuc"}) })
	for _, want := range []string{"site nuc:", "set (…c123)", "set (…-xyz)", "absent"} {
		if !strings.Contains(st, want) {
			t.Errorf("status missing %q in:\n%s", want, st)
		}
	}
}

func TestAuthSetStdin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	store := client.NewFileCredStore()
	if rc := runAuthSet(store, []string{"nuc", "--token", "-"}, strings.NewReader("tok-from-stdin\n")); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cbus", "nuc", "token")); string(b) != "tok-from-stdin" {
		t.Errorf("stdin token = %q", b)
	}
}

func TestAuthSetErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	store := client.NewFileCredStore()
	cases := map[string][]string{
		"unknown flag":   {"nuc", "--bogus", "x"},
		"empty value":    {"nuc", "--token", "   "},
		"nothing to set": {"nuc"},
		"bad host":       {"bad/host", "--token", "x"},
		"missing value":  {"nuc", "--token"},
	}
	for name, args := range cases {
		if rc := runAuthSet(store, args, strings.NewReader("")); rc == 0 {
			t.Errorf("%s: expected non-zero exit", name)
		}
	}
}

func TestAuthStatusBadHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	store := client.NewFileCredStore()
	if rc := runAuthStatus(store, []string{"bad/host"}); rc == 0 {
		t.Error("auth status with a bad host should fail (closed gap)")
	}
}

// TestAuthSetDoubleStdinDrainsOnce pins the reviewer-requested contract: each '-'
// drains stdin, so a second stdin-fed credential in one invocation gets empty and
// dies — and the first credential is still stored.
func TestAuthSetDoubleStdinDrainsOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	store := client.NewFileCredStore()
	rc := runAuthSet(store, []string{"nuc", "--token", "-", "--cf-id", "-"}, strings.NewReader("only-one-token\n"))
	if rc == 0 {
		t.Error("second '-' should drain empty and fail")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cbus", "nuc", "token")); string(b) != "only-one-token" {
		t.Errorf("first token = %q, want only-one-token", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "cbus", "nuc", "cf-id")); err == nil {
		t.Error("cf-id must not be written after the drain-empty failure")
	}
}

func TestRenderRemoteList(t *testing.T) {
	mustTime := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	peers := core.PeersResponse{
		"dev/nuc": {Connected: true, Queued: 0, LastSeen: mustTime("2026-07-12T14:31:10.802809793-06:00")},
		"dev/mbp": {Connected: false, Queued: 3, LastSeen: mustTime("2026-07-12T15:00:00Z")},
		"other/x": {Connected: true, Queued: 1, LastSeen: mustTime("2026-07-12T15:00:00Z")},
	}
	// channel filter "dev" excludes other/x; sorted -> dev/mbp before dev/nuc
	out := captureStdout(t, func() { renderRemoteList(peers, "dev", "nuc") })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 filtered lines, got %d:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "off   ") || !strings.Contains(lines[0], "dev@nuc/mbp") || !strings.Contains(lines[0], "queued=3") {
		t.Errorf("line0 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "listen") || !strings.Contains(lines[1], "dev@nuc/nuc") || !strings.Contains(lines[1], "lastSeen=2026-07-12T14:31:10.802809793-06:00") {
		t.Errorf("line1 = %q", lines[1])
	}

	// empty result prints the "no remote peers in ..." line
	empty := captureStdout(t, func() { renderRemoteList(core.PeersResponse{}, "dev", "nuc") })
	if strings.TrimSpace(empty) != "no remote peers in dev@nuc" {
		t.Errorf("empty render = %q", empty)
	}
}
