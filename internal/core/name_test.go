package core

import "testing"

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
	}
}
