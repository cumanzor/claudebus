package client

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStatIsZombie(t *testing.T) {
	for stat, want := range map[string]bool{
		"12 (codex) Z 1 12":   true,
		"12 (codex) S 1 12":   false,
		"12 (a) Z (b) S 1 12": false,
		"12 (a) S (b) Z 1 12": true,
		"12 (Z) R 1 12":       false,
		"no parens here Z 1":  false,
	} {
		if got := statIsZombie(stat); got != want {
			t.Errorf("statIsZombie(%q) = %v, want %v", stat, got, want)
		}
	}
}

// an unreaped child named codex is what a Codex app-server leaves behind
func TestCodexScanSkipsZombie(t *testing.T) {
	src, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true not on PATH")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
	pid := cmd.Process.Pid
	schedulerWait(t, "child becomes a zombie", func() bool { return procIsZombie(pid) })

	rollout := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(rollout, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := codexRolloutWriters(context.Background(), rollout)
	if err != nil {
		t.Fatalf("scan failed on a zombie: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("writers = %v, want none", found)
	}
	if files, err := codexProcessFiles(context.Background(), pid); err != nil || len(files) != 0 {
		t.Fatalf("zombie files = %v, %v", files, err)
	}
}

// a live process that cannot be read might be the writer, so it stays an error
func TestCodexScanKeepsLivePermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read every process")
	}
	if procIsZombie(1) {
		t.Skip("pid 1 is not live")
	}
	if _, err := os.ReadDir("/proc/1/fd"); !os.IsPermission(err) {
		t.Skipf("pid 1 descriptors are readable here: %v", err)
	}
	if _, err := codexProcessFiles(context.Background(), 1); !os.IsPermission(err) {
		t.Fatalf("live unreadable process: err = %v, want a permission error", err)
	}
}
