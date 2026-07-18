package claudebus

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEmbedCountAndSourceMatch is S2, two guards in one:
//   - the embed-count guard: the embedded set is EXACTLY these files, so adding or
//     removing a command/role without updating the expectation fails the build.
//   - the runtime-FS canary: the bytes the binary serves (the embed snapshot) equal
//     the repo source files. go:embed snapshots at build, so repo-vs-repo alone would
//     not prove the binary's own copies match — this compares the served content.
func TestEmbedCountAndSourceMatch(t *testing.T) {
	assertEmbed(t, Commands, "commands", []string{
		"bus-branch.md", "bus-formation.md", "bus-join.md", "bus-rename.md", "bus-spawn.md",
	})
	assertEmbed(t, Roles, "roles", []string{
		"coder.md", "documenter.md", "orchestrator.md", "reviewer.md",
	})
}

func assertEmbed(t *testing.T, fsys embed.FS, subdir string, want []string) {
	t.Helper()
	entries, err := fs.ReadDir(fsys, subdir)
	if err != nil {
		t.Fatalf("read embedded %s: %v", subdir, err)
	}
	var got []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s embed set:\n got %v\nwant %v (a file added/removed without updating this test)", subdir, got, want)
	}
	// the served bytes must equal the repo source (test cwd is the package dir = repo root).
	for _, name := range got {
		emb, err := fs.ReadFile(fsys, subdir+"/"+name)
		if err != nil {
			t.Errorf("read embed %s/%s: %v", subdir, name, err)
			continue
		}
		src, err := os.ReadFile(filepath.Join(subdir, name))
		if err != nil {
			t.Errorf("read source %s/%s: %v", subdir, name, err)
			continue
		}
		if !bytes.Equal(emb, src) {
			t.Errorf("%s/%s: embedded bytes differ from the repo source (stale embed?)", subdir, name)
		}
	}
}
