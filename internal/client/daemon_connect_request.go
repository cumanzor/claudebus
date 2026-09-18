package client

import "errors"

// The capability exists only in this private, write-only control request.
// ConnectRequest, ClaudeConnectBinding and ConnectionState remain nonsecret.
type connectWireRequest struct {
	ConnectRequest
	ClaudeToken string `json:"claudeToken,omitempty"`
}

func (connectWireRequest) String() string   { return "cbus connect request (capability redacted)" }
func (connectWireRequest) GoString() string { return "cbus connect request (capability redacted)" }

// DaemonConnect sends a session capability only to the private control socket.
// Callers must verify daemon protocol/version compatibility before invoking it.
func DaemonConnect(req ConnectRequest, claudeToken string, out *ConnectionState) error {
	if err := validateConnectEnvelope(req, claudeToken); err != nil {
		return err
	}
	return DaemonCall("POST", "/connect", connectWireRequest{req, claudeToken}, out)
}

func validateConnectEnvelope(req ConnectRequest, token string) error {
	if req.Protocol != 0 && req.Protocol != DaemonProtocolVersion {
		return errors.New("connect protocol is incompatible; use the matching cbus binary and daemon")
	}
	if daemonHarness(req.Harness) == daemonHarnessClaude {
		if req.Protocol != DaemonProtocolVersion || req.Claude == nil || token == "" {
			return errors.New("Claude connect requires protocol 3, an exact caller binding and its private capability")
		}
	} else if req.Claude != nil || token != "" {
		return errors.New("Claude caller binding and capability require the Claude harness")
	}
	return nil
}
