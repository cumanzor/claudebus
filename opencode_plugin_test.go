package claudebus

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpencodePluginEmbedMatchesSource(t *testing.T) {
	entries, err := fs.ReadDir(OpencodePlugins, "integrations/opencode")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if e.IsDir() {
			sub, _ := fs.ReadDir(OpencodePlugins, "integrations/opencode/"+e.Name())
			for _, f := range sub {
				got = append(got, e.Name()+"/"+f.Name())
			}
			continue
		}
		got = append(got, e.Name())
	}
	if want := []string{"cbus-server.js", "cbus-tui.js", "commands/bus-join.md"}; !equalStrings(got, want) {
		t.Fatalf("embedded OpenCode plugins = %v, want %v (tests and docs stay out of the install)", got, want)
	}
	for _, name := range got {
		served, _ := fs.ReadFile(OpencodePlugins, "integrations/opencode/"+name)
		source, err := os.ReadFile(filepath.Join("integrations", "opencode", filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(served, source) {
			t.Errorf("%s: embedded bytes differ from the source file (%v)", name, err)
		}
	}
}

// The plugin protocol is exercised against a fake OpenCode api under node;
// live OpenCode acceptance is a separate, manual gate.
func TestOpencodePluginProtocol(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; plugin protocol tests need node 22 or later")
	}
	cmd := exec.Command(node, "--test", "--test-timeout=5000", "plugin.test.mjs")
	cmd.Dir = filepath.Join("integrations", "opencode")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
