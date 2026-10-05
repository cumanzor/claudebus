//go:build windows

package client

import (
	"errors"
	"syscall"
	"unsafe"
)

var procGetNamedPipeServerProcessId = syscall.NewLazyDLL("kernel32.dll").NewProc("GetNamedPipeServerProcessId")

// a named pipe has no inode to pin, so the endpoint is the pipe name plus the
// exact claude.exe incarnation, and every connection must be served by it.
func captureClaudeEndpoint(path string, pid int, start string) (claudeEndpoint, error) {
	e := claudeEndpoint{Socket: path, PID: pid, StartToken: start}
	if !validClaudeEndpointName(path) {
		return e, errors.New("Claude endpoint must be a named pipe under \\\\.\\pipe\\")
	}
	return e, validateClaudeEndpoint(e)
}

func validateClaudeEndpoint(e claudeEndpoint) error {
	if !e.wellFormed() || e.PID <= 4 || e.StartToken == "" {
		return errors.New("Claude endpoint lacks an exact process/pipe binding")
	}
	start, err := procStartTime(e.PID)
	if err != nil || start != e.StartToken {
		return errors.New("Claude endpoint owner exited or changed; reconnect from that session")
	}
	return nil
}

func (e claudeEndpoint) wellFormed() bool {
	return validClaudeEndpointName(e.Socket) && e.Dev == 0 && e.Ino == 0
}

// validateClaudeSocketPeer checks the connected pipe's server process before
// the token is written: a pipe name can be squatted by any process that
// creates it first.
func validateClaudeSocketPeer(conn claudeConn, pid int) error {
	var server uint32
	if r, _, _ := procGetNamedPipeServerProcessId.Call(conn.Fd(), uintptr(unsafe.Pointer(&server))); r == 0 {
		return errors.New("Claude pipe server identity could not be read")
	}
	if int(server) != pid {
		return errors.New("Claude pipe is served by a different process")
	}
	return nil
}
