package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The store-name tightening, exercised through the CLI door (run(...)) rather than
// through client.Join directly: an in-process call proves the predicate works, not
// that the verb reaches it. A wiring that never runs looks identical from inside.

// tightenBad is the set every creation entry must refuse. ".." and "a/b" are here to
// keep the charset half covered — the tightening is additive, so both halves must
// still bite at every door.
var tightenBad = []string{".hidden", "-flag", "--force", "..", "a/b"}

func joinRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-tighten")
	return root
}

func TestJoinRefusesBadChannelAtCLIDoor(t *testing.T) {
	for _, bad := range tightenBad {
		t.Run(bad, func(t *testing.T) {
			root := joinRoot(t)
			var rc int
			errOut := captureStderr(t, func() { rc = run([]string{"join", bad, "peer"}) })
			if rc == 0 {
				t.Fatalf("join %q returned 0 — the channel was accepted", bad)
			}
			if !strings.Contains(errOut, "channel") {
				t.Errorf("stderr %q does not name the channel as the problem", errOut)
			}
			if got := storeEntries(t, root); len(got) != 0 {
				t.Errorf("join %q was refused but the store gained %v", bad, got)
			}
		})
	}
}

func TestJoinRefusesBadAliasAtCLIDoor(t *testing.T) {
	for _, bad := range tightenBad {
		t.Run(bad, func(t *testing.T) {
			root := joinRoot(t)
			var rc int
			errOut := captureStderr(t, func() { rc = run([]string{"join", "ok", bad}) })
			if rc == 0 {
				t.Fatalf("join ok %q returned 0 — the alias was accepted", bad)
			}
			if !strings.Contains(errOut, "alias") {
				t.Errorf("stderr %q does not name the alias as the problem", errOut)
			}
			if got := storeEntries(t, filepath.Join(root, "ok")); len(got) != 0 {
				t.Errorf("join ok %q was refused but the channel gained %v", bad, got)
			}
		})
	}
}

func TestRenameRefusesBadAliasAtCLIDoor(t *testing.T) {
	for _, bad := range tightenBad {
		t.Run(bad, func(t *testing.T) {
			root := joinRoot(t)
			if rc := captureRC(t, func() int { return run([]string{"join", "ok", "good"}) }); rc != 0 {
				t.Fatalf("setup join rc=%d", rc)
			}
			var rc int
			captureStderr(t, func() { rc = run([]string{"rename", bad}) })
			if rc == 0 {
				t.Fatalf("rename %q returned 0", bad)
			}
			// the roster is the assertion: a refused rename must leave exactly the
			// original peer, having neither created the new name nor moved the old one.
			if got := storeEntries(t, filepath.Join(root, "ok")); len(got) != 1 || got[0] != "good" {
				t.Errorf("rename %q was refused but the channel holds %v, want [good]", bad, got)
			}
		})
	}
}

// A --name that fails the store rule must be refused BEFORE the fork: the forker is
// what opens a terminal window, and the whole point of the pre-validator is that a
// flag-shaped alias never reaches a child CLI. Both verbs share the check.
func TestSpawnAndBranchRefuseBadNameAtCLIDoor(t *testing.T) {
	for _, bad := range []string{".hidden", "-flag", "--force"} {
		t.Run(bad, func(t *testing.T) {
			root := joinRoot(t)
			for _, verb := range []string{"spawn", "branch"} {
				var rc int
				errOut := captureStderr(t, func() { rc = run([]string{verb, "window", "dev", "--name", bad}) })
				if rc == 0 {
					t.Fatalf("%s --name %q returned 0", verb, bad)
				}
				if !strings.Contains(errOut, "bad name") {
					t.Errorf("%s stderr = %q, want the frozen \"bad name\" message", verb, errOut)
				}
				if got := storeEntries(t, filepath.Join(root, "dev")); len(got) != 0 {
					t.Errorf("%s --name %q was refused but the channel gained %v", verb, bad, got)
				}
			}
		})
	}
}

// The channel positional is a creation path too: spawn reserves in it, branch joins
// the parent into it. Both must refuse a bad one before touching the store.
func TestSpawnAndBranchRefuseBadChannelAtCLIDoor(t *testing.T) {
	for _, bad := range []string{".hidden", "-flag"} {
		t.Run(bad, func(t *testing.T) {
			root := joinRoot(t)
			for _, verb := range []string{"spawn", "branch"} {
				var rc int
				errOut := captureStderr(t, func() { rc = run([]string{verb, "window", bad}) })
				if rc == 0 {
					t.Fatalf("%s %q returned 0", verb, bad)
				}
				// the FROZEN pre-validator message, not merely "channel": the store
				// chokepoint refuses this too, so a looser assertion passes with the
				// pre-validator deleted and pins nothing.
				if !strings.Contains(errOut, "bad channel") {
					t.Errorf("%s stderr = %q, want the frozen \"bad channel\" message", verb, errOut)
				}
				if got := storeEntries(t, root); len(got) != 0 {
					t.Errorf("%s %q was refused but the store gained %v", verb, bad, got)
				}
			}
		})
	}
}

// A formation name becomes .formations/<name>.json, which ListFormations skips when
// dot-prefixed — the same create-then-invisible shape, so a NEW envelope is gated.
func TestFormationSaveRefusesBadNameAtCLIDoor(t *testing.T) {
	for _, bad := range []string{".hidden", "-flag"} {
		t.Run(bad, func(t *testing.T) {
			root := joinRoot(t)
			if rc := captureRC(t, func() int { return run([]string{"join", "ok", "good"}) }); rc != 0 {
				t.Fatalf("setup join rc=%d", rc)
			}
			var rc int
			errOut := captureStderr(t, func() { rc = run([]string{"formation", "save", bad, "ok"}) })
			if rc == 0 {
				t.Fatalf("formation save %q returned 0", bad)
			}
			if !strings.Contains(errOut, "formation name") {
				t.Errorf("stderr = %q, want it to name the formation name", errOut)
			}
			if got := storeEntries(t, filepath.Join(root, ".formations")); len(got) != 0 {
				t.Errorf("formation save %q was refused but .formations holds %v", bad, got)
			}
		})
	}
}

// The tightening is a subset rule, so every name that was legal before and does not
// LEAD with . or - must still work. Without this the suite cannot tell "rejects the
// two bad shapes" from "rejects everything".
func TestGoodNamesStillJoinAtCLIDoor(t *testing.T) {
	for _, good := range []string{"main", "a.b_c-d", "42", "fork-1"} {
		t.Run(good, func(t *testing.T) {
			root := joinRoot(t)
			if rc := captureRC(t, func() int { return run([]string{"join", good, good}) }); rc != 0 {
				t.Fatalf("join %s %s rc=%d, want 0", good, good, rc)
			}
			if _, err := os.Stat(filepath.Join(root, good, good, "meta.json")); err != nil {
				t.Errorf("join %s %s reported success but wrote no meta: %v", good, good, err)
			}
		})
	}
}

// storeEntries lists what a store dir actually holds. Asserting on the LISTING, not
// on a Stat of the rejected name: filepath.Join cleans "x/.." back to a path that
// always exists, so a Stat-based check reports a phantom directory for the ".." case
// and passes for the wrong reason on the others.
func storeEntries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil // no dir at all is the strongest form of "nothing was created"
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// captureRC runs f with stdout+stderr swallowed and returns its exit code.
func captureRC(t *testing.T, f func() int) int {
	t.Helper()
	var rc int
	_ = captureStdout(t, func() { _ = captureStderr(t, func() { rc = f() }) })
	return rc
}
