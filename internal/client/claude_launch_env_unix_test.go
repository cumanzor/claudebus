//go:build !windows

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeLaunchCommandsPreserveStoreAndClearTerminalIdentity(t *testing.T) {
	identityKeys := []string{"CBUS_SESSION_ID", "CBUS_ALIAS", "CBUS_CHANNEL", "CBUS_HARNESS",
		"CLAUDE_CODE_SESSION_ID", "CLAUDE_PID", "CLAUDECODE", "CLAUDE_ENV_FILE",
		"CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CODEX_THREAD_ID", "GROK_SESSION_ID"}
	for _, launch := range []string{"fresh", "fork"} {
		t.Run(launch, func(t *testing.T) {
			root := t.TempDir()
			home, bus := filepath.Join(root, "chosen home"), filepath.Join(root, "chosen bus")
			config := filepath.Join(home, ".ccs", "instances", "alpha")
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// The only launched child is a shell fixture. Its argv still carries
			// the real fresh/fork prompt, but no harness, GUI or model is invoked.
			if err := os.WriteFile(filepath.Join(bin, "ccs"), []byte("#!/bin/sh\n/usr/bin/env\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			t.Setenv("HOME", home)
			t.Setenv("CBUS_DIR", bus)
			t.Setenv("CLAUDE_CONFIG_DIR", config)
			t.Setenv("CBUS_SESSION_ID", "")
			t.Setenv("CLAUDE_CODE_SESSION_ID", "parent-session")
			f := &fakeForker{}
			var err error
			if launch == "fresh" {
				_, _, err = Spawn("tab", "launch-test", "", "child", "", f)
			} else {
				_, _, _, err = Branch("tab", "launch-test", "", "child", f)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := ReadPeerMeta(filepath.Join(bus, "launch-test", "child", "meta.json")); !ok {
				t.Fatal("launcher did not reserve the chosen store")
			}
			for _, backend := range []string{"tmux", "iterm"} {
				t.Run(backend, func(t *testing.T) {
					var cmd *exec.Cmd
					if backend == "tmux" {
						cmd = exec.Command("/bin/sh", "-c", terminalCommand(f.spec))
					} else {
						path := filepath.Join(t.TempDir(), "launch.sh")
						if err := os.WriteFile(path, []byte(launcherScript(f.spec, path)), 0700); err != nil {
							t.Fatal(err)
						}
						cmd = exec.Command("/bin/bash", path)
					}
					cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/wrong-home", "CBUS_DIR=/wrong-bus",
						"CLAUDE_CONFIG_DIR=/wrong-config", "ANTHROPIC_BASE_URL=http://fixture-provider", "KEEP_CONFIG=chosen"}
					for _, key := range identityKeys {
						cmd.Env = append(cmd.Env, key+"=stale-terminal-identity")
					}
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("generated %s command failed: %v", backend, err)
					}
					got := map[string]string{}
					for _, line := range strings.Split(string(out), "\n") {
						key, value, _ := strings.Cut(line, "=")
						got[key] = value
					}
					for key, want := range map[string]string{"HOME": home, "CBUS_DIR": bus, "CLAUDE_CONFIG_DIR": config,
						"PATH": os.Getenv("PATH"), "ANTHROPIC_BASE_URL": "http://fixture-provider", "KEEP_CONFIG": "chosen"} {
						if got[key] != want {
							t.Errorf("%s=%q, want %q", key, got[key], want)
						}
					}
					for _, key := range identityKeys {
						if _, exists := got[key]; exists {
							t.Errorf("inherited runtime identity %s survived generated launch", key)
						}
					}
				})
			}
		})
	}
}

func TestClaudeLaunchPinsRelativeBusBeforeChangingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CBUS_DIR", "relative-bus")
	env, err := peerEnv("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd(), "relative-bus")
	if env["CBUS_DIR"] != want || !filepath.IsAbs(env["CBUS_DIR"]) {
		t.Fatalf("relative bus would follow restored peer cwd: %q", env["CBUS_DIR"])
	}
}
