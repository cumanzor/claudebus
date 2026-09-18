//go:build linux

package client

import (
	"errors"
	"net"
	"os"
	"syscall"
)

func validateClaudeSocketPeer(conn *net.UnixConn, expectedPID int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var cred *syscall.Ucred
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		cred, peerErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return err
	}
	if peerErr != nil {
		return peerErr
	}
	if cred.Pid != int32(expectedPID) || cred.Uid != uint32(os.Geteuid()) {
		return errors.New("Claude socket peer does not match the bound process and user")
	}
	return nil
}
