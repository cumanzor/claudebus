package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
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
