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
		{"42", true},       // all-digit names are legal
		{".remote", true},  // leading dot passes (quirk)
		{"-a", true},       // leading hyphen passes (quirk)
		{"--force", true},  // ...even flag-shaped
		{"...", true},      // only "." and ".." are excepted, not "..."
		{"a..b", true},
		{"_", true},
		{"-", true},
		// rejected
		{"", false},   // empty fails the + quantifier
		{".", false},  // excepted literal
		{"..", false}, // excepted literal
		{"a/b", false},   // / is a structural separator
		{"a@b", false},   // @ is a structural separator
		{"a b", false},   // space not in the class
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
