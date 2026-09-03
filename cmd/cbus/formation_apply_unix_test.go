//go:build darwin || linux

// formation apply is a phase-1 windows-excluded verb (unsupported_windows.go), and its
// --dry-run is refused with it: runFormationApply refuses ahead of the planner, the
// unjoined guard, the brief renderer and the per-flag arg validation, so every apply path
// only exercises its own logic on unix. Windows coverage is the "formation apply" refusal
// row in unsupported_windows_test.go. formation save/list/show/rm/bootstrap are live on
// windows and stay in the untagged formation_test.go.

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
)

// TestFormationVerbErrorsApply is the apply half of TestFormationVerbErrors, split out
// because formation apply is windows-refused: these arg-error rows never reach the
// applier's own validation there. The bootstrap/save/show/rm/list rows stay cross-platform.
func TestFormationVerbErrorsApply(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"apply with no name", []string{"apply"}},
		{"apply trailing junk", []string{"apply", "a", "b"}},
		{"apply unknown flag", []string{"apply", "x", "--bogus"}},
		{"apply bad wait", []string{"apply", "x", "--wait", "soon"}},
		{"apply negative wait", []string{"apply", "x", "--wait", "-5s"}},
		{"apply empty only", []string{"apply", "x", "--only", ","}},
		{"apply missing formation", []string{"apply", "ghost"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rc := runFormation(tc.args); rc == 0 {
				t.Errorf("runFormation(%v): want rc!=0", tc.args)
			}
		})
	}
}

// TestFormationApplyVerbRefusesUnjoined: apply briefs peers to answer THIS session,
// so it must be a peer first. The error has to name the join, not just complain.
func TestFormationApplyVerbRefusesUnjoined(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-outsider")
	saveFixture(t, dir, "roles", fixtureRoles())
	if rc := runFormation([]string{"apply", "roles"}); rc == 0 {
		t.Error("apply from a session that is not on the channel must fail")
	}
}

// TestFormationApplyDryRunVerb: the read-only path through the real CLI. It must
// launch nothing, so it is safe to run anywhere — including here.
func TestFormationApplyDryRunVerb(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantMeta(t, dir, "roles", "orchestrator", "sid-orch")
	saveFixture(t, dir, "roles", fixtureRoles())

	out := captureStdout(t, func() {
		if rc := runFormation([]string{"apply", "roles", "--dry-run", "--brief", "ship it"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	for _, want := range []string{"nothing was launched", "orchestrator", "present", "coder",
		"re-run without --dry-run"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	// the applier is a peer of this formation and is running apply: never launched
	if !strings.Contains(out, "running apply") {
		t.Errorf("the applier should be reported as present because it IS apply:\n%s", out)
	}
}

// cmdRecForker records launches so an apply driven through the CLI can be inspected
// without opening a terminal.
type cmdRecForker struct{ specs []client.ForkSpec }

func (f *cmdRecForker) Fork(s client.ForkSpec) (string, error) {
	f.specs = append(f.specs, s)
	return "", nil
}

// TestFormationApplyBriefThroughCLI is the reviewer's user's-door requirement for
// D17: the brief must reach a rendered kickoff through runFormationApply itself, not
// only through a client-level shim. It drives the real CLI verb with --brief and a
// recording forker, then reads the delivered kickoff.
func TestFormationApplyBriefThroughCLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantMeta(t, dir, "roles", "orchestrator", "sid-orch") // the applier, present
	saveFixture(t, dir, "roles", fixtureRoles())

	rec := &cmdRecForker{}
	prev := applyForker
	applyForker = rec
	defer func() { applyForker = prev }()

	// --wait 0 so the CLI returns without polling for an answer the recorder can't give
	out := captureStdout(t, func() {
		if rc := runFormation([]string{"apply", "roles", "--brief", "SHIP FORMATIONS V1", "--wait", "0"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if len(rec.specs) == 0 {
		t.Fatalf("apply launched nothing through the CLI:\n%s", out)
	}
	found := false
	for _, s := range rec.specs {
		prompt := s.Argv[len(s.Argv)-1]
		if strings.Contains(prompt, "--- the effort ---") && strings.Contains(prompt, "SHIP FORMATIONS V1") {
			found = true
		}
	}
	if !found {
		t.Errorf("the --brief text did not reach any rendered kickoff through the CLI path")
	}
}
