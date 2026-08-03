package client

import (
	"os"
	"path/filepath"
	"regexp"

	"claudebus/internal/core"
)

// sidRe is the session-id shape: a uuid, no path or glob metacharacters. The
// envelope is hand-edited, so a sid arrives as untrusted text and is a path
// segment AND a glob pattern below — `*` would turn a lookup into a wildcard
// scan, and a `/` would walk out of the projects tree.
var sidRe = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// TranscriptPath locates a session's transcript, the file `claude --resume <sid>`
// needs. It GLOBS for the sid instead of rebuilding the project dir from the
// peer's cwd: Claude Code munges a cwd into that directory name by a rule this
// tool would have to duplicate and keep in sync forever, and a peer whose cwd
// moved since the save would then read as stale while --resume still worked fine.
// A sid is unique across projects, so matching on it alone is exact and survives
// both a cwd move and a change to the munging rule.
//
// The lookup is best-effort by nature: it proves a transcript exists on THIS
// machine under a config dir we know to look in, never that a resume will
// succeed. Callers report it as evidence, not as a verdict.
func TranscriptPath(profile, sid string) (path string, ok bool) {
	if !sidRe.MatchString(sid) {
		return "", false
	}
	for _, root := range transcriptRoots(profile) {
		matches, err := filepath.Glob(filepath.Join(root, "*", sid+".jsonl"))
		if err == nil && len(matches) > 0 {
			return matches[0], true
		}
	}
	return "", false
}

// transcriptRoots are the projects/ dirs a transcript may live under, most
// specific first: the peer's own CCS profile (a sibling instance dir), this
// session's own config dir, then the bare ~/.claude default. The CCS instance
// shape is the same one freshLaunchArgv already relies on to relaunch a child
// under its profile — this adds no new dependency on that layout, and a
// non-CCS machine simply falls through to ~/.claude.
func transcriptRoots(profile string) []string {
	var roots []string
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	// profile is a path segment from a hand-edited file — screen it like any name.
	if profile != "" && core.ValidName(profile) && isCCSInstanceDir(cfg) {
		roots = append(roots, filepath.Join(filepath.Dir(cfg), profile, "projects"))
	}
	if cfg != "" {
		roots = append(roots, filepath.Join(cfg, "projects"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "projects"))
	}
	return dedupeStrings(roots)
}

// isCCSInstanceDir reports whether cfg names a CCS profile instance, .../.ccs/instances/<name>.
//
// Structural, not textual. This was a strings.Contains against a hardcoded forward-slash
// path fragment, which a real windows CLAUDE_CONFIG_DIR never matches, so the
// profile-sibling root was silently never added and a peer's recorded profile stopped
// resolving with no error anywhere. Comparing components asks the question the literal
// was approximating, and filepath answers it on both separators.
//
// The literal is deliberately not reproduced here: the acceptance check for this fix is
// that the fragment greps to ZERO tree-wide, and a comment quoting it would defeat a
// mechanical check for the sake of a historical note prose can carry instead.
func isCCSInstanceDir(cfg string) bool {
	parent := filepath.Dir(cfg)
	return filepath.Base(parent) == "instances" && filepath.Base(filepath.Dir(parent)) == ".ccs"
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
