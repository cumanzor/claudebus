package main

import (
	"os"
	"strings"
)

// repoSlug is the GitHub "owner/repo" the release verbs (selfupdate, update-check)
// talk to. It is baked at release time via -ldflags "-X main.repoSlug=owner/repo"
// and is EMPTY in dev/source builds on purpose: keeping a personal slug out of
// committed source lets the repo change visibility (public<->private) without ever
// needing another history scrub (cbus-7sg D26).
var repoSlug string

// resolveRepoSlug returns the effective slug and whether one is configured. A
// runtime $CBUS_REPO overrides the baked default, so a dev build works by exporting
// it. With neither set, ok is false and the caller prints repoSlugRemedy rather than
// guessing a slug.
func resolveRepoSlug() (slug string, ok bool) {
	if env := strings.TrimSpace(os.Getenv("CBUS_REPO")); env != "" {
		return env, true
	}
	if s := strings.TrimSpace(repoSlug); s != "" {
		return s, true
	}
	return "", false
}

// repoSlugRemedy is the one-line fix printed when no slug is configured.
const repoSlugRemedy = `no release repo configured — set CBUS_REPO=owner/repo, or use a released binary (its slug is baked in at build)`
