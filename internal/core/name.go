package core

import (
	"regexp"
	"strings"
)

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

// ValidStoreName is the client-side tightening ValidName's doc earmarks: it gates a
// name the client is about to CREATE as a store path segment. Two of the quirks above
// are rejected here, both because the name survives creation and then misbehaves:
//
//   - leading '.' — the client's own traversals skip dot-prefixed entries to stay
//     blind to the .remote/.reap trees, so a dot-named peer or channel is written
//     successfully and is thereafter invisible to list, channels, and ResolveSelf;
//   - leading '-' — flag-shaped, so it cannot be passed as a CLI filter without a
//     `--` terminator, and a forked child's CLI parses it as a flag.
//
// It is ADDITIVE and never a replacement. ValidName remains the wire authority (the
// relay gates /send and /tail on it), so a name this rejects can still arrive from an
// older or third-party client, and ADDRESSING an existing name still goes through
// ValidName — otherwise `cbus unregister <ch>/.foo`, the only cleanup path for a
// legacy bad name, would be unable to name its target.
func ValidStoreName(s string) bool {
	return ValidName(s) && !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "-")
}
