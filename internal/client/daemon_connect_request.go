package client

import (
	"errors"
	"fmt"

	"claudebus/internal/core"
)

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

// connectHostLabel is the host recorded for a connecting session: the label the
// client resolved from its own environment, re-screened because the request is
// input, or the daemon's own label when an older client sends none.
func connectHostLabel(sent string) (string, error) {
	if sent == "" {
		return HostLabel()
	}
	if !core.ValidName(sent) || shortLabel(sent) != sent {
		return "", fmt.Errorf("%w %q in the connect request", ErrBadHostLabel, sent)
	}
	return sent, nil
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
