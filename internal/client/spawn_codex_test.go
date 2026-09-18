//go:build darwin || linux

package client

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func prepareCodexSpawn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	program := filepath.Join(dir, "codex")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	return program
}

func TestCodexSpawnIsIndependentOfTerminalAndParentHarness(t *testing.T) {
	program := prepareCodexSpawn(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "/parent/.ccs/instances/work")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent-claude")
	t.Setenv("CODEX_THREAD_ID", "parent-codex")
	for _, target := range []string{"window", "tab", "tmux", "pane"} {
		f := &fakeForker{}
		_, alias, err := SpawnWithOptions(target, "codex-test", "gpt-5", "worker-"+target, "", SpawnOptions{Harness: "codex", Profile: "work"}, f)
		if err != nil {
			t.Fatal(err)
		}
		if !f.called || f.spec.Target != target || f.spec.Dir == "" || f.spec.Title != alias {
			t.Fatalf("lost terminal spec: %+v", f.spec)
		}
		args := f.spec.Argv
		if !slices.Contains(args, program) || slices.Contains(args, "claude") || slices.Contains(args, "--name") || slices.Contains(args, "--remote") {
			t.Fatalf("wrong harness argv: %q", args)
		}
		for _, v := range []string{"--profile", "work", "--model", "gpt-5", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID", "CODEX_EXEC_SERVER_URL"} {
			if !slices.Contains(args, v) {
				t.Fatalf("missing %s in %q", v, args)
			}
		}
		if f.spec.Env["CODEX_HOME"] != os.Getenv("CODEX_HOME") || f.spec.Env["CBUS_DIR"] != CBUSDir() || f.spec.Env["CLAUDE_CONFIG_DIR"] != "" {
			t.Fatalf("wrong caller configuration: %+v", f.spec.Env)
		}
		prompt := args[len(args)-1]
		if !strings.Contains(prompt, " connect 'codex-test' '"+alias+"' --json") || !strings.Contains(prompt, "do not start a Monitor") {
			t.Fatalf("wrong bootstrap: %s", prompt)
		}
		if !strings.Contains(prompt, " list 'codex-test' once") || !strings.Contains(prompt, "role unknown") || !strings.Contains(prompt, "Briefly tell the user") {
			t.Fatalf("spawned peer lacks roster and visible presence guidance: %s", prompt)
		}
	}
}

func TestCodexSpawnFailureReleasesOnlyReservation(t *testing.T) {
	prepareCodexSpawn(t)
	f := failedCodexForker{}
	_, _, err := SpawnWithOptions("pane", "spawn-failed", "", "worker", "", SpawnOptions{Harness: "codex"}, f)
	if err == nil {
		t.Fatal("expected terminal error")
	}
	if _, err := os.Stat(filepath.Join(CBUSDir(), "spawn-failed", "worker")); !os.IsNotExist(err) {
		t.Fatal("failed launch left reservation")
	}
}

func TestCodexRemoteSpawnChoosesChildAliasAndScopesRoster(t *testing.T) {
	for _, alias := range []string{"", "reviewer"} {
		prompt := CodexSpawnPrompt("/installed/cbus", "team@relay", alias)
		wantAlias := `'reviewer'`
		if alias == "" {
			wantAlias = `"codex-$CODEX_THREAD_ID"`
		}
		if !strings.Contains(prompt, `connect 'team@relay' `+wantAlias+` --json`) || !strings.Contains(prompt, `'/installed/cbus' list 'team@relay' once`) {
			t.Fatalf("remote bootstrap lacks explicit child alias or scoped roster: %s", prompt)
		}
	}
}

type failedCodexForker struct{}

func (failedCodexForker) Fork(ForkSpec) (string, error) {
	return "", errors.New("stale terminal anchor")
}

func TestCodexLaunchScrubsInheritedIdentityInBothTerminalCommands(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires shell")
	}
	prepareCodexSpawn(t)
	t.Setenv("CODEX_THREAD_ID", "wrong-parent")
	t.Setenv("CBUS_ALIAS", "wrong-alias")
	t.Setenv("CODEX_EXEC_SERVER_URL", "wrong-executor")
	c, err := resolveCodexSpawnContext()
	if err != nil {
		t.Fatal(err)
	}
	c.binary = "/bin/sh"
	args := c.argv("", "", "")
	args = args[:len(args)-1]
	args = append(args, "-c", `test -z "$CODEX_THREAD_ID$CBUS_ALIAS$CODEX_EXEC_SERVER_URL" && test "$CODEX_HOME" = "$EXPECTED_HOME" && printf OK`)
	c.env["EXPECTED_HOME"] = c.env["CODEX_HOME"]
	spec := ForkSpec{Argv: args, Env: c.env, Dir: t.TempDir()}
	for _, script := range []string{forkShellCommand(spec), launcherScript(spec, filepath.Join(t.TempDir(), "launcher"))} {
		out, err := exec.Command("/bin/bash", "-c", script).CombinedOutput()
		if err != nil || string(out) != "OK" {
			t.Fatalf("terminal launch leaked caller identity: %v %s", err, out)
		}
	}
}
