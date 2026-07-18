package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	updateCheckTTL    = 24 * time.Hour
	updateCheckBudget = 5 * time.Second
	updateCheckSubcmd = "__update-check"
)

type updateCheckCache struct {
	CheckedAt   time.Time `json:"checked_at"`
	LatestKnown string    `json:"latest_known"`
}

func updateCheckCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cbus", "update-check.json")
}

// maybeStartUpdateCheck runs on every invocation. Opt-in via CBUS_UPDATE_CHECK=1.
// Synchronously: if the cache already knows a newer STABLE release, print one stderr
// hint. Asynchronously: when the cache is missing or stale, spawn a detached poll
// that outlives this process. Everything here is best-effort and silent on error — a
// version hint must never break or slow the command the user actually ran.
//
// Skipped for --json (scripts see no chatter), for selfupdate (it does its own
// check), for hook-exit (the SessionEnd hook stays quiet), and for the hidden refresh
// subcommand (no recursion).
func maybeStartUpdateCheck(cmd string, jsonMode bool) {
	if os.Getenv("CBUS_UPDATE_CHECK") != "1" || jsonMode {
		return
	}
	switch cmd {
	case "selfupdate", "hook-exit", updateCheckSubcmd, "--version", "version":
		return
	}
	if _, ok := resolveRepoSlug(); !ok {
		return // no repo configured — nothing to check against
	}
	path := updateCheckCachePath()
	if path == "" {
		return
	}
	cache, _ := readUpdateCheckCache(path)
	if cache != nil && newerRelease(cache.LatestKnown, version) {
		fmt.Fprintf(os.Stderr, "cbus: note: %s available — run 'cbus selfupdate'\n", normalizeVersion(cache.LatestKnown))
	}
	if cache != nil && time.Since(cache.CheckedAt) <= updateCheckTTL {
		return
	}
	spawnDetachedUpdateCheck()
}

// spawnDetachedUpdateCheck execs `cbus __update-check` detached so it can finish
// writing the cache after the primary command has already returned.
func spawnDetachedUpdateCheck() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, updateCheckSubcmd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detachProcess(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// cmdUpdateCheckRefresh is the hidden subcommand: poll gh, write the cache, exit.
// Always 0 — a failed check is no worse than a missing one; next time retries.
func cmdUpdateCheckRefresh() int {
	if path := updateCheckCachePath(); path != "" {
		refreshUpdateCache(path)
	}
	return 0
}

func readUpdateCheckCache(path string) (*updateCheckCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c updateCheckCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func writeUpdateCheckCache(path string, c updateCheckCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// write+rename so a concurrent reader never sees a partial file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func refreshUpdateCache(path string) {
	slug, ok := resolveRepoSlug()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckBudget)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "release", "view", "--repo", slug, "--json", "tagName").Output()
	if err != nil {
		return
	}
	var resp struct {
		TagName string `json:"tagName"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.TagName == "" {
		return
	}
	_ = writeUpdateCheckCache(path, updateCheckCache{CheckedAt: time.Now().UTC(), LatestKnown: resp.TagName})
}

// hasJSONFlag reports whether --json / -json appears before a "--" terminator.
func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" || a == "-json" {
			return true
		}
	}
	return false
}
