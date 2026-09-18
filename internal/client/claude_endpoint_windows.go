//go:build windows

package client

import (
	"errors"
	"net"
)

func captureClaudeEndpoint(string, int, string) (claudeEndpoint, error) {
	return claudeEndpoint{}, errors.New("native Claude connections are not supported on Windows")
}
func validateClaudeEndpoint(claudeEndpoint) error {
	return errors.New("native Claude connections are not supported on Windows")
}
func validateClaudeSocketPeer(*net.UnixConn, int) error {
	return errors.New("native Claude connections are not supported on Windows")
}
