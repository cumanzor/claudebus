//go:build !windows

package client

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"time"
)

type claudeConn = *net.UnixConn

func validClaudeEndpointName(path string) bool { return filepath.IsAbs(path) }

// release closes the connection; ctx cancellation closes it early.
func dialClaude(ctx context.Context, endpoint string, deadline time.Time) (claudeConn, func(), error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, nil, err
	}
	conn := c.(*net.UnixConn)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	release := func() { stop(); _ = conn.Close() }
	if err := conn.SetDeadline(deadline); err != nil {
		release()
		return nil, nil, errors.New("Claude socket deadline failed")
	}
	return conn, release, nil
}
