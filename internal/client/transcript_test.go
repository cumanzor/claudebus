package client

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTranscript plants a transcript at <cfg>/projects/<project>/<sid>.jsonl.
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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

func TestTranscriptRootsBareShellFindsProfiled(t *testing.T) {
	// the ccresume-button finding: a bare or GUI shell has NO CLAUDE_CONFIG_DIR,
	// and the profile root must resolve from HOME + the recorded profile anyway —
	// the envelope is the authority, the env is a hint
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	sid := "bare-shell-sid"
	want := writeTranscript(t, filepath.Join(home, ".ccs", "instances", "work"), "-Users-dev-proj", sid)
	got, ok := TranscriptPath("work", sid)
	if !ok || got != want {
		t.Fatalf("bare-shell profiled lookup = %q,%v want %q", got, ok, want)
	}
	// the inverse stays true and documented: blank profile + bare shell cannot
	// see instance roots THROUGH THIS LOOKUP — the recovery for pre-profile
	// envelopes is InstanceProfiles, a deliberate named sweep, never an implicit
	// widening of every caller's search (cbus-kl4)
	if _, ok := TranscriptPath("", sid); ok {
		t.Fatal("blank profile from a bare shell must not find instance transcripts")
	}
}

// TestInstanceProfilesSweep: the recovery input for envelopes that record no
// profile — the transcript's own location names the profile. HOME-derived like
// transcriptRoots, so a bare shell (no CLAUDE_CONFIG_DIR) can still sweep.
func TestInstanceProfilesSweep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	work := filepath.Join(home, ".ccs", "instances", "work")
	personal := filepath.Join(home, ".ccs", "instances", "personal")
	sidWork := "aaaaaaaa-1111-2222-3333-444444444444"
	sidBoth := "bbbbbbbb-1111-2222-3333-444444444444"
	writeTranscript(t, work, "-Users-dev-work-repo", sidWork)
	writeTranscript(t, work, "-Users-dev-work-repo", sidBoth)
	writeTranscript(t, personal, "-Users-dev-play-repo", sidBoth)

	if got := InstanceProfiles(sidWork); len(got) != 1 || got[0] != "work" {
		t.Errorf("single-owner sweep = %v, want [work]", got)
	}
	// ambiguity is reported sorted, never silently resolved by glob order
	if got := InstanceProfiles(sidBoth); len(got) != 2 || got[0] != "personal" || got[1] != "work" {
		t.Errorf("two-owner sweep = %v, want [personal work]", got)
	}
	if got := InstanceProfiles("ffffffff-0000-0000-0000-000000000000"); len(got) != 0 {
		t.Errorf("absent sid swept to %v, want none", got)
	}
	// same untrusted-text screen as TranscriptPath: globs and traversals die
	for _, bad := range []string{"*", "../../../etc", "a/b", ""} {
		if got := InstanceProfiles(bad); len(got) != 0 {
			t.Errorf("InstanceProfiles(%q) = %v, want refused", bad, got)
		}
	}
}
