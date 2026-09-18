//go:build darwin || linux

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminateCodexTUIAllowsLauncherToForwardSignal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-pid")
	command := exec.Command("sh", "-c", `sleep 60 &
child=$!
trap 'kill -TERM "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 0' TERM
echo "$child" > "$1"
wait "$child"`, "shim", path)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	var nativePID int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			nativePID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if nativePID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if nativePID == 0 {
		t.Fatal("launcher did not publish child pid")
	}
	if err := terminateCodexTUI(command.Process); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launcher could not forward and reap child: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("launcher did not terminate")
	}
	if pidAlive(nativePID) && !procZombie(nativePID) {
		t.Fatalf("native child %d survived launcher teardown", nativePID)
	}
}
