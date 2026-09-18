//go:build windows

package client

import "errors"

func validateCurrentClaudeSession(ClaudeConnectBinding) error {
	return errors.New("Claude current-session observation is unavailable on Windows")
}
