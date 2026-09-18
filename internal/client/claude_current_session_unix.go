//go:build darwin || linux

package client

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// A PID/socket can survive /clear or /resume. Consult only that PID's current
// registry under the captured config home; historical transcripts prove no
// current ownership. The neighboring native .key file is never accessed.
func validateCurrentClaudeSession(b ClaudeConnectBinding) error {
	invalid := errors.New("current Claude session registry is unavailable, unsafe or inconsistent")
	if !filepath.IsAbs(b.ConfigHome) || b.Endpoint.PID <= 1 || !uuidLike(b.SessionID) {
		return invalid
	}
	prior, err := os.Lstat(b.ConfigHome)
	if err != nil || !claudeRegistryInfo(prior, true) {
		return invalid
	}
	config, err := os.OpenRoot(b.ConfigHome)
	if err != nil {
		return invalid
	}
	defer config.Close()
	opened, err := config.Stat(".")
	if err != nil || !os.SameFile(prior, opened) {
		return invalid
	}
	parent, err := config.Lstat("sessions")
	if err != nil || !claudeRegistryInfo(parent, true) {
		return invalid
	}
	root, err := config.OpenRoot("sessions")
	if err != nil {
		return invalid
	}
	defer root.Close()
	opened, err = root.Stat(".")
	if err != nil || !os.SameFile(parent, opened) {
		return invalid
	}
	name := strconv.Itoa(b.Endpoint.PID) + ".json"
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return invalid
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !claudeRegistryInfo(before, false) || before.Size() <= 0 || before.Size() > 64<<10 {
		return invalid
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || int64(len(data)) != before.Size() {
		return invalid
	}
	after, err := f.Stat()
	current, pathErr := root.Lstat(name)
	currentDir, dirErr := config.Lstat("sessions")
	currentConfig, configErr := os.Lstat(b.ConfigHome)
	if err != nil || pathErr != nil || dirErr != nil || configErr != nil || !os.SameFile(prior, currentConfig) || !claudeRegistryInfo(current, false) || !os.SameFile(before, current) ||
		!os.SameFile(parent, currentDir) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return invalid
	}
	var record struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Kind      string `json:"kind"`
		Socket    string `json:"messagingSocketPath"`
	}
	if json.Unmarshal(data, &record) != nil || record.PID != b.Endpoint.PID || record.Kind != "interactive" || record.Socket != b.Endpoint.Socket || !uuidLike(record.SessionID) {
		return invalid
	}
	// procStart has ps lstart formatting, not the kernel token's representation.
	// Endpoint validation independently fences PID reuse before every live send.
	if record.SessionID != b.SessionID {
		return errClaudeSessionChanged
	}
	return nil
}

func claudeRegistryInfo(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular() && st.Nlink == 1
}
