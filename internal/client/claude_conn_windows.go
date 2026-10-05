package client

import (
	"context"
	"os"
	"regexp"
	"syscall"
	"time"
)

// Claude Code's inbox on windows is a named pipe, by default
// \\.\pipe\LOCAL\cc-msg-<32 hex>; --messaging-socket-path may name another.
type claudeConn = *os.File

var claudePipeName = regexp.MustCompile(`^\\\\\.\\pipe\\(?:LOCAL\\)?[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)

func validClaudeEndpointName(path string) bool { return claudePipeName.MatchString(path) }

var procCancelIoEx = syscall.NewLazyDLL("kernel32.dll").NewProc("CancelIoEx")

// a pipe opened by os.OpenFile takes no deadline, so expiry cancels its
// pending I/O instead (CancelIoEx reaches I/O blocked in another goroutine).
func dialClaude(ctx context.Context, endpoint string, deadline time.Time) (claudeConn, func(), error) {
	f, err := os.OpenFile(endpoint, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	expire, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(expire, func() { procCancelIoEx.Call(f.Fd(), 0) })
	release := func() {
		stop()
		cancel()
		_ = f.Close()
	}
	return f, release, nil
}
