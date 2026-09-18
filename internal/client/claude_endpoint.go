package client

import "net"

// claudeEndpoint pins a runtime, not a terminal, cwd, or session name. The caller
// must first establish that PID owns the requesting Claude session. It carries
// no credential. A changed process or socket requires an explicit new binding.
type claudeEndpoint struct {
	Socket     string
	PID        int
	StartToken string
	Dev, Ino   uint64
}

// validateConnected runs before the transport sends the session's auth token.
// Inspecting the pathname alone would leak that token if the socket was replaced
// between inspection and connect. Check both the connected peer and the path.
func (e claudeEndpoint) validateConnected(conn *net.UnixConn) error {
	if err := validateClaudeEndpoint(e); err != nil {
		return err
	}
	if err := validateClaudeSocketPeer(conn, e.PID); err != nil {
		return err
	}
	return validateClaudeEndpoint(e)
}
