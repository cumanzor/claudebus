//go:build darwin || linux

package client

import (
	"errors"
	"fmt"
	"os"
)

// NativeConnectIdentity prefers the nearest witnessed harness. An explicit Codex
// store plus exact thread UUID retains detached attachment when none is visible;
// an inherited session ID alone never selects an adapter. Tokens stay separate.
func NativeConnectIdentity(opts CodexConnectOptions) (ConnectRequest, string, error) {
	return nativeConnectIdentityForHarness(HarnessName(), opts, CodexConnectIdentityWithOptions, ClaudeConnectIdentity)
}

func nativeConnectIdentityForHarness(harness string, opts CodexConnectOptions, codex func(CodexConnectOptions) (CodexQueueConfig, string, error), claude func() (ClaudeConnectBinding, error)) (ConnectRequest, string, error) {
	if harness == "" && opts.SQLiteHome != "" && uuidLike(os.Getenv("CODEX_THREAD_ID")) {
		harness = "codex"
	}
	req := ConnectRequest{Harness: harness, Protocol: DaemonProtocolVersion}
	host, err := HostLabel()
	if err != nil {
		return req, "", err
	}
	req.Host = host
	switch harness {
	case "codex":
		cfg, sid, err := codex(opts)
		req.Config, req.ThreadID = cfg, sid
		return req, "", err
	case "claude":
		if opts.SQLiteHome != "" {
			return req, "", errors.New("--codex-sqlite-home applies only to Codex CLI sessions")
		}
		binding, err := claude()
		if err != nil {
			return req, "", err
		}
		token := os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN")
		if !validClaudeCredentialToken(token) {
			return req, "", errors.New("Claude native messaging token is unavailable or invalid in this session")
		}
		req.Claude, req.ThreadID = &binding, binding.SessionID
		return req, token, nil
	case "":
		return req, "", errors.New("cbus connect requires a verified Claude Code or Codex CLI ancestor; run it from the current session")
	default:
		return req, "", fmt.Errorf("cbus connect has no native adapter for the nearest harness %q", harness)
	}
}
