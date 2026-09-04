package client

import "fmt"

// defaultTmuxRun is the windows half of the tmuxRun seam. Layout is unix-only in phase 1
// (tmux has no windows backend), so if RunLayoutOps is ever reached here it must fail
// LOUDLY rather than silently no-op. The CLI refuses arrange/scatter/focus before any of
// this, so in normal operation it is unreachable — this exists only to keep the common
// tmuxRun var resolvable and to make a regression that routes past the refusal a hard
// error instead of a quiet success.
func defaultTmuxRun([]string) ([]byte, error) {
	return nil, fmt.Errorf("layout is not available on windows in phase 1: tmux is unix-only")
}
