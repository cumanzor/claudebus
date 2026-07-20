package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"claudebus/internal/core"
)

// A DERIVED channel is sanitized, not rejected. The user never typed it, so a repo
// living at ~/.dotfiles must keep working — rejecting the derived name would make
// branch and spawn unusable in that repo with no lever but an explicit channel on
// every call.
func TestBranchChannelFromGitYieldsAStoreLegalName(t *testing.T) {
	cases := []struct{ dir, want string }{
		{".dotfiles", "dotfiles"},
		{"-weird", "weird"},
		{"....", "global"}, // strips to empty, so the documented fallback takes over
		{"normal-repo", "normal-repo"},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), c.dir)
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v (%s)", err, out)
			}
			t.Chdir(repo)
			got := branchChannelFromGit()
			if got != c.want {
				t.Errorf("branchChannelFromGit() in %q = %q, want %q", c.dir, got, c.want)
			}
			if !core.ValidStoreName(got) {
				t.Errorf("derived channel %q is not store-legal — branch would hard-fail here", got)
			}
		})
	}
}

// The reservation is the chokepoint every fork verb funnels through, so it must
// refuse on its own even when a caller's pre-validator is bypassed.
func TestReserveAliasRefusesStoreIllegalNames(t *testing.T) {
	for _, bad := range []string{".hidden", "-flag", "--force"} {
		t.Run(bad, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CBUS_DIR", root)
			if _, err := ReserveAlias("dev", bad, OriginFresh, ""); err == nil {
				t.Fatalf("ReserveAlias(dev, %q) succeeded", bad)
			}
			if _, err := ReserveAlias(bad, "peer", OriginFresh, ""); err == nil {
				t.Fatalf("ReserveAlias(%q, peer) succeeded", bad)
			}
			if es, _ := os.ReadDir(root); len(es) != 0 {
				t.Errorf("a refused reservation still created %d entries", len(es))
			}
		})
	}
}
