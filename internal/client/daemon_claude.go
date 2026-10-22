package client

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const daemonHarnessClaude = "claude"

const claudeAwaitingReceiptState = "awaiting-receipt"

// Only a clean write with a persisted submission time waits for its receipt;
// ambiguous writes and attempts journaled without that time stay uncertain.
func claudeReceiptWaiting(c *ConnectionState, now time.Time) bool {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		return c.Pending != nil && c.Pending.QueueID == "" && c.Pending.Evidence == nil &&
			!c.Pending.SubmittedAt.IsZero() && now.Before(c.Pending.SubmittedAt.Add(claudeReceiptTimeout))
	default:
		return false
	}
}

// A clean Claude socket write has no ACK; its receipt arrives in the transcript.
func claudeCleanWrite(c *ConnectionState, err error) bool {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		var awaiting *claudeAwaitingReceiptError
		return errors.As(err, &awaiting) && awaiting.State == claudeSubmitted && awaiting.Err == nil
	default:
		return false
	}
}

// Only adapter-specific proof that nothing was submitted may release an attempt.
func provedNotSubmitted(c *ConnectionState, err error) bool {
	switch daemonHarness(c.Harness) {
	case daemonHarnessCodex:
		var rejection *rpcError
		return errors.As(err, &rejection) && rejection.Code == -32600
	case daemonHarnessClaude:
		var rejected *claudeNotSubmittedError
		return errors.As(err, &rejected)
	case daemonHarnessOpencode:
		var rejected *opencodeNotSubmittedError
		return errors.As(err, &rejected)
	default:
		return false
	}
}

func pendingConnectionState(c *ConnectionState, now time.Time) string {
	if claudeReceiptWaiting(c, now) {
		return claudeAwaitingReceiptState
	}
	return "uncertain"
}

func logClaudeAttempt(c *ConnectionState, a queueAttempt, outcome string) {
	if !a.SubmittedAt.IsZero() && outcome != "submitted" {
		outcome += " after " + time.Since(a.SubmittedAt).Round(time.Millisecond).String()
	}
	daemonLogf("%s attempt %s: %s", ConnectionTarget(c), a.ClientID, outcome)
}

// Adapter dispatch is separate from public admission. Claude admission remains
// disabled until its session-side request and credential handoff are integrated.
func validateConnectionAdapter(c *ConnectionState) error {
	switch daemonHarness(c.Harness) {
	case daemonHarnessCodex, daemonHarnessClaude, daemonHarnessOpencode:
		return nil
	default:
		return fmt.Errorf("unsupported connection adapter %q", c.Harness)
	}
}

func validateConnectionBinding(c *ConnectionState) error {
	if err := validateConnectionAdapter(c); err != nil {
		return err
	}
	switch daemonHarness(c.Harness) {
	case daemonHarnessCodex:
		return validateCodexQueueBinding(c.Config)
	case daemonHarnessOpencode:
		if c.Opencode == nil {
			return errors.New("OpenCode connection binding is missing")
		}
		return validateOpencodeBinding(c.ThreadID, c.Opencode.Binding)
	case daemonHarnessClaude:
	default:
		return errUnsupportedHarness(c.Harness)
	}
	if c.Claude == nil {
		return errors.New("Claude connection binding is missing")
	}
	b := c.Claude.Binding
	if !uuidLike(c.ThreadID) || b.SessionID != c.ThreadID ||
		!filepath.IsAbs(b.ConfigHome) || !filepath.IsAbs(b.UserHome) || !filepath.IsAbs(b.Cwd) ||
		!filepath.IsAbs(b.TranscriptPath) || !b.Endpoint.wellFormed() ||
		b.TranscriptIno == 0 || b.Endpoint.PID <= 1 || b.Endpoint.StartToken == "" ||
		c.Claude.ReceiptOffset < 0 || !validClaudeCredentialRef(c.Claude.CredentialRef) {
		return errors.New("Claude connection requires exact session, runtime, transcript and credential bindings")
	}
	return nil
}

func connectionReadyState(c *ConnectionState) string {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude, daemonHarnessOpencode:
		return "socket-ready"
	case daemonHarnessCodex:
		return "queue-ready"
	default:
		return "unsupported"
	}
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

// pinnedConsumer is the consumer incarnation a connection records: the bound
// Claude endpoint, or the Codex CLI the daemon last observed.
func pinnedConsumer(c *ConnectionState) (int, string) {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		if c.Claude == nil {
			return 0, ""
		}
		return c.Claude.Binding.Endpoint.PID, c.Claude.Binding.Endpoint.StartToken
	case daemonHarnessOpencode:
		if c.Opencode == nil {
			return 0, ""
		}
		return c.Opencode.Binding.Endpoint.PID, c.Opencode.Binding.Endpoint.StartToken
	case daemonHarnessCodex:
		if c.Consumer == nil {
			return 0, ""
		}
		return c.Consumer.PID, c.Consumer.StartToken
	default:
		return 0, ""
	}
}
