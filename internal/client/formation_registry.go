package client

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// registrySids maps session ids to the pid Claude Code's own registry
// (<config>/sessions/<pid>.json) says is running them. It sees a session the bus
// and argv cannot: one started fresh that left the channel or lost its listener.
// A recycled pid behind a stale file reads as held, which fails closed.
var registrySids = func() map[string]int {
	return readRegistrySids(claudeConfigHomes(), pidAlive)
}

// claudeConfigHomes are the config dirs a local Claude session may register under.
func claudeConfigHomes() []string {
	var homes []string
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		homes = append(homes, cfg)
	}
	if home, err := os.UserHomeDir(); err == nil {
		homes = append(homes, filepath.Join(home, ".claude"))
		if dirs, err := os.ReadDir(filepath.Join(home, ".ccs", "instances")); err == nil {
			for _, d := range dirs {
				if d.IsDir() {
					homes = append(homes, filepath.Join(home, ".ccs", "instances", d.Name()))
				}
			}
		}
	}
	return dedupeStrings(homes)
}

func readRegistrySids(homes []string, alive func(int) bool) map[string]int {
	held := map[string]int{}
	for _, home := range homes {
		dir := filepath.Join(home, "sessions")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
			if err != nil || pid <= 1 || !alive(pid) {
				continue
			}
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(f, 64<<10))
			f.Close()
			var rec struct {
				PID       int    `json:"pid"`
				SessionID string `json:"sessionId"`
			}
			if err != nil || json.Unmarshal(b, &rec) != nil || rec.PID != pid || !uuidLike(rec.SessionID) {
				continue
			}
			if prev, ok := held[rec.SessionID]; !ok || pid < prev {
				held[rec.SessionID] = pid
			}
		}
	}
	return held
}

// addRegistrySids folds registry-held sessions into a liveSids map; a bus holder wins.
func addRegistrySids(live map[string]string, held map[string]int) {
	for sid, pid := range held {
		if _, ok := live[sid]; !ok {
			live[sid] = fmt.Sprintf("pid %d (Claude Code's session registry lists it running)", pid)
		}
	}
}
