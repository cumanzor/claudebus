package core

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// accepted (all as-is per protocol.md §1.1)
		{"main", true},
		{"fork-1", true},
		{"a.b_c-d", true},
		{"42", true},      // all-digit names are legal
		{".remote", true}, // leading dot passes (quirk)
		{"-a", true},      // leading hyphen passes (quirk)
		{"--force", true}, // ...even flag-shaped
		{"...", true},     // only "." and ".." are excepted, not "..."
		{"a..b", true},
		{"_", true},
		{"-", true},
		// rejected
		{"", false},    // empty fails the + quantifier
		{".", false},   // excepted literal
		{"..", false},  // excepted literal
		{"a/b", false}, // / is a structural separator
		{"a@b", false}, // @ is a structural separator
		{"a b", false}, // space not in the class
		{"a\tb", false},
		{"a\nb", false},
		{"café", false}, // non-ASCII rejected
	}
	for _, c := range cases {
		if got := ValidName(c.in); got != c.want {
			t.Errorf("ValidName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestValidNameStaysTheWireAuthority pins the half that must NOT move. The relay
// gates /send and /tail on ValidName, so tightening the client must not narrow it:
// a leading-dot or leading-dash name still has to pass here, or a name an older or
// third-party client legitimately created becomes unroutable on the wire.
func TestValidNameStaysTheWireAuthority(t *testing.T) {
	for _, s := range []string{".remote", ".hidden", "-a", "--force", "..."} {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false — the wire rule narrowed; the client tightening belongs in ValidStoreName", s)
		}
	}
}

func TestValidStoreName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// accepted: everything ValidName takes that does not LEAD with . or -
		{"main", true},
		{"fork-1", true},  // interior hyphen is fine
		{"a.b_c-d", true}, // interior dot is fine
		{"42", true},      // all-digit stays legal, unchanged from ValidName
		{"_", true},
		{"a..b", true},
		// rejected by the tightening (each of these ValidName ACCEPTS)
		{".remote", false},
		{".hidden", false},
		{"...", false},
		{"-a", false},
		{"--force", false},
		{"-", false},
		// trailing dot: windows strips it, so the store creates the dir under a DIFFERENT
		// name and the peer's identity stops matching its path. Legal on this filesystem,
		// refused fleet-wide. "a..b" above is the discriminating neighbour — an INTERIOR
		// dot run stays legal, so a mutant rejecting any dot at all fails there rather
		// than passing this block. An all-dots name never reaches this rule: the
		// leading-dot clause takes it first, which is why "..." sits in the block above.
		{"foo.", false},
		{"foo...", false},
		{"a.b.", false},
		// rejected by ValidName already — the tightening is additive, not a rewrite
		{"", false},
		{".", false},
		{"..", false},
		{"a/b", false},
		{"a@b", false},
		{"a b", false},
		{"café", false},
	}
	for _, c := range cases {
		if got := ValidStoreName(c.in); got != c.want {
			t.Errorf("ValidStoreName(%q) = %v, want %v", c.in, got, c.want)
		}
		if c.want && !ValidName(c.in) {
			t.Errorf("ValidStoreName(%q) accepts what ValidName rejects — it must be a strict subset", c.in)
		}
		// every refusal has to explain itself: an unexplained no on a name the local
		// filesystem accepts reads as a bug and invites a workaround.
		why := StoreNameReason(c.in)
		if (why == "") != c.want {
			t.Errorf("StoreNameReason(%q) = %q but ValidStoreName = %v — the bool and the reason disagree", c.in, why, c.want)
		}
		// a reason that shows an example must show THIS name's, not a hardcoded one: a
		// user who typed "bar..." being told it becomes "foo" reads as the tool being
		// confused rather than the name being wrong. Keyed on the REASON GIVEN, not on the
		// input's shape: "..." ends in a dot but is caught by the leading-dot branch one
		// clause earlier, so keying on the suffix would demand interpolation from a
		// message that never claimed to do any...
		if strings.Contains(why, "may not end with '.'") && !strings.Contains(why, c.in) {
			t.Errorf("StoreNameReason(%q) does not name the rejected input: %q", c.in, why)
		}
		// ...and this catches the regression at every branch, including any added later:
		// no reason may name a stem the caller never typed.
		if why != "" && strings.Contains(why, "foo") && !strings.Contains(c.in, "foo") {
			t.Errorf("StoreNameReason(%q) carries a hardcoded example name: %q", c.in, why)
		}
	}
}
