package client

import (
	"strings"
	"testing"
)

// TestLayoutRunOpsFailsLoudlyOnWindows pins the linkage-trap fallback: the tmuxRun seam
// resolves on windows (defaultTmuxRun exists there), but reaching it must be a hard error,
// never a silent success. The CLI refuses the layout verbs ahead of this; this guards a
// regression that routes past the refusal.
func TestLayoutRunOpsFailsLoudlyOnWindows(t *testing.T) {
	if _, err := defaultTmuxRun([]string{"list-panes"}); err == nil ||
		!strings.Contains(err.Error(), "not available on windows") {
		t.Fatalf("defaultTmuxRun on windows must error naming the exclusion, got %v", err)
	}
	if applied, err := RunLayoutOps([]LayoutOp{{Argv: []string{"list-panes"}}}); err == nil {
		t.Fatalf("RunLayoutOps must fail loudly on windows, got applied=%d err=nil", applied)
	}
}
