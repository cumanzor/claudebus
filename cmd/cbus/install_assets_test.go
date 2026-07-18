package main

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus"
)

// TestInstallAssetsShaGuardTable is S7: the sha-guard table driven through the REAL
// CLI verb, with the per-file skip reason printed at the terminal. Uses the roles
// embed against a temp --path so nothing on the machine is touched.
func TestInstallCommandsShaGuard(t *testing.T) {
	dst := t.TempDir()

	// fresh install: every file installed.
	out := captureStdout(t, func() {
		if rc := runInstallCommands([]string{"--path", dst}); rc != 0 {
			t.Fatalf("fresh install rc=%d", rc)
		}
	})
	if !strings.Contains(out, "bus-join.md") || !strings.Contains(out, "installed") {
		t.Errorf("fresh install output:\n%s", out)
	}
	// every embedded command now exists in dst.
	names, _ := embeddedNames(claudebus.Commands, "commands")
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dst, n)); err != nil {
			t.Errorf("%s not installed: %v", n, err)
		}
	}

	// re-run: all up-to-date, rc 0.
	out = captureStdout(t, func() {
		if rc := runInstallCommands([]string{"--path", dst}); rc != 0 {
			t.Fatalf("re-run rc=%d", rc)
		}
	})
	if !strings.Contains(out, "up-to-date") || strings.Contains(out, "installed") {
		t.Errorf("re-run should be all up-to-date:\n%s", out)
	}

	// locally edit one file: without --force it is SKIPPED with a per-file reason,
	// rc non-zero, and the OTHER files stay up-to-date (the batch is not aborted).
	edited := filepath.Join(dst, "bus-join.md")
	if err := os.WriteFile(edited, []byte("locally hacked"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if rc := runInstallCommands([]string{"--path", dst}); rc == 0 {
			t.Error("an edited file without --force must yield a non-zero exit")
		}
	})
	if !strings.Contains(out, "bus-join.md") || !strings.Contains(out, "SKIPPED") || !strings.Contains(out, "--force") {
		t.Errorf("edited file must be skipped with a per-file reason:\n%s", out)
	}
	if b, _ := os.ReadFile(edited); string(b) != "locally hacked" {
		t.Error("a skipped file must not be overwritten")
	}

	// --force overwrites the edited file back to shipped, rc 0.
	out = captureStdout(t, func() {
		if rc := runInstallCommands([]string{"--path", dst, "--force"}); rc != 0 {
			t.Fatalf("--force rc=%d", rc)
		}
	})
	if !strings.Contains(out, "installed") {
		t.Errorf("--force should reinstall the edited file:\n%s", out)
	}
	emb, _ := claudebus.Commands.ReadFile("commands/bus-join.md")
	if b, _ := os.ReadFile(edited); string(b) != string(emb) {
		t.Error("--force did not restore the shipped content")
	}
}

// TestInstallRolesDefaultAndContent: roles install to a temp path and match the embed.
func TestInstallRoles(t *testing.T) {
	dst := t.TempDir()
	if rc := runInstallRoles([]string{"--path", dst}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for _, n := range []string{"coder.md", "documenter.md", "orchestrator.md", "reviewer.md"} {
		got, err := os.ReadFile(filepath.Join(dst, n))
		if err != nil {
			t.Errorf("%s not installed: %v", n, err)
			continue
		}
		emb, _ := claudebus.Roles.ReadFile("roles/" + n)
		if string(got) != string(emb) {
			t.Errorf("%s content differs from embed", n)
		}
	}
}

func TestInstallDefaultRolesDir(t *testing.T) {
	t.Setenv("CBUS_DIR", "/tmp/x-cbus")
	if got := defaultRolesDir(); got != "/tmp/x-cbus/roles" {
		t.Errorf("defaultRolesDir = %q", got)
	}
}

func embeddedNames(fsys embed.FS, subdir string) ([]string, error) {
	entries, err := fsys.ReadDir(subdir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
