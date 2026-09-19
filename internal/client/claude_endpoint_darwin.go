//go:build darwin

package client

import (
	"errors"
	"net"
	"syscall"
)

func validateClaudeSocketPeer(conn *net.UnixConn, expectedPID int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var pid int
	var peerErr error
	// SOL_LOCAL / LOCAL_PEERPID from Darwin's sys/un.h. The kernel reports the
	// process that owns the connected listener, independent of its pathname.
	err = raw.Control(func(fd uintptr) { pid, peerErr = syscall.GetsockoptInt(int(fd), 0, 2) })
	if err != nil {
		return err
	}
	if peerErr != nil {
		return peerErr
	}
	if pid != expectedPID {
		return errors.New("Claude socket peer does not match the bound process")
	}
	return nil
}
