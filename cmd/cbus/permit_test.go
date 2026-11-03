package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func permitCLIFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CBUS_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-lead")
	plantMeta(t, dir, "dev", "lead", "sid-lead")
	plantMeta(t, dir, "dev", "coder", "sid-coder")
	rec := filepath.Join(dir, ".coordinators", "dev")
	if err := os.MkdirAll(rec, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec, "coder.json"), []byte(`{"coordinator":"dev/lead","door":"apply","sessionId":"sid-coder"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runVerb returns exit code, stdout and stderr.
func runVerb(t *testing.T, args ...string) (rc int, out, errOut string) {
	t.Helper()
	errOut = captureStderr(t, func() { out = captureStdout(t, func() { rc = run(args) }) })
	return rc, out, errOut
}

func TestPermitVerbsThroughTheCLI(t *testing.T) {
	permitCLIFixture(t)
	rc, out, errOut := runVerb(t, "permit", "push-branch,pr-update", "--repo", "git@github.com:o/r.git", "--ref", "epic/demo*", "--says", "push the epic branches", "--ttl", "2d")
	if rc != 0 || !strings.Contains(out, "push-branch,pr-update ref epic/demo* on github.com/o/r") || !strings.Contains(out, "written by dev/lead") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errOut)
	}
	id := strings.SplitN(out, ":", 2)[0]

	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-coder")
	rc, out, errOut = runVerb(t, "permits", "use", "push-branch", "--ref", "epic/demo-1", "--repo", "https://github.com/o/r.git")
	if rc != 0 || errOut != "" || !strings.HasPrefix(out, "covered by "+id+": ") || !strings.Contains(out, "(use 1)") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errOut)
	}
	rc, out, errOut = runVerb(t, "permits", "use", "push-branch", "--ref", "epic/demo/x", "--repo", "https://github.com/o/r.git")
	if rc != 1 || out != "" || !strings.HasPrefix(errOut, "not covered: "+id+" covers ref") || !strings.HasSuffix(strings.TrimSpace(errOut), "ask dev/lead on the bus") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errOut)
	}

	rc, out, _ = runVerb(t, "permits", "--json")
	var views []map[string]any
	if rc != 0 || json.Unmarshal([]byte(out), &views) != nil || len(views) != 1 || views[0]["uses"] != float64(1) {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	rc, out, _ = runVerb(t, "permits", "--all")
	if rc != 0 || !strings.Contains(out, "says: push the epic branches") || !strings.Contains(out, "uses 1") {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	rc, out, _ = runVerb(t, "permit", "revoke", id)
	if rc != 0 || !strings.Contains(out, "by dev/coder") {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	rc, _, errOut = runVerb(t, "permits", "use", "push-branch", "--ref", "epic/demo-1", "--repo", "https://github.com/o/r.git")
	if rc != 1 || !strings.Contains(errOut, "was revoked") {
		t.Fatalf("rc=%d err=%q", rc, errOut)
	}
}

func TestPermitsUseReadsOriginFromTheWorkingDirectory(t *testing.T) {
	permitCLIFixture(t)
	if rc, _, errOut := runVerb(t, "permit", "draft-pr", "--repo", "https://github.com/o/r", "--ref", "x", "--says", "go"); rc != 0 {
		t.Fatal(errOut)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-coder")
	repo := t.TempDir()
	t.Chdir(repo)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:o/r.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	if rc, out, errOut := runVerb(t, "permits", "use", "draft-pr", "--ref", "x"); rc != 0 || !strings.HasPrefix(out, "covered by") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errOut)
	}
	t.Chdir(t.TempDir())
	if rc, _, errOut := runVerb(t, "permits", "use", "draft-pr", "--ref", "x"); rc != 2 || !strings.Contains(errOut, "pass --repo") {
		t.Fatalf("rc=%d err=%q", rc, errOut)
	}
}

func TestPermitExitCodesTellATypoFromANo(t *testing.T) {
	permitCLIFixture(t)
	for name, args := range map[string][]string{
		"permit no args":    {"permit"},
		"permit bad flag":   {"permit", "draft-pr", "--bogus"},
		"permit junk":       {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x", "--says", "go", "junk"},
		"permit no says":    {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x"},
		"permit wildcard":   {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "*", "--says", "go"},
		"permit bad ttl":    {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x", "--says", "go", "--ttl", "soon"},
		"permit zero ttl":   {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x", "--says", "go", "--ttl", "0d"},
		"permit zero hours": {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x", "--says", "go", "--ttl", "0h"},
		"permit over max":   {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x", "--says", "go", "--ttl", "31d"},
		"permit relay":      {"permit", "draft-pr", "--repo", "h:o/r", "--ref", "x", "--says", "go", "--channel", "dev@server"},
		"revoke no id":      {"permit", "revoke"},
		"revoke bad id":     {"permit", "revoke", "../x"},
		"permits junk":      {"permits", "junk"},
		"permits bad flag":  {"permits", "--bogus"},
		"use no action":     {"permits", "use"},
		"use no ref":        {"permits", "use", "push-branch", "--repo", "h:o/r"},
		"use junk":          {"permits", "use", "push-branch", "--repo", "h:o/r", "--ref", "x", "junk"},
		"use bad flag":      {"permits", "use", "push-branch", "--bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			if rc, _, errOut := runVerb(t, args...); rc != 2 {
				t.Fatalf("rc=%d err=%q", rc, errOut)
			}
		})
	}
	if rc, _, errOut := runVerb(t, "permit", "revoke", "p-0000000000"); rc != 1 || !strings.Contains(errOut, "no permit") {
		t.Fatalf("rc=%d err=%q", rc, errOut)
	}
}

func TestPermitsNotJoinedAsksForAll(t *testing.T) {
	permitCLIFixture(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-nobody")
	if rc, _, errOut := runVerb(t, "permits"); rc != 2 || !strings.Contains(errOut, "--all") {
		t.Fatalf("rc=%d err=%q", rc, errOut)
	}
	if rc, out, _ := runVerb(t, "permits", "--all"); rc != 0 || !strings.Contains(out, "no permits") {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
}
