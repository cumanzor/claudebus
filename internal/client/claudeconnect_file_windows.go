//go:build windows

package client

import (
	"errors"
	"os"
)

func openClaudeTranscript(string) (*os.File, error) {
	return nil, errors.New("native Claude connections are not supported on Windows")
}

func ClaudeConnectIdentity() (ClaudeConnectBinding, error) {
	return ClaudeConnectBinding{}, errors.New("Claude native messaging is not supported on Windows")
}
