package core

import (
	"fmt"
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
// it byte-identical. Documented properties preserved as-is (protocol.md §1.1). The
// first two are still open quirks a later phase may tighten CLIENT-side only; the last
// two HAVE been tightened, in ValidStoreName below — do not "fix" them here, narrowing
// this regex would change the wire:
//
//   - no length cap (de-facto bound is filesystem NAME_MAX);
//   - all-digit names are legal (the client's jset then stores them as JSON ints);
//   - leading-dot names pass ("." / ".." excepted) yet collide with the invisible
//     .remote/.reap trees and every `*/` glob — REJECTED at creation by ValidStoreName;
//   - leading-hyphen names pass (`-a`, `--force`), unusable as CLI filters with no
//     `--` terminator — REJECTED at creation by ValidStoreName.
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
//     `--` terminator, and a forked child's CLI parses it as a flag;
//   - trailing '.' — windows STRIPS trailing dots from a path component, so `cbus join
//     foo...` creates the directory under a DIFFERENT name, `foo`, and the peer's
//     recorded identity no longer matches its own path. Measured end to end: it does
//     not silently corrupt, it pollutes the store with a wrong-named dir and then fails
//     loudly. Rejected fleet-wide rather than per-platform, because a name that resolves
//     to two different directories on two machines in one fleet is worse than a name
//     refused on both. (An ALL-dots name never reaches this rule; the leading-dot
//     clause above already refuses it.)
//
// It is ADDITIVE and never a replacement. ValidName remains the wire authority (the
// relay gates /send and /tail on it), so a name this rejects can still arrive from an
// older or third-party client, and ADDRESSING an existing name still goes through
// ValidName — otherwise `cbus unregister <ch>/.foo`, the only cleanup path for a
// legacy bad name, would be unable to name its target.
// StoreNameReason explains why ValidStoreName rejects s, or "" when it accepts.
//
// The reason has to travel with the refusal, because it does not make sense on its own
// wherever the user is standing. A trailing dot is perfectly legal on APFS and ext4, so
// refusing one there while saying nothing reads as a bug in cbus and invites the user to
// work around it. It is refused fleet-wide because the name means something different on
// a windows peer, and a name that means two things in one fleet is worse than a name
// refused in both places.
func StoreNameReason(s string) string {
	switch {
	case !ValidName(s):
		return "must be [A-Za-z0-9._-] and not \".\" or \"..\""
	case strings.HasPrefix(s, "."):
		return "may not start with '.': a leading dot hides it from list, channels and whoami"
	case strings.HasPrefix(s, "-"):
		return "may not start with '-': flag-shaped, so a CLI filter or a forked child parses it as a flag"
	case strings.HasSuffix(s, "."):
		// the example is INTERPOLATED, never illustrative: a hardcoded one tells a user who
		// typed "bar..." that their name becomes "foo", which is someone else's name and
		// reads as the tool being confused rather than the name being wrong.
		return fmt.Sprintf("may not end with '.': windows strips trailing dots from a path component, so a windows peer would "+
			"create %q as %q and its identity would not match its own path — refused everywhere, including filesystems like "+
			"this one that accept it", s, strings.TrimRight(s, "."))
	}
	return ""
}

func ValidStoreName(s string) bool {
	return ValidName(s) && !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "-") &&
		!strings.HasSuffix(s, ".")
}
