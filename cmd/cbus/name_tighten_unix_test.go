//go:build darwin || linux

// spawn and branch are phase-1 windows-excluded verbs (unsupported_windows.go): runSpawn
// and runBranch refuse ahead of the name/channel pre-validators, so these CLI-door checks
// assert their frozen "bad name"/"bad channel" messages only on unix. Windows coverage is
// the spawn and branch refusal rows in unsupported_windows_test.go. The join/rename/save
// door tests, whose verbs are live on windows, stay in name_tighten_test.go.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

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
