package client

import (
	"errors"
	"os"
	"path/filepath"
)

// connectBindings carries the one adapter-specific binding a request admits.
type connectBindings struct {
	claude   *ClaudeConnectionConfig
	opencode *OpencodeConnectionConfig
}

func prepareConnectBinding(req ConnectRequest, token string) (connectBindings, error) {
	switch daemonHarness(req.Harness) {
	case daemonHarnessCodex:
		return connectBindings{}, validateCodexQueueBinding(req.Config)
	case daemonHarnessClaude:
		cfg, err := prepareClaudeAdmission(req, token)
		return connectBindings{claude: cfg}, err
	case daemonHarnessOpencode:
		cfg, err := prepareOpencodeAdmission(req)
		return connectBindings{opencode: cfg}, err
	default:
		return connectBindings{}, errUnsupportedHarness(req.Harness)
	}
}

func sameConnectHome(c *ConnectionState, req ConnectRequest) bool {
	switch daemonHarness(c.Harness) {
	case daemonHarnessClaude:
		return c.Claude != nil && req.Claude != nil && c.Claude.Binding.ConfigHome == req.Claude.ConfigHome
	case daemonHarnessOpencode:
		return c.Opencode != nil && req.Opencode != nil &&
			filepath.Dir(c.Opencode.Binding.Endpoint.Socket) == filepath.Dir(req.Opencode.Endpoint.Socket)
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
	case daemonHarnessOpencode:
		if c.Opencode != nil {
			return c.Opencode.Binding.Cwd
		}
		return ""
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
