package client

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	daemonHarnessCodex    = "codex"
	daemonHarnessOpencode = "opencode"
)

// Journals and requests predating the discriminator always represented Codex.
// Keep this compatibility rule explicit; unknown harnesses must never fall
// through to Codex identity, queue, transcript, or runtime inspection.
func daemonHarness(harness string) string {
	if harness == "" {
		return daemonHarnessCodex
	}
	return harness
}

func validateDaemonHarness(harness string) error {
	switch daemonHarness(harness) {
	case daemonHarnessCodex, daemonHarnessClaude:
		return nil
	case daemonHarnessOpencode:
		return errors.New("the OpenCode adapter is not available in this cbus version")
	default:
		return fmt.Errorf("unsupported daemon harness %q; only codex and claude are available", harness)
	}
}

func errUnsupportedHarness(harness string) error {
	return fmt.Errorf("unsupported daemon harness %q", harness)
}

// Guard the current adapter entry points independently of request admission:
// a later adapter must supply its own behavior, not borrow Codex assumptions.
func requireCodexConnection(c *ConnectionState) error {
	switch daemonHarness(c.Harness) {
	case daemonHarnessCodex:
		return nil
	default:
		return fmt.Errorf("Codex adapter does not support daemon harness %q", c.Harness)
	}
}

// validThreadID checks the session identifier form each harness persists.
func validThreadID(harness, id string) bool {
	switch daemonHarness(harness) {
	case daemonHarnessCodex, daemonHarnessClaude:
		return uuidLike(id)
	case daemonHarnessOpencode:
		return validOpencodeSessionID(id)
	default:
		return false
	}
}

// OpenCode session IDs are "ses_" followed by an ascending time-hex and base62
// tail, 26 characters in 1.19.3; accept a bounded alphanumeric tail.
func validOpencodeSessionID(id string) bool {
	tail, ok := strings.CutPrefix(id, "ses_")
	if !ok || len(tail) < 20 || len(tail) > 64 {
		return false
	}
	for _, c := range tail {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// consumerProbeInterval spaces probes by their cost per harness.
func consumerProbeInterval(c *ConnectionState) time.Duration {
	switch daemonHarness(c.Harness) {
	case daemonHarnessCodex:
		return 30 * time.Second // each probe runs lsof, and every pass now visits every connection
	default:
		return 5 * time.Second
	}
}
