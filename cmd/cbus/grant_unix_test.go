//go:build darwin || linux

package main

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestGrantBinaryRefusesWithoutATerminal runs the real binary with no controlling
// terminal (a new session, stdin from /dev/null): the gate itself, no seam.
func TestGrantBinaryRefusesWithoutATerminal(t *testing.T) {
	bin := buildCbus(t)
	root := t.TempDir()
	join := exec.Command(bin, "join", "ch", "coder")
	join.Env = []string{"CBUS_DIR=" + root, "HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CBUS_SESSION_ID=sid-coder"}
	if out, err := join.CombinedOutput(); err != nil {
		t.Fatalf("join ch/coder: %v\n%s", err, out)
	}
	before := storeHash(t, root)
	for _, args := range [][]string{{"grant", "ch/coder", "push"}, {"grant", "revoke", "g-0000000000"}} {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"CBUS_DIR=" + root, "HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%v: must exit non-zero without a terminal:\n%s", args, out)
		}
		if !strings.Contains(string(out), "grant needs the operator at a real terminal") || !strings.Contains(string(out), "nothing was written") {
			t.Errorf("%v: refusal not stated:\n%s", args, out)
		}
	}
	if storeHash(t, root) != before {
		t.Error("a refused grant changed the store")
	}
}
