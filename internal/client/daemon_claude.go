package client

import (
	"errors"
	"fmt"
	"path/filepath"
)

const daemonHarnessClaude = "claude"

// Adapter dispatch is separate from public admission. Claude admission remains
// disabled until its session-side request and credential handoff are integrated.
func validateConnectionAdapter(c *ConnectionState) error {
	switch daemonHarness(c.Harness) {
	case daemonHarnessCodex, daemonHarnessClaude:
		return nil
	default:
		return fmt.Errorf("unsupported connection adapter %q", c.Harness)
	}
}

func validateConnectionBinding(c *ConnectionState) error {
	if err := validateConnectionAdapter(c); err != nil {
		return err
	}
	if daemonHarness(c.Harness) == daemonHarnessCodex {
		return validateCodexQueueBinding(c.Config)
	}
	if c.Claude == nil {
		return errors.New("Claude connection binding is missing")
	}
	b := c.Claude.Binding
	if !uuidLike(c.ThreadID) || b.SessionID != c.ThreadID ||
		!filepath.IsAbs(b.ConfigHome) || !filepath.IsAbs(b.UserHome) || !filepath.IsAbs(b.Cwd) ||
		!filepath.IsAbs(b.TranscriptPath) || !filepath.IsAbs(b.Endpoint.Socket) ||
		b.TranscriptIno == 0 || b.Endpoint.PID <= 1 || b.Endpoint.StartToken == "" || b.Endpoint.Ino == 0 ||
		c.Claude.ReceiptOffset < 0 || !validClaudeCredentialRef(c.Claude.CredentialRef) {
		return errors.New("Claude connection requires exact session, runtime, transcript and credential bindings")
	}
	return nil
}

func connectionReadyState(c *ConnectionState) string {
	if daemonHarness(c.Harness) == daemonHarnessClaude {
		return "socket-ready"
	}
	return "queue-ready"
}

func observeClaudeConsumer(c *ConnectionState) (consumerProbe, error) {
	unknown := consumerProbe{State: "unknown"}
	if err := validateConnectionBinding(c); err != nil {
		return unknown, err
	}
	b := c.Claude.Binding
	exited, err := codexOwnerExited(b.Endpoint.PID, b.Endpoint.StartToken)
	if err != nil {
		return unknown, err
	}
	if exited {
		return consumerProbe{State: "exited", Detail: "bound Claude runtime exited; reconnect from the resumed session"}, nil
	}
	if err := validateClaudeEndpoint(b.Endpoint); err != nil {
		return unknown, err
	}
	if err := validateCurrentClaudeSession(b); err != nil {
		if errors.Is(err, errClaudeSessionChanged) {
			return consumerProbe{State: "exited", Detail: err.Error()}, nil
		}
		return unknown, err
	}
	f, err := openBoundClaudeTranscript(b)
	if err != nil {
		return unknown, err
	}
	f.Close()
	return consumerProbe{State: "online", PID: b.Endpoint.PID, StartToken: b.Endpoint.StartToken,
		Detail: "exact bound Claude process, socket and transcript observed"}, nil
}
