package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"claudebus/internal/core"
)

// FormationCodexBackend records storage identity, not a runnable launch profile.
// It deliberately omits credentials and transient process/presence identifiers.
// A later launcher must also establish the owning CLI's launch configuration.
type FormationCodexBackend struct {
	Binary        string `json:"binary"`
	Home          string `json:"home"`
	SQLiteHome    string `json:"sqliteHome"`
	UserHome      string `json:"userHome"`
	BindingSource string `json:"bindingSource,omitempty"`
}

func formationHarness(p *FormationPeer) string {
	if p.Harness != "" {
		return strings.ToLower(p.Harness)
	}
	if p.CodexBackend != nil {
		return "codex"
	}
	// Existing v1 templates predate harness identity and use the Claude launcher.
	return "claude"
}

func formationHarnessRefusal(p *FormationPeer) error {
	harness := formationHarness(p)
	if harness == "claude" && p.CodexBackend == nil {
		return nil
	}
	if harness == "codex" || p.CodexBackend != nil {
		return fmt.Errorf("peer %q records harness=codex; automated Formation launch/bootstrap for Codex is not supported yet: open or resume the exact Codex CLI thread with its recorded backend, then run cbus connect from that session; no Claude or fresh-session fallback was attempted", p.Alias)
	}
	return fmt.Errorf("peer %q records harness=%s; automated Formation launch/bootstrap for this harness is not supported; start that harness explicitly and reconnect it", p.Alias, harness)
}

type formationManagedSnapshot struct {
	Harness, Cwd, Profile string
	CodexBackend          *FormationCodexBackend
	ProfileKnown          bool
	Online, LiveSession   bool
}

// Captured identity comes only from this registration's journal. Claude runtime
// observations perform no message submission and never read its credential.
func formationManagedBackend(ch, alias string, m PeerMeta) (*formationManagedSnapshot, error) {
	if m.ConnectionID == "" {
		return nil, nil
	}
	if !core.ValidStoreName(m.ConnectionID) {
		return nil, fmt.Errorf("invalid managed connection id for %s/%s", ch, alias)
	}
	b, err := os.ReadFile(filepath.Join(DaemonDir(), "connections", m.ConnectionID+".json"))
	if err != nil {
		return nil, fmt.Errorf("read managed backend for %s/%s: %w", ch, alias, err)
	}
	var c ConnectionState
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("read managed backend for %s/%s: %w", ch, alias, err)
	}
	harness := daemonHarness(c.Harness)
	if c.ID != m.ConnectionID || c.Channel != ch || c.Alias != alias || c.ThreadID != m.SessionID || m.Alias != alias ||
		c.Relay != nil || (m.Harness != "" && m.Harness != harness) {
		return nil, fmt.Errorf("managed backend identity mismatch for %s/%s", ch, alias)
	}
	if err := validateDaemonHarness(harness); err != nil {
		return nil, err
	}
	s := &formationManagedSnapshot{Harness: harness, Cwd: m.Cwd, Profile: m.Profile}
	if harness == daemonHarnessClaude {
		if err := validateConnectionBinding(&c); err != nil {
			return nil, fmt.Errorf("invalid managed Claude binding for %s/%s: %w", ch, alias, err)
		}
		// A saver may use a different CCS profile. Only the captured runtime's
		// config directory can identify the peer's launch profile.
		s.Cwd = c.Claude.Binding.Cwd
		if cfg := c.Claude.Binding.ConfigHome; isCCSInstanceDir(cfg) {
			s.Profile, s.ProfileKnown = filepath.Base(cfg), true
		} else if cfg == filepath.Join(c.Claude.Binding.UserHome, ".claude") {
			s.Profile, s.ProfileKnown = "", true
		} else {
			return nil, fmt.Errorf("managed Claude peer %s/%s uses a custom config root that formations cannot represent; open or resume that exact configured CLI and reconnect manually", ch, alias)
		}
		p, err := observeClaudeConsumer(&c)
		s.Online = err == nil && p.State == "online" && c.State != "disconnected" && c.State != "detached" && c.State != "binding-required"
		// Unknown is not proof that a transcript is free to resume. A positive
		// process exit or loaded-session change is required to release it.
		s.LiveSession = err != nil || p.State != "exited"
		return s, nil
	}
	s.CodexBackend = &FormationCodexBackend{Binary: c.Config.Binary, Home: c.Config.Home, SQLiteHome: c.Config.SQLiteHome, UserHome: c.Config.UserHome, BindingSource: c.Config.BindingSource}
	s.Online = c.State != "disconnected" && c.State != "detached" && c.Consumer != nil && c.Consumer.State == "online"
	s.LiveSession = c.Consumer == nil || c.Consumer.State != "exited"
	return s, nil
}
