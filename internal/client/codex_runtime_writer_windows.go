//go:build windows

package client

import (
	"context"
	"errors"
)

func codexRolloutWriters(context.Context, string) ([]codexWriterFD, error) {
	return nil, errors.New("native Codex consumer discovery is unavailable on Windows")
}
func codexProcessFiles(context.Context, int) ([]codexWriterFD, error) {
	return nil, errors.New("native Codex consumer discovery is unavailable on Windows")
}

func managedCodexFrontend(context.Context, *ConnectionState, int) (*codexFrontend, error) {
	return nil, errors.New("native Codex consumer discovery is unavailable on Windows")
}
