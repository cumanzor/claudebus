//go:build windows

package client

import "errors"

func NativeConnectIdentity(CodexConnectOptions) (ConnectRequest, string, error) {
	return ConnectRequest{}, "", errors.New("native cbus connect is unavailable on Windows")
}
