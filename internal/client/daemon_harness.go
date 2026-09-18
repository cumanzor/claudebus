package client

import "fmt"

const daemonHarnessCodex = "codex"

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
	if daemonHarness(harness) != daemonHarnessCodex {
		return fmt.Errorf("unsupported daemon harness %q; only codex is available", harness)
	}
	return nil
}

// Guard the current adapter entry points independently of request admission:
// a later adapter must supply its own behavior, not borrow Codex assumptions.
func requireCodexConnection(c *ConnectionState) error {
	if daemonHarness(c.Harness) != daemonHarnessCodex {
		return fmt.Errorf("Codex adapter does not support daemon harness %q", c.Harness)
	}
	return nil
}
