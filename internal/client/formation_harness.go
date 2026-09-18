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

// A managed backend comes only from the journal named by this exact registration
// epoch. Refuse missing/mismatched state instead of saving an older peer's store.
func formationManagedBackend(ch, alias string, m PeerMeta) (*FormationCodexBackend, bool, error) {
	if m.ConnectionID == "" {
		return nil, false, nil
	}
	if !core.ValidStoreName(m.ConnectionID) {
		return nil, false, fmt.Errorf("invalid managed connection id for %s/%s", ch, alias)
	}
	b, err := os.ReadFile(filepath.Join(DaemonDir(), "connections", m.ConnectionID+".json"))
	if err != nil {
		return nil, false, fmt.Errorf("read managed backend for %s/%s: %w", ch, alias, err)
	}
	var c ConnectionState
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, false, fmt.Errorf("read managed backend for %s/%s: %w", ch, alias, err)
	}
	if c.ID != m.ConnectionID || c.Channel != ch || c.Alias != alias || c.ThreadID != m.SessionID {
		return nil, false, fmt.Errorf("managed backend identity mismatch for %s/%s", ch, alias)
	}
	backend := &FormationCodexBackend{Binary: c.Config.Binary, Home: c.Config.Home, SQLiteHome: c.Config.SQLiteHome, UserHome: c.Config.UserHome, BindingSource: c.Config.BindingSource}
	online := c.State != "disconnected" && c.State != "detached" && c.Consumer != nil && c.Consumer.State == "online"
	return backend, online, nil
}
