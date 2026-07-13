package core

import "regexp"

// nameRe is the ONE name-validation rule shared verbatim by the client
// (bin/cbus:24 `valid_name`) and the relay (formerly main.go:33-37 `validName`).
// It applies to channels, aliases, and relay host names, and is what makes names
// safe to use directly as spool/`$CBUS_DIR` path segments (no traversal): `/` and
// `@` are structural separators that can never appear inside a name.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidName reports whether s is an acceptable channel/alias/host name. It is the
// wire authority — the relay gates `/send` and `/tail` on it, so a port must keep
// it byte-identical. Documented properties preserved as-is (protocol.md §1.1),
// each a quirk a later phase may tighten CLIENT-side only:
//
//   - no length cap (de-facto bound is filesystem NAME_MAX);
//   - all-digit names are legal (the client's jset then stores them as JSON ints);
//   - leading-dot names pass ("." / ".." excepted) yet collide with the invisible
//     .remote/.reap trees and every `*/` glob;
//   - leading-hyphen names pass (`-a`, `--force`), unusable as CLI filters with no
//     `--` terminator.
func ValidName(s string) bool {
	return s != "." && s != ".." && nameRe.MatchString(s)
}
