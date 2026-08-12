package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modeSid is the documenter's recorded session. Shaped like a real one because
// TranscriptPath screens the id before it ever globs for the file.
const modeSid = "beefbeef-0000-0000-0000-000000000000"

// fixtureModes is a formation whose launchable peer is recorded mode=template with a
// resumable session. That is the discriminating shape for --mode: the file alone
// produces a fresh spawn, so a launch carrying --resume can only have come from the
// flag reaching the planner through the real CLI.
func fixtureModes() string {
	return `{
  "schema": "cbus-formation/v1",
  "name": "modes",
  "channel": "modes",
  "host": null,
  "anchorAlias": "orchestrator",
  "savedAt": "2026-08-08T00:00:00Z",
  "savedBy": "modes/orchestrator",
  "peers": [
    { "alias": "orchestrator", "origin": "joined", "mode": "template",
      "target": "tab", "machine": "` + thisMachine() + `" },
    { "alias": "documenter", "origin": "fresh", "mode": "template",
      "sessionId": "` + modeSid + `", "onStale": "template",
      "target": "tab", "machine": "` + thisMachine() + `" }
  ]
}`
}

// plantTranscript puts a transcript where TranscriptPath looks for one, so a CLI-door
// resume is decided by the same predicate production uses instead of an injected stub.
func plantTranscript(t *testing.T, cfg, sid string) {
	t.Helper()
	dir := filepath.Join(cfg, "projects", "-Users-dev-repos-AI-claudebus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFormationApplyModeThroughCLI is the user's-door requirement for cbus-osr: the
// override has to reach the planner through runFormationApply itself, not only
// through a client-level shim — the --brief lesson (D17), where a parameter was dead
// at the CLI while every in-process test passed.
func TestFormationApplyModeThroughCLI(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantMeta(t, dir, "modes", "orchestrator", "sid-orch") // the applier, present
	saveFixture(t, dir, "modes", fixtureModes())
	plantTranscript(t, cfg, modeSid)

	rec := &cmdRecForker{}
	prev := applyForker
	applyForker = rec
	defer func() { applyForker = prev }()

	out := captureStdout(t, func() {
		if rc := runFormation([]string{"apply", "modes", "--mode", "resume", "--wait", "0"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if len(rec.specs) == 0 {
		t.Fatalf("apply launched nothing through the CLI:\n%s", out)
	}
	joined := strings.Join(rec.specs[0].Argv, " ")
	if !strings.Contains(joined, "--resume "+modeSid) {
		t.Errorf("--mode resume did not reach the launch through the CLI: %v", rec.specs[0].Argv)
	}
	if strings.Contains(joined, "--fork-session") {
		t.Errorf("resume must not fork: %v", rec.specs[0].Argv)
	}

	// the this-run-only line lands BEFORE the plan renders, the way --channel's does:
	// the operator reads what is being overridden while reading what it did.
	note := strings.Index(out, "mode: every peer apply would launch -> resume")
	plan := strings.Index(out, "apply: formation")
	if note < 0 {
		t.Fatalf("no override note on the terminal:\n%s", out)
	}
	if !strings.Contains(out, "this run only; the modes file is untouched") {
		t.Errorf("the note must say the envelope is untouched:\n%s", out)
	}
	if plan < 0 || note > plan {
		t.Errorf("the override note must print before the plan (note=%d plan=%d):\n%s", note, plan, out)
	}

	// and the envelope really is untouched: the override lasted one run, on disk too
	saved, err := os.ReadFile(filepath.Join(dir, ".formations", "modes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), `"mode": "resume"`) || strings.Contains(string(saved), `"mode":"resume"`) {
		t.Errorf("--mode rewrote the saved formation; it is a per-run override:\n%s", saved)
	}
}

// TestFormationApplyModeVerbErrors: the malformed doors. A flag whose value is
// missing or unknown has to fail loudly — silently applying an unrecognized mode
// would plan every peer as template and read like it worked.
func TestFormationApplyModeVerbErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantMeta(t, dir, "modes", "orchestrator", "sid-orch")
	saveFixture(t, dir, "modes", fixtureModes())

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"missing value", []string{"apply", "modes", "--mode"}, []string{"missing value for --mode"}},
		{"unknown mode", []string{"apply", "modes", "--mode", "restore", "--dry-run"},
			[]string{"resume", "fork", "template", "restore"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rc int
			errs := captureStderr(t, func() {
				_ = captureStdout(t, func() { rc = runFormation(tc.args) })
			})
			if rc == 0 {
				t.Fatalf("runFormation(%v): want rc!=0", tc.args)
			}
			for _, want := range tc.want {
				if !strings.Contains(errs, want) {
					t.Errorf("the error must name %q, got: %s", want, errs)
				}
			}
		})
	}
}
