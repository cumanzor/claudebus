//go:build windows

package client

import (
	"context"
	"errors"
)

func newClaudeQueueContext(context.Context, string, ClaudeConnectionConfig) (nativeQueue, error) {
	return nil, errors.New("Claude native socket delivery is unavailable on Windows")
}
