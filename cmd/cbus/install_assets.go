package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"claudebus"
	"claudebus/internal/client"
)

// assetResult is one file's fate during an install, so every outcome — including a
// skip — is reported per file at the terminal (D27/S7): a best-effort refresh that
// silently no-ops reads as a clean install, and it must not.
type assetResult struct {
	name    string
	outcome string // installed | up-to-date | skipped | failed
	reason  string
}

// installAssets writes every <subdir>/*.md from the embedded FS into dstDir, sha-
// guarded: an unchanged file is left alone, a locally-edited one is skipped unless
// force, a fresh one is written. It reports per file and never aborts the batch on a
// single file's problem — one edited command must not stop the other four installing.
func installAssets(fsys fs.FS, subdir, dstDir string, force bool) ([]assetResult, error) {
	entries, err := fs.ReadDir(fsys, subdir)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", subdir, err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dstDir, err)
	}
	var out []assetResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		res := assetResult{name: e.Name()}
		content, rerr := fs.ReadFile(fsys, subdir+"/"+e.Name())
		if rerr != nil {
			res.outcome, res.reason = "failed", "read embed: "+rerr.Error()
			out = append(out, res)
			continue
		}
		dst := filepath.Join(dstDir, e.Name())
		if existing, err := os.ReadFile(dst); err == nil {
			if shaHex(existing) == shaHex(content) {
				res.outcome = "up-to-date"
				out = append(out, res)
				continue
			}
			if !force {
				res.outcome, res.reason = "skipped", "differs from shipped (locally edited?) — pass --force to overwrite"
				out = append(out, res)
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			res.outcome, res.reason = "failed", "read dest: "+err.Error()
			out = append(out, res)
			continue
		}
		if werr := writeFileAtomic(dst, content); werr != nil {
			res.outcome, res.reason = "failed", werr.Error()
			out = append(out, res)
			continue
		}
		res.outcome = "installed"
		out = append(out, res)
	}
	return out, nil
}

// writeFileAtomic writes via a sibling temp + rename in the SAME directory, so a
// reader sees the old or the new file, never a torn one, and a failed write leaves
// the existing file intact (S3, applied to asset files too).
func writeFileAtomic(dst string, content []byte) error {
	dir := filepath.Dir(dst)
	tmp := filepath.Join(dir, "."+filepath.Base(dst)+".tmp."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// reportAssets prints each file's outcome and returns the exit code: non-zero when
// any file was skipped or failed, so a caller (or a script) sees the install was
// incomplete, while the safe files still landed.
func reportAssets(label, dstDir string, results []assetResult) int {
	skipped, failed := 0, 0
	for _, r := range results {
		switch r.outcome {
		case "installed", "up-to-date":
			fmt.Printf("  %-24s %s\n", r.name, r.outcome)
		case "skipped":
			skipped++
			fmt.Printf("  %-24s SKIPPED — %s\n", r.name, r.reason)
		default:
			failed++
			fmt.Printf("  %-24s FAILED — %s\n", r.name, r.reason)
		}
	}
	fmt.Printf("%s -> %s (%d file(s))\n", label, dstDir, len(results))
	if skipped+failed > 0 {
		return 1
	}
	return 0
}

func defaultCommandsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "commands"), nil
}

// defaultRolesDir is $CBUS_DIR/roles — the LoadRole fallback searched when a spawn
// runs outside the repo.
func defaultRolesDir() string {
	return filepath.Join(client.CBUSDir(), "roles")
}

func runInstallCommands(args []string) int {
	const use = "usage: cbus install-commands [--path DIR] [--force]"
	dir, force, err := parseInstallArgs(args, use)
	if err != nil {
		return die("%v", err)
	}
	if dir == "" {
		if dir, err = defaultCommandsDir(); err != nil {
			return die("resolve home dir: %v", err)
		}
	}
	results, err := installAssets(claudebus.Commands, "commands", dir, force)
	if err != nil {
		return die("%v", err)
	}
	return reportAssets("commands", dir, results)
}

func runInstallRoles(args []string) int {
	const use = "usage: cbus install-roles [--path DIR] [--force]"
	dir, force, err := parseInstallArgs(args, use)
	if err != nil {
		return die("%v", err)
	}
	if dir == "" {
		dir = defaultRolesDir()
	}
	results, err := installAssets(claudebus.Roles, "roles", dir, force)
	if err != nil {
		return die("%v", err)
	}
	return reportAssets("roles", dir, results)
}

// parseInstallArgs handles the shared [--path DIR] [--force].
func parseInstallArgs(args []string, use string) (dir string, force bool, err error) {
	p, perr := splitVerbArgs(args, map[string]bool{"--path": true}, map[string]bool{"--force": true}, true)
	if perr != nil {
		return "", false, fmt.Errorf("%v (%s)", perr, use)
	}
	if e := noExtra(p.pos, 0, use); e != nil {
		return "", false, e
	}
	if v, ok := p.has("--path"); ok {
		if v == "" {
			return "", false, fmt.Errorf("--path: value must not be empty")
		}
		dir = v
	}
	return dir, p.flags["--force"], nil
}
