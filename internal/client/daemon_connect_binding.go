package client

import (
	"errors"
	"os"
	"path/filepath"
)

func prepareConnectBinding(req ConnectRequest, token string) (*ClaudeConnectionConfig, error) {
	if daemonHarness(req.Harness) == daemonHarnessCodex {
		return nil, validateCodexQueueBinding(req.Config)
	}
	return prepareClaudeAdmission(req, token)
}

func sameConnectHome(c *ConnectionState, req ConnectRequest) bool {
	if daemonHarness(c.Harness) == daemonHarnessClaude {
		return c.Claude != nil && req.Claude != nil && c.Claude.Binding.ConfigHome == req.Claude.ConfigHome
	}
	return sameCodexConnectionHome(c.Config, req.Config.Home)
}

func connectionCwd(c *ConnectionState) string {
	if c.Claude != nil && daemonHarness(c.Harness) == daemonHarnessClaude {
		return c.Claude.Binding.Cwd
	}
	return c.Config.Cwd
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
