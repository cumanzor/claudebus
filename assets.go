// Package claudebus embeds the repo's committed skill commands and role prompts so
// the cbus binary can install them on any machine with no checkout present. The repo
// files stay the single source of truth — the roles canary and LoadRole read the same
// copies — and go:embed reaches them from the module root, which cmd/cbus (a subdir)
// cannot, since embed forbids a ".." path (cbus-7sg D23).
package claudebus

import "embed"

// Commands holds commands/*.md (the slash-command skills: the /bus-* family plus
// /save-formation) as the binary serves them.
//
//go:embed commands/*.md
var Commands embed.FS

// CodexSkills holds the native Codex skill entrypoints, installed as
// <skills-root>/<skill-name>/SKILL.md.
//
//go:embed skills/codex/*/SKILL.md
var CodexSkills embed.FS

// Roles holds roles/*.md, installed to $CBUS_DIR/roles as the LoadRole fallback for
// spawns outside a repo. The doctrine block is duplicated 4x BY RULING; the embed
// copies the files verbatim and never DRYs them (cbus-7sg).
//
//go:embed roles/*.md
var Roles embed.FS
