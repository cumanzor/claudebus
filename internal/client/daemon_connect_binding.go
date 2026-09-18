package client

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
