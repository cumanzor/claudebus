package client

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CodexConnectOptions supplies the exact local queue store when the owning CLI
// is detached or process inspection is unavailable. It does not select a thread.
type CodexConnectOptions struct{ SQLiteHome string }

type codexRuntimeBinding struct {
	Binary     string
	SQLiteHome string
	PID        int
	StartToken string
}

// codexQueueHome accepts the main queue database, never a similarly named
// rollout, WAL, or history database. Multiple stores are ambiguous.
func codexQueueHome(paths []string) (string, error) {
	roots := map[string]bool{}
	for _, path := range paths {
		if filepath.Base(path) != "queue_1.sqlite" || !filepath.IsAbs(path) {
			continue
		}
		home, err := canonicalCodexDirectory(filepath.Dir(path), "runtime SQLite home")
		if err != nil {
			return "", err
		}
		roots[home] = true
	}
	if len(roots) > 1 {
		return "", fmt.Errorf("current Codex process has more than one open queue store; supply --codex-sqlite-home with the exact recipient store")
	}
	for home := range roots {
		return home, nil
	}
	return "", nil
}

func canonicalCodexDirectory(path, label string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", label)
	}
	return canonical, nil
}

func validateCodexQueueHome(home string) error {
	info, err := os.Stat(filepath.Join(home, "queue_1.sqlite"))
	if err != nil {
		return fmt.Errorf("read existing Codex queue store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Codex queue store is not a regular file")
	}
	file, err := os.Open(filepath.Join(home, "queue_1.sqlite"))
	if err != nil {
		return fmt.Errorf("read existing Codex queue store: %w", err)
	}
	defer file.Close()
	header := make([]byte, 16)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("read Codex queue database header: %w", err)
	}
	if string(header) != "SQLite format 3\x00" {
		return fmt.Errorf("existing Codex queue store has an invalid SQLite header; refusing to start a sidecar against it")
	}
	return nil
}

// Walk only the caller's ancestry. The first native Codex process owns this
// shell; never fall through to an outer, unrelated Codex session's store.
func codexRuntimeWitness(start int, lookup func(int) (procRecord, bool), inspect func(int) (codexRuntimeBinding, error)) (codexRuntimeBinding, error) {
	var previous procRecord
	for depth, pid := 0, start; depth < maxWalkDepth && pid > procWalkRoot; depth++ {
		rec, ok := lookup(pid)
		if !ok {
			return codexRuntimeBinding{}, fmt.Errorf("cannot inspect caller process ancestry (process inspection may require normal command approval)")
		}
		if depth > 0 && !ancestryPlausible(previous, rec) {
			return codexRuntimeBinding{}, fmt.Errorf("caller process ancestry changed during inspection")
		}
		if strings.EqualFold(commBase(rec.Comm), "codex") || strings.EqualFold(commBase(rec.Comm), "codex.exe") {
			return inspect(pid)
		}
		previous, pid = rec, rec.PPid
	}
	return codexRuntimeBinding{}, nil
}
