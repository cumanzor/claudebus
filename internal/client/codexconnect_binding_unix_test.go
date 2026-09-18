//go:build darwin || linux

package client

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This is an isolated process/file witness test, not a running Codex session or
// a model call. The child holds only a temporary queue filename open.
func TestCodexRuntimeOpenQueueWitness(t *testing.T) {
	if os.Getenv("CBUS_TEST_OPEN_QUEUE_CHILD") == "1" {
		file, err := os.Open(os.Getenv("CBUS_TEST_OPEN_QUEUE_PATH"))
		if err != nil {
			os.Exit(2)
		}
		defer file.Close()
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	home := t.TempDir()
	path := filepath.Join(home, "queue_1.sqlite")
	if err := os.WriteFile(path, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestCodexRuntimeOpenQueueWitness$")
	cmd.Env = append(os.Environ(), "CBUS_TEST_OPEN_QUEUE_CHILD=1", "CBUS_TEST_OPEN_QUEUE_PATH="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "ready\n" {
		t.Fatalf("child readiness=%q %v", ready, err)
	}
	binding, err := inspectCodexRuntime(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if binding.SQLiteHome != canonicalTestPath(t, home) || binding.PID != cmd.Process.Pid || binding.StartToken == "" {
		t.Fatalf("binding=%+v", binding)
	}
	if canonicalTestPath(t, binding.Binary) != canonicalTestPath(t, executable) {
		t.Fatalf("binary=%q want=%q", binding.Binary, executable)
	}
}
