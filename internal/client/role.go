package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"claudebus/internal/core"
)

// LoadRole resolves a committed role prompt (roles/*.md): the body ships
// verbatim as the tail of the child's first turn, and the file's MODEL: line
// is the launch default when --model is absent. Resolution order: the spawn
// cwd's git-toplevel roles/<role>.md (role files ship with the repo they
// serve), then $CBUS_DIR/roles/<role>.md as the machine-global fallback.
// Read before any alias is reserved, so a missing role fails clean.
func LoadRole(role string) (body, model string, err error) {
	if !core.ValidName(role) || strings.HasPrefix(role, "-") {
		return "", "", fmt.Errorf("bad role %q", role)
	}
	var dirs []string
	if out, gerr := exec.Command("git", "rev-parse", "--show-toplevel").Output(); gerr == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			dirs = append(dirs, filepath.Join(top, "roles"))
		}
	}
	dirs = append(dirs, filepath.Join(CBUSDir(), "roles"))
	var tried []string
	for _, d := range dirs {
		p := filepath.Join(d, role+".md")
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			tried = append(tried, p)
			continue
		}
		return string(b), roleModel(string(b)), nil
	}
	return "", "", fmt.Errorf("role %q not found (tried %s)", role, strings.Join(tried, ", "))
}

// roleModel pulls the first "MODEL: <short>" line; invalid or flag-shaped
// tokens read as absent (the same pre-screen Spawn applies to --model).
func roleModel(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "MODEL:") {
			continue
		}
		m := strings.TrimSpace(strings.TrimPrefix(line, "MODEL:"))
		if core.ValidName(m) && !strings.HasPrefix(m, "-") {
			return m
		}
		return ""
	}
	return ""
}
