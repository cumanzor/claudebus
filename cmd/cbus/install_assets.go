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
		out = append(out, installAsset(filepath.Join(dstDir, e.Name()), e.Name(), content, force))
	}
	return out, nil
}

func installAsset(dst, name string, content []byte, force bool) assetResult {
	res := assetResult{name: name}
	if existing, err := os.ReadFile(dst); err == nil {
		if shaHex(existing) == shaHex(content) {
			res.outcome = "up-to-date"
			return res
		}
		if !force {
			res.outcome, res.reason = "skipped", "differs from shipped (locally edited?) — pass --force to overwrite"
			return res
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		res.outcome, res.reason = "failed", "read dest: "+err.Error()
		return res
	}
	if err := writeFileAtomic(dst, content); err != nil {
		res.outcome, res.reason = "failed", err.Error()
		return res
	}
	res.outcome = "installed"
	return res
}

// installCodexSkills installs each immediate skill directory's SKILL.md and a
// receipt for safe upgrades. Other embedded files/deeper assets are not installed.
func installCodexSkills(fsys fs.FS, dstDir string, force bool) ([]assetResult, error) {
	const subdir = "skills/codex"
	entries, err := fs.ReadDir(fsys, subdir)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", subdir, err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dstDir, err)
	}
	var out []assetResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		res := assetResult{name: name + "/SKILL.md", outcome: "failed"}
		if name == "." || !fs.ValidPath(name) || !filepath.IsLocal(name) || strings.ContainsAny(name, `/\`) {
			res.reason = "invalid embedded skill directory name"
			out = append(out, res)
			continue
		}
		content, err := fs.ReadFile(fsys, subdir+"/"+name+"/SKILL.md")
		if err != nil {
			res.reason = "read embed: " + err.Error()
			out = append(out, res)
			continue
		}
		skillDir := filepath.Join(dstDir, name)
		if info, err := os.Lstat(skillDir); err == nil {
			if !info.IsDir() {
				res.reason = "destination skill path is not a directory (symlinks are not followed)"
				out = append(out, res)
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			res.reason = "stat dest: " + err.Error()
			out = append(out, res)
			continue
		}
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			res.reason = "mkdir dest: " + err.Error()
			out = append(out, res)
			continue
		}
		out = append(out, installCodexSkill(skillDir, res.name, content, force))
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

func defaultCodexSkillsDir() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "skills"), nil
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

func runInstallCodexSkills(args []string) int {
	const use = "usage: cbus install-codex-skills [--path DIR] [--force] [--with-permissions]"
	p, err := splitVerbArgs(args, map[string]bool{"--path": true}, map[string]bool{"--force": true, "--with-permissions": true}, true)
	if err != nil {
		return die("%v (%s)", err, use)
	}
	if err := noExtra(p.pos, 0, use); err != nil {
		return die("%v", err)
	}
	dir, hasPath := p.has("--path")
	if hasPath && dir == "" {
		return die("--path: value must not be empty")
	}
	if dir == "" {
		if dir, err = defaultCodexSkillsDir(); err != nil {
			return die("resolve Codex skills dir: %v", err)
		}
	}
	results, err := installCodexSkills(claudebus.CodexSkills, dir, p.flags["--force"])
	if err != nil {
		return die("%v", err)
	}
	if code := reportAssets("Codex skills", dir, results); code != 0 {
		return code
	}
	if p.flags["--with-permissions"] {
		// Rules belong to the active Codex home even with a custom skills path.
		// Skill --force never authorizes overwriting locally edited permissions.
		return runCodexPermissions([]string{"--scope", "bus", "--install"})
	}
	return 0
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
