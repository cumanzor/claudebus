//go:build linux

package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func currentCodexRuntimeBinding() (codexRuntimeBinding, error) {
	return codexRuntimeWitness(os.Getppid(), procLookup(), inspectCodexRuntime)
}

func inspectCodexRuntime(pid int) (codexRuntimeBinding, error) {
	before, err := procStartTime(pid)
	if err != nil {
		return codexRuntimeBinding{}, fmt.Errorf("inspect current Codex process: %w", err)
	}
	base := filepath.Join("/proc", strconv.Itoa(pid))
	binary, err := os.Readlink(filepath.Join(base, "exe"))
	if err != nil {
		return codexRuntimeBinding{}, fmt.Errorf("inspect current Codex executable: %w", err)
	}
	entries, err := os.ReadDir(filepath.Join(base, "fd"))
	if err != nil {
		return codexRuntimeBinding{}, fmt.Errorf("inspect current Codex open queue store (process inspection may require normal command approval): %w", err)
	}
	var paths []string
	for _, entry := range entries {
		path, err := os.Readlink(filepath.Join(base, "fd", entry.Name()))
		if err == nil {
			paths = append(paths, path)
		}
	}
	home, err := codexQueueHome(paths)
	if err != nil {
		return codexRuntimeBinding{}, err
	}
	after, err := procStartTime(pid)
	if err != nil || after != before {
		return codexRuntimeBinding{}, fmt.Errorf("current Codex process changed during queue inspection")
	}
	return codexRuntimeBinding{Binary: binary, SQLiteHome: home, PID: pid, StartToken: before}, nil
}
