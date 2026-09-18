package main

import "strings"

// A detached daemon serves many sessions. It must not retain the launching
// session's messaging capability or borrow its identity for child operations.
func daemonEnvironment(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CLAUDE_CODE_MESSAGING_") {
			continue
		}
		switch key {
		case "CBUS_SESSION_ID", "CBUS_CHANNEL", "CBUS_ALIAS", "CBUS_HARNESS",
			"CLAUDE_CODE_SESSION_ID", "CLAUDE_PID", "CLAUDE_ENV_FILE",
			"CODEX_THREAD_ID", "CODEX_SESSION_ID", "GROK_SESSION_ID":
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}
