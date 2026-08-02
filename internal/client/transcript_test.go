package client

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeTranscript plants a transcript at <cfg>/projects/<project>/<sid>.jsonl.
// setHome points os.UserHomeDir at dir. The variable it reads is platform-specific —
// USERPROFILE on windows, HOME elsewhere — so setting only HOME leaves the resolver
// pointed at the real profile and every lookup misses the fixture entirely.
func setHome(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
		return
	}
	t.Setenv("HOME", dir)
}

func writeTranscript(t *testing.T, cfg, project, sid string) string {
	t.Helper()
	dir := filepath.Join(cfg, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTranscriptPathFindsSidAcrossProjects(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ccs", "instances", "personal")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	setHome(t, home)
	sid := "a26d120e-4d73-4d91-8550-498ab65a5107"
	want := writeTranscript(t, cfg, "-Users-dev-repos-AI-claudebus", sid)

	got, ok := TranscriptPath("", sid)
	if !ok || got != want {
		t.Errorf("TranscriptPath: got (%q,%v) want (%q,true)", got, ok, want)
	}
	// the whole point of globbing: the project dir name is never reconstructed, so
	// a peer whose cwd moved since save still resolves.
	moved := "-Users-dev-somewhere-else-entirely"
	sid2 := "11111111-2222-3333-4444-555555555555"
	want2 := writeTranscript(t, cfg, moved, sid2)
	if got, ok := TranscriptPath("", sid2); !ok || got != want2 {
		t.Errorf("moved cwd: got (%q,%v) want (%q,true)", got, ok, want2)
	}
	if _, ok := TranscriptPath("", "ffffffff-0000-0000-0000-000000000000"); ok {
		t.Error("absent sid: want not found")
	}
}

// TestTranscriptPathProfileSibling: a peer records the CCS profile it ran under, so
// a formation applied from one profile can still see another profile's transcripts.
func TestTranscriptPathProfileSibling(t *testing.T) {
	home := t.TempDir()
	personal := filepath.Join(home, ".ccs", "instances", "personal")
	work := filepath.Join(home, ".ccs", "instances", "work")
	t.Setenv("CLAUDE_CONFIG_DIR", personal)
	setHome(t, home)
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	want := writeTranscript(t, work, "-Users-dev-work-repo", sid)

	if _, ok := TranscriptPath("", sid); ok {
		t.Error("without the profile hint the work-instance transcript must not resolve")
	}
	got, ok := TranscriptPath("work", sid)
	if !ok || got != want {
		t.Errorf("profile sibling: got (%q,%v) want (%q,true)", got, ok, want)
	}
}

func TestTranscriptPathBareClaudeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	setHome(t, home)
	sid := "0f0f0f0f-1111-2222-3333-444444444444"
	want := writeTranscript(t, filepath.Join(home, ".claude"), "-Users-dev-proj", sid)
	got, ok := TranscriptPath("", sid)
	if !ok || got != want {
		t.Errorf("bare ~/.claude: got (%q,%v) want (%q,true)", got, ok, want)
	}
}

// TestTranscriptPathRejectsInjection: the sid is hand-edited text used as BOTH a
// glob pattern and a path segment. A wildcard must not match a real transcript,
// and a traversal must not escape the projects tree.
func TestTranscriptPathRejectsInjection(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ccs", "instances", "personal")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	setHome(t, home)
	writeTranscript(t, cfg, "-proj", "real-sid-here")
	outside := filepath.Join(home, "secret.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"*",                      // glob: would match the real transcript
		"real-sid-*",             // glob prefix
		"?????????",              // glob single-char
		"[a-z]*",                 // glob class
		"../../../secret",        // traversal
		"../-proj/real-sid-here", // traversal back into a sibling
		"",                       // empty
		"a/b",                    // path separator
		"sid with space",         // not a uuid shape
		"sid\x00null",            // NUL
	} {
		if got, ok := TranscriptPath("", bad); ok {
			t.Errorf("TranscriptPath(%q) must be refused, resolved to %q", bad, got)
		}
	}
}

// TestTranscriptRootsRejectsBadProfile: profile is a path segment from the same
// hand-edited file.
func TestTranscriptRootsRejectsBadProfile(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ccs", "instances", "personal")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	setHome(t, home)
	roots := transcriptRoots("../../../etc")
	for _, r := range roots {
		if filepath.Clean(r) != r || !filepath.IsAbs(r) {
			t.Errorf("root %q is not a clean absolute path", r)
		}
		if got := filepath.Join(home, ".ccs", "instances", "..", "..", "..", "etc", "projects"); r == filepath.Clean(got) {
			t.Errorf("bad profile escaped the instances tree: %q", r)
		}
	}
}

func TestDedupeStrings(t *testing.T) {
	got := dedupeStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
