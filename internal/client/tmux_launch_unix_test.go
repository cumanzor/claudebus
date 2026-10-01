//go:build darwin || linux

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scratchTmux starts a private tmux server on its own socket and points TMUX and
// TMUX_PANE at it, so the forkers' plain `tmux` calls never reach the operator's server.
func scratchTmux(t *testing.T, serverEnv ...string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// a short dir: t.TempDir() can exceed the 104-byte unix socket path limit
	dir, err := os.MkdirTemp("/tmp", "cbtm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	tm := func(args ...string) string {
		out, err := exec.Command("tmux", append([]string{"-S", sock}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	start := exec.Command("tmux", "-S", sock, "new-session", "-d", "-s", "probe", "-x", "200", "-y", "50")
	start.Env = append(os.Environ(), serverEnv...) // tmux children inherit the SERVER's environment
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start scratch tmux: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
	pid, pane := tm("display-message", "-p", "#{pid}"), tm("display-message", "-p", "#{pane_id}")
	t.Setenv("TMUX", sock+","+pid+",0")
	t.Setenv("TMUX_PANE", pane)
}

// awkwardPrompt is n bytes of what a launch prompt can hold and a shell mangles.
func awkwardPrompt(n int) string {
	unit := "quote ' double \" dollar $HOME back `id` bang ! newline\n tab\t é ü 漢字 \\ ; | & > <\n"
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(unit)
	}
	return b.String()[:n]
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			time.Sleep(200 * time.Millisecond) // let the writer finish
			b, _ = os.ReadFile(path)
			return b
		}
	}
	t.Fatalf("the launched child never wrote %s", path)
	return nil
}

// TestTmuxLaunchCarriesALargePromptIntact: a launch prompt far past tmux's command
// size limit reaches the child byte for byte, through both tmux launch paths.
func TestTmuxLaunchCarriesALargePromptIntact(t *testing.T) {
	scratchTmux(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // the launcher lands here, so a leftover is visible
	for _, size := range []int{20 << 10, 64 << 10} {
		for _, target := range []string{"tmux", "pane"} {
			out := filepath.Join(t.TempDir(), "argv.out")
			prompt := awkwardPrompt(size)
			spec := ForkSpec{
				Target: target, Title: "probe", Dir: t.TempDir(),
				Argv: []string{"/bin/sh", "-c", `printf '[%s][%s]' "$2" "$3" > "$1.extra"; printf %s "$0" > "$1"`, prompt, out, "-leading", ""},
				Env:  map[string]string{"PATH": "/usr/bin:/bin"},
			}
			var err error
			if target == "tmux" {
				err = forkTmuxWindow(spec)
			} else {
				_, err = forkTmuxPane(spec)
			}
			if err != nil {
				t.Fatalf("%s, %d-byte prompt: launch failed: %v", target, size, err)
			}
			if got := waitForFile(t, out); string(got) != prompt {
				t.Fatalf("%s, %d-byte prompt: the child received %d bytes that differ from the prompt", target, size, len(got))
			}
			if extra, _ := os.ReadFile(out + ".extra"); string(extra) != "[-leading][]" {
				t.Errorf("%s: a leading-dash arg and an empty arg must arrive intact, got %q", target, extra)
			}
			if left, _ := filepath.Glob(filepath.Join(tmp, "cc-branch.*.sh")); len(left) != 0 {
				t.Errorf("%s: the launcher must delete itself, found %v", target, left)
			}
		}
	}
}

// TestTmuxLaunchArgvStaysShortForAHugePrompt: the tmux argument is the launcher's
// short command whatever the prompt size, far under tmux's ~16KB command limit.
func TestTmuxLaunchArgvStaysShortForAHugePrompt(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	spec := ForkSpec{Target: "tmux", Title: "w", Dir: "/tmp", Argv: []string{"claude", awkwardPrompt(64 << 10)}}
	path, err := writeLauncher(spec)
	if err != nil {
		t.Fatal(err)
	}
	for name, argv := range map[string][]string{
		"new-window":   tmuxNewWindowArgv(spec, launcherCommand(path)),
		"split-window": tmuxSplitArgv("%1", launcherCommand(path), 1, ""),
	} {
		if n := len(strings.Join(argv, " ")); n > 200 {
			t.Errorf("%s argv is %d bytes for a 64KB prompt; it must not carry the prompt", name, n)
		}
	}
}

func TestWriteLauncherIsPrivateAndExact(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	spec := ForkSpec{Target: "tmux", Dir: "/tmp", Argv: []string{"claude", "hi 'there'"}, Env: map[string]string{"PATH": "/a b"}}
	path, err := writeLauncher(spec)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("launcher mode = %v, want 0700", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); string(b) != launcherScript(spec, path) {
		t.Errorf("launcher content differs from launcherScript:\n%s", b)
	}
}

// stubTmux puts a fake tmux on PATH that answers the forkers' queries and refuses
// every launch, the way a real server refuses an oversized command.
func stubTmux(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
display-message) echo '$1' ;;
list-panes) echo '%1' ;;
new-window|split-window) echo "refused" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/nonexistent,1,0")
	t.Setenv("TMUX_PANE", "%1")
}

func TestTmuxDispatchFailureRemovesTheLauncher(t *testing.T) {
	stubTmux(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	spec := ForkSpec{Target: "tmux", Title: "w", Dir: "/tmp", Argv: []string{"true"}}
	if err := forkTmuxWindow(spec); err == nil || !strings.Contains(err.Error(), "tmux new-window") {
		t.Fatalf("precondition: the stub must refuse new-window, got %v", err)
	}
	spec.Target = "pane"
	if _, err := forkTmuxPane(spec); err == nil || !strings.Contains(err.Error(), "tmux split-window") {
		t.Fatalf("precondition: the stub must refuse split-window (and its retry), got %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "cc-branch.*.sh")); len(left) != 0 {
		t.Errorf("a launcher that never ran must be removed, found %v", left)
	}
}

func TestLauncherDeletesItselfWhenCdFails(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path, err := writeLauncher(ForkSpec{Dir: "/nonexistent/dir", Argv: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/bin/bash", path).Run(); err == nil {
		t.Fatal("precondition: the cd must fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the launcher must delete itself before the cd, stat err = %v", err)
	}
}

// TestTmuxLaunchSetsTheChildEnvironment: a tmux child starts from the server's
// environment, not the launcher's caller, so the launcher itself must unset stale
// identity and set the replicated values.
func TestTmuxLaunchSetsTheChildEnvironment(t *testing.T) {
	scratchTmux(t, "CLAUDE_CODE_SESSION_ID=stale-server", "CBUS_SESSION_ID=stale-server", "KEEP_ME=server")
	t.Setenv("TMPDIR", t.TempDir())
	for _, target := range []string{"tmux", "pane"} {
		out := filepath.Join(t.TempDir(), "env.out")
		spec := ForkSpec{
			Target: target, Title: "env", Dir: t.TempDir(),
			Argv:     []string{"/bin/sh", "-c", `env > "$0"`, out},
			Env:      map[string]string{"PATH": "/usr/bin:/bin", "CBUS_DIR": "/chosen/bus"},
			UnsetEnv: []string{"CLAUDE_CODE_SESSION_ID", "CBUS_SESSION_ID"},
		}
		var err error
		if target == "tmux" {
			err = forkTmuxWindow(spec)
		} else {
			_, err = forkTmuxPane(spec)
		}
		if err != nil {
			t.Fatalf("%s: launch failed: %v", target, err)
		}
		env := "\n" + string(waitForFile(t, out))
		for _, gone := range []string{"\nCLAUDE_CODE_SESSION_ID=", "\nCBUS_SESSION_ID="} {
			if strings.Contains(env, gone) {
				t.Errorf("%s: %s survived from the server environment", target, strings.TrimSpace(gone))
			}
		}
		for _, want := range []string{"\nCBUS_DIR=/chosen/bus\n", "\nPATH=/usr/bin:/bin\n", "\nKEEP_ME=server\n"} {
			if !strings.Contains(env, want) {
				t.Errorf("%s: child environment lacks %s", target, strings.TrimSpace(want))
			}
		}
	}
}

// TestLauncherAvoidsATempDirThatNeedsQuoting: the launcher command is handed over
// bare, so a TMPDIR with a space or a quote must not end up in it.
func TestLauncherAvoidsATempDirThatNeedsQuoting(t *testing.T) {
	odd := filepath.Join(t.TempDir(), "a b'c")
	if err := os.MkdirAll(odd, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", odd)
	path, err := writeLauncher(ForkSpec{Dir: "/tmp", Argv: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if strings.ContainsAny(path, " '") || !strings.HasPrefix(path, "/tmp/") {
		t.Fatalf("the launcher must land in a dir that needs no quoting, got %q", path)
	}
	if f := strings.Fields(launcherCommand(path)); len(f) != 2 {
		t.Fatalf("the launcher command must stay two bare tokens, got %q", f)
	}
}
