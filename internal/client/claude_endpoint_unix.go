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
	info, err := privateSocket(path, "Claude")
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
	info, err := privateSocket(e.Socket, "Claude")
	if err != nil {
		return err
	}
	dev, ino, _, ok := statIdentity(info)
	if !ok || dev != e.Dev || ino != e.Ino {
		return errors.New("Claude endpoint socket was replaced; reconnect from that session")
	}
	return nil
}

func privateSocket(path, what string) (os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("%s endpoint must be an absolute clean socket path", what)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s endpoint: %w", what, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 ||
		st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%s endpoint must be a private socket owned by the current user", what)
	}
	// Canonicalize trusted OS aliases such as /tmp -> /private/tmp, then require
	// the immediate socket directory to be owner-controlled. The connected peer
	// check still fences a pathname race after this observation.
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("inspect %s socket directory: %w", what, err)
	}
	parent, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect %s socket directory: %w", what, err)
	}
	owner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || !parent.IsDir() || owner.Uid != uint32(os.Geteuid()) || parent.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("%s socket directory must be owned by the current user and not writable by others", what)
	}
	return info, nil
}

// wellFormed: an absolute socket path pinned to the inode captured at bind time.
func (e claudeEndpoint) wellFormed() bool { return filepath.IsAbs(e.Socket) && e.Ino != 0 }
