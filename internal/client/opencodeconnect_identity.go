package client

import (
	"errors"
	"os"
	"path/filepath"
)

// OpencodeConnectIdentity binds the agent shell's own session to the one TUI
// showing it. The session comes only from the cbus server plugin's
// CBUS_SESSION_ID; cwd and recency never select one.
func OpencodeConnectIdentity() (OpencodeConnectBinding, error) {
	sid := os.Getenv("CBUS_SESSION_ID")
	if !validOpencodeSessionID(sid) {
		return OpencodeConnectBinding{}, errors.New("CBUS_SESSION_ID does not hold an OpenCode session; run cbus connect from an OpenCode agent shell with the cbus plugins installed (cbus install-opencode)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return OpencodeConnectBinding{}, err
	}
	if cwd, err = filepath.Abs(cwd); err != nil {
		return OpencodeConnectBinding{}, err
	}
	e, err := discoverOpencodeEndpoint(sid)
	if err != nil {
		return OpencodeConnectBinding{}, err
	}
	return OpencodeConnectBinding{SessionID: sid, Cwd: cwd, Endpoint: e}, nil
}
