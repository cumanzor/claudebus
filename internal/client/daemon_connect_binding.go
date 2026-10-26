package client

import (
	"errors"
	"os"
	"path/filepath"
)

func prepareConnectBinding(req ConnectRequest, token string) (*ClaudeConnectionConfig, error) {
	switch daemonHarness(req.Harness) {
	case daemonHarnessCodex:
		return nil, validateCodexQueueBinding(req.Config)
	case daemonHarnessClaude:
		return prepareClaudeAdmission(req, token)
	default:
		return nil, errUnsupportedHarness(req.Harness)
	}
}

func sameConnectHome(c *ConnectionState, req ConnectRequest) bool {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		return c.Claude != nil && req.Claude != nil && c.Claude.Binding.ConfigHome == req.Claude.ConfigHome
	case daemonHarnessCodex:
		return sameCodexConnectionHome(c.Config, req.Config.Home)
	default:
		return false
	}
}

func connectionCwd(c *ConnectionState) string {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		if c.Claude != nil {
			return c.Claude.Binding.Cwd
		}
		return c.Config.Cwd
	case daemonHarnessCodex:
		return c.Config.Cwd
	default:
		return ""
	}
}

// Called under the alias lock before any journal write. Never clean a reserved
// or published registration, additional files, or anything but our empty inode.
func cleanupClaudeUnpublishedClaim(dir string, dev, ino uint64) error {
	refuse := errors.New("failed Claude admission left a partial alias; inspect it before explicitly unregistering or retry with a fresh alias")
	if _, err := os.Lstat(filepath.Join(dir, "meta.json")); !os.IsNotExist(err) {
		return refuse
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "inbox.jsonl" {
		return refuse
	}
	path := filepath.Join(dir, "inbox.jsonl")
	currentDev, currentIno, size, ok := fileIdentity(path)
	if !ok || currentDev != dev || currentIno != ino || size != 0 {
		return refuse
	}
	if err := os.Remove(path); err != nil {
		return refuse
	}
	if err := os.Remove(dir); err != nil {
		return refuse
	}
	return nil
}
