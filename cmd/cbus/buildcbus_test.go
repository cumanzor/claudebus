package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildCbus builds the real cbus binary to a temp path and returns it. It is the ONE
// place the suite shells out to the toolchain, so the D8 disposition lives here: on a
// host with no go toolchain (the logos gate binary) every caller skips uniformly with one
// reason that names D8 and the host. A missing toolchain is a SKIP; a toolchain that is
// present but fails to build is a hard failure, never a skip.
func buildCbus(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		host, _ := os.Hostname()
		t.Skipf("no go toolchain on %s (%s/%s): this test builds the real cbus binary at runtime, "+
			"which the D8 gate host cannot do; skipped here, run where a toolchain exists",
			host, runtime.GOOS, runtime.GOARCH)
	}
	t.Logf("buildCbus: using %s", goBin)
	name := "cbus"
	if runtime.GOOS == "windows" {
		name += ".exe" // Go refuses to exec an extensionless binary on windows (que.5)
	}
	bin := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command(goBin, "build", "-o", bin, "claudebus/cmd/cbus").CombinedOutput(); err != nil {
		t.Fatalf("build cbus: %v\n%s", err, out)
	}
	return bin
}
