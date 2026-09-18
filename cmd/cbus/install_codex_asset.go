package main

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const codexSkillReceipt = ".cbus-installed-sha256"

// A receipt records the bytes cbus last installed, rather than comparing an old
// release to the new release and mistaking every legitimate upgrade for an edit.
// Missing/corrupt receipts remain conservative; --force is an explicit override.
func installCodexSkill(dir, name string, content []byte, force bool) assetResult {
	return installTrackedCodexAsset(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, codexSkillReceipt), name, content, force)
}

func installTrackedCodexAsset(dst, receipt, name string, content []byte, force bool) assetResult {
	res := assetResult{name: name, outcome: "failed"}
	for _, path := range []string{dst, receipt} {
		info, err := os.Lstat(path)
		if err == nil && !info.Mode().IsRegular() {
			res.reason = "destination and install receipt must be regular files (symlinks are not followed)"
			return res
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			res.reason = "stat destination: " + err.Error()
			return res
		}
	}
	existing, err := os.ReadFile(dst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		res.reason = "read destination: " + err.Error()
		return res
	}
	wanted := shaHex(content)
	unchanged := err == nil && shaHex(existing) == wanted
	if err == nil && !unchanged && !force {
		prior, err := os.ReadFile(receipt)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			res.reason = "read install receipt: " + err.Error()
			return res
		}
		priorHash := strings.TrimSpace(string(prior))
		decoded, decodeErr := hex.DecodeString(priorHash)
		if err != nil || decodeErr != nil || len(decoded) != 32 || priorHash != shaHex(existing) {
			res.outcome, res.reason = "skipped", "locally edited or no matching install receipt; pass --force to replace deliberately"
			return res
		}
	}
	if !unchanged {
		if err := writeFileAtomic(dst, content); err != nil {
			res.reason = err.Error()
			return res
		}
	}
	// Install content first. If recording it fails, future upgrades fail closed;
	// recording the new hash before content would permit overwriting a local edit.
	if err := writeFileAtomic(receipt, []byte(wanted+"\n")); err != nil {
		res.reason = "content installed but receipt write failed: " + err.Error()
		return res
	}
	res.outcome = "installed"
	if unchanged {
		res.outcome = "up-to-date"
	}
	return res
}
