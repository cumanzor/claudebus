//go:build darwin || linux

package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func captureClaudeEndpoint(path string, pid int, start string) (claudeEndpoint, error) {
	e := claudeEndpoint{Socket: path, PID: pid, StartToken: start}
	info, err := privateClaudeSocket(path)
	if err != nil {
		return e, err
	}
	e.Dev, e.Ino, _, _ = statIdentity(info)
	return e, validateClaudeEndpoint(e)
}

func validateClaudeEndpoint(e claudeEndpoint) error {
	if e.PID <= 1 || e.StartToken == "" || e.Ino == 0 {
		return errors.New("Claude endpoint lacks an exact process/socket binding")
	}
	start, err := procStartTime(e.PID)
	if err != nil || start != e.StartToken || procZombie(e.PID) {
		return errors.New("Claude endpoint owner exited or changed; reconnect from that session")
	}
	info, err := privateClaudeSocket(e.Socket)
	if err != nil {
		return err
	}
	dev, ino, _, ok := statIdentity(info)
	if !ok || dev != e.Dev || ino != e.Ino {
		return errors.New("Claude endpoint socket was replaced; reconnect from that session")
	}
	return nil
}

func privateClaudeSocket(path string) (os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("Claude endpoint must be an absolute clean socket path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect Claude endpoint: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 ||
		st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("Claude endpoint must be a private socket owned by the current user")
	}
	// Canonicalize trusted OS aliases such as /tmp -> /private/tmp, then require
	// the immediate socket directory to be owner-controlled. The connected peer
	// check still fences a pathname race after this observation.
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("inspect Claude socket directory: %w", err)
	}
	parent, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect Claude socket directory: %w", err)
	}
	owner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || !parent.IsDir() || owner.Uid != uint32(os.Geteuid()) || parent.Mode().Perm()&0022 != 0 {
		return nil, errors.New("Claude socket directory must be owned by the current user and not writable by others")
	}
	return info, nil
}
