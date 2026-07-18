package main

import "testing"

func TestVersionMatchesTagGate(t *testing.T) {
	// S4 pure compare — the version-gate's whole decision. Mutation-tested: flipping
	// any row exposes a gate that would let a wrong asset through.
	for _, tc := range []struct {
		reported, tag string
		want          bool
	}{
		{"v0.1.0", "v0.1.0", true},   // exact
		{"0.1.0", "v0.1.0", true},    // 'v' normalized on both sides
		{"v0.1.0", "0.1.0", true},    // and the other way
		{"v0.1.0", "v0.2.0", false},  // wrong version (stale/mispublished asset)
		{"v0.2.0", "v0.1.0", false},  // newer-than-asked is still a mismatch
		{"dev", "v0.1.0", false},     // a dev binary is never the release we asked for
		{"", "v0.1.0", false},        // no version parsed -> refuse
		{"garbage", "v0.1.0", false}, // corrupt output -> refuse
	} {
		if got := versionMatchesTag(tc.reported, tc.tag); got != tc.want {
			t.Errorf("versionMatchesTag(%q,%q) = %v, want %v", tc.reported, tc.tag, got, tc.want)
		}
	}
}

func TestIsReleaseVersionClassification(t *testing.T) {
	// S6: the stable path selects only clean X.Y.Z; a '-' tag (prerelease or
	// git-describe) and dev are non-release, so a prerelease can never be picked.
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"0.1.0", true}, {"v0.1.0", true}, {"10.20.30", true},
		{"0.2.0-rc1", false}, {"v0.2.0-rc1", false},
		{"0.1.6-3-gabcd-dirty", false},
		{"dev", false}, {"", false}, {"0.1", false}, {"0.1.0.0", false},
	} {
		if got := isReleaseVersion(tc.v); got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

func TestNewerRelease(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.1", "0.1.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.2.0-rc1", "0.1.0", false}, // a prerelease never nags
		{"0.2.0", "dev", false},       // a dev build never nags
	} {
		if got := newerRelease(tc.latest, tc.current); got != tc.want {
			t.Errorf("newerRelease(%q,%q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestParseVersionOutput(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"cbus-go v0.1.0\n", "v0.1.0"},
		{"cbus-go dev", "dev"},
		{"  cbus-go   0.2.0  ", "0.2.0"},
		{"", ""},
	} {
		if got := parseVersionOutput(tc.in); got != tc.want {
			t.Errorf("parseVersionOutput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
