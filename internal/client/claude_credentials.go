//go:build darwin || linux

package client

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

const claudeCredentialDir = "claude-credentials"
const claudeCredentialMaxBytes = 4096

// The caller must generate a fresh random binding UUID for each runtime epoch.
// References are immutable and contain no token; binding state separately pins
// each reference to its exact endpoint and session. No daemon path is activated.
func storeClaudeCredential(daemonRoot, bindingUUID, token string) (string, error) {
	ref := bindingUUID + ".token"
	if !validClaudeCredentialRef(ref) || !validClaudeCredentialToken(token) {
		return "", errors.New("invalid Claude credential input")
	}
	root, err := openClaudeCredentialRoot(daemonRoot, true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.OpenFile(ref, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", errors.New("Claude credential exclusive creation failed")
	}
	defer f.Close()
	info, err := f.Stat()
	pathInfo, pathErr := root.Lstat(ref)
	if err != nil || pathErr != nil || !privateClaudeCredentialInfo(info, false) || !privateClaudeCredentialInfo(pathInfo, false) || !os.SameFile(info, pathInfo) {
		return "", errors.New("Claude credential file identity is invalid")
	}
	if n, err := f.WriteString(token); err != nil || n != len(token) {
		return "", errors.New("Claude credential write failed")
	}
	if err := f.Sync(); err != nil {
		return "", errors.New("Claude credential sync failed")
	}
	if err := f.Close(); err != nil {
		return "", errors.New("Claude credential close failed")
	}
	if err := syncClaudeCredentialRoot(root); err != nil {
		return "", errors.New("Claude credential directory sync failed")
	}
	return ref, nil
}

// Tokens are plain return values, never status fields or formatted structs.
// Errors deliberately exclude underlying path, OS, and content error strings.
func readClaudeCredential(daemonRoot, ref string) (string, error) {
	if !validClaudeCredentialRef(ref) {
		return "", errors.New("invalid Claude credential reference")
	}
	root, err := openClaudeCredentialRoot(daemonRoot, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	prior, err := root.Lstat(ref)
	if err != nil || !privateClaudeCredentialInfo(prior, false) || prior.Size() <= 0 || prior.Size() > claudeCredentialMaxBytes {
		return "", errors.New("Claude credential file is unavailable or invalid")
	}
	// Root bounds traversal but may follow in-root symlinks. Check both the
	// opened inode and final Lstat before reading; NONBLOCK also fences a FIFO
	// replacement from hanging the open before its type can be validated.
	f, err := root.OpenFile(ref, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("Claude credential open failed")
	}
	defer f.Close()
	opened, openErr := f.Stat()
	current, currentErr := root.Lstat(ref)
	if openErr != nil || currentErr != nil || !privateClaudeCredentialInfo(opened, false) || !privateClaudeCredentialInfo(current, false) || !os.SameFile(prior, opened) || !os.SameFile(opened, current) || opened.Size() != prior.Size() {
		return "", errors.New("Claude credential file identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, claudeCredentialMaxBytes+1))
	if err != nil || int64(len(data)) != opened.Size() || !validClaudeCredentialToken(string(data)) {
		return "", errors.New("Claude credential read failed or content is invalid")
	}
	return string(data), nil
}

func validClaudeCredentialRef(ref string) bool {
	id, ok := strings.CutSuffix(ref, ".token")
	return ok && uuidLike(id) && id == strings.ToLower(id)
}

func validClaudeCredentialToken(token string) bool {
	return len(token) > 0 && len(token) <= claudeCredentialMaxBytes && utf8.ValidString(token) && !strings.ContainsAny(token, "\r\n\x00")
}

func privateClaudeCredentialInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0o700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && stat.Nlink == 1
}

// OpenRoot anchors all later operations to descriptors. Refuse a symlink or
// replacement at either directory capture; do not repair insecure permissions.
func openClaudeCredentialRoot(path string, create bool) (*os.Root, error) {
	prior, err := os.Lstat(path)
	if !filepath.IsAbs(path) || err != nil || !privateClaudeCredentialInfo(prior, true) {
		return nil, errors.New("Claude credential daemon directory is invalid")
	}
	parent, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.New("Claude credential daemon directory open failed")
	}
	defer parent.Close()
	opened, openErr := parent.Stat(".")
	current, currentErr := os.Lstat(path)
	if openErr != nil || currentErr != nil || !privateClaudeCredentialInfo(opened, true) || !privateClaudeCredentialInfo(current, true) || !os.SameFile(prior, opened) || !os.SameFile(opened, current) {
		return nil, errors.New("Claude credential daemon directory identity changed")
	}
	if create {
		if err := parent.Mkdir(claudeCredentialDir, 0o700); err != nil && !os.IsExist(err) {
			return nil, errors.New("Claude credential directory creation failed")
		}
		if err := syncClaudeCredentialRoot(parent); err != nil {
			return nil, errors.New("Claude credential parent sync failed")
		}
	}
	prior, err = parent.Lstat(claudeCredentialDir)
	if err != nil || !privateClaudeCredentialInfo(prior, true) {
		return nil, errors.New("Claude credential directory is invalid")
	}
	root, err := parent.OpenRoot(claudeCredentialDir)
	if err != nil {
		return nil, errors.New("Claude credential directory open failed")
	}
	opened, openErr = root.Stat(".")
	current, currentErr = parent.Lstat(claudeCredentialDir)
	if openErr != nil || currentErr != nil || !privateClaudeCredentialInfo(opened, true) || !privateClaudeCredentialInfo(current, true) || !os.SameFile(prior, opened) || !os.SameFile(opened, current) {
		root.Close()
		return nil, errors.New("Claude credential directory identity changed")
	}
	return root, nil
}

func syncClaudeCredentialRoot(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
