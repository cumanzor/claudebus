//go:build windows

package client

import "errors"

func prepareClaudeAdmission(ConnectRequest, string) (*ClaudeConnectionConfig, error) {
	return nil, errors.New("Claude native admission is unavailable on Windows")
}

func (*busDaemon) persistClaudeCapability(*ClaudeConnectionConfig, string) error {
	return errors.New("Claude native admission is unavailable on Windows")
}

func (*busDaemon) reconnectClaude(*ConnectionState, ConnectRequest, *ClaudeConnectionConfig, string) (*ConnectionState, error) {
	return nil, errors.New("Claude native admission is unavailable on Windows")
}
