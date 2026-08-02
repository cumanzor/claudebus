//go:build darwin || linux

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The launcher shim is POSIX shell that the OSA forker feeds to iTerm2 or tmux, and
// that whole path refuses on windows in phase 1 (cbus-que.3), so this case is tagged
// rather than given a windows stand-in: there is no windows launcher for it to prove.
// TestLauncherScriptExecutes runs the generated script via `/bin/bash <tmpfile>` (the
// exact invocation iTerm2 makes) and proves the mechanism end-to-end WITHOUT iTerm2:
// PATH + CLAUDE_CONFIG_DIR + cwd are replicated, the exec runs, and the script deletes
// itself. (The iTerm2 tokenizer leg is the reviewer's live probe harness.)
func TestLauncherScriptExecutes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "probe.out")
	spec := ForkSpec{
		Target: "window",
		Argv:   []string{"/bin/sh", "-c", `printf 'cwd=%s\npath=%s\ncfg=%s\n' "$PWD" "$PATH" "$CLAUDE_CONFIG_DIR" > ` + out},
		// PATH keeps the real /bin:/usr/bin (the launcher's own `rm` resolves through
		// it, just like cc-branch.sh) plus a probe marker to prove replication.
		Env: map[string]string{"PATH": "/probe/bin:/bin:/usr/bin", "CLAUDE_CONFIG_DIR": "/probe/cfg"},
		Dir: dir,
	}
	script := filepath.Join(dir, "launch.sh")
	if err := os.WriteFile(script, []byte(launcherScript(spec, script)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/bin/bash", script).Run(); err != nil {
		t.Fatalf("launcher run: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cwd=" + dir, "path=/probe/bin:/bin:/usr/bin", "cfg=/probe/cfg"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("launcher did not replicate %q; probe output: %q", want, got)
		}
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("launcher must self-delete before exec; stat err = %v", err)
	}
}
