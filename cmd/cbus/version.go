package main

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// normalizeVersion strips a leading 'v' so "v0.1.6" and "0.1.6" compare equal;
// empty becomes "dev".
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}

var releaseVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// isReleaseVersion reports whether v is a clean release tag (X.Y.Z). A prerelease
// ("0.2.0-rc1"), a git-describe build ("0.1.6-3-gabc-dirty"), and "dev" are all
// NON-release, so a dev build never nags about a release and the stable path never
// selects a '-' tag (S6).
func isReleaseVersion(v string) bool {
	return releaseVersionPattern.MatchString(normalizeVersion(v))
}

// newerRelease is true iff both look like clean release tags AND latest > current.
func newerRelease(latest, current string) bool {
	if !isReleaseVersion(latest) || !isReleaseVersion(current) {
		return false
	}
	return versionGreater(latest, current)
}

func versionGreater(a, b string) bool {
	ap, bp := versionParts(a), versionParts(b)
	if ap == nil || bp == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if ap[i] != bp[i] {
			return ap[i] > bp[i]
		}
	}
	return false
}

func versionParts(v string) []int {
	parts := strings.Split(normalizeVersion(v), ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// assetNameFor is the release asset filename for a platform: cbus-<os>-<arch>. It is
// the EXACT string selfupdate hands gh as --pattern, and it must equal the Makefile's
// dist output (unix-only, no .exe) — TestAssetNameMatchesMakefile pins that (S5).
func assetNameFor(goos, goarch string) string {
	return "cbus-" + goos + "-" + goarch
}

func assetName() string {
	return assetNameFor(runtime.GOOS, runtime.GOARCH)
}

// parseVersionOutput pulls the version token out of `cbus --version` ("cbus-go
// v0.1.0" -> "v0.1.0"), so a downloaded binary can be checked before it is swapped.
func parseVersionOutput(s string) string {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// versionMatchesTag is the version-gate's pure compare (S4): the downloaded binary
// must report the version we asked gh for, so a wrong-asset or corrupt download never
// replaces a working binary. Both sides are normalized, so "v0.1.0" == "0.1.0".
func versionMatchesTag(reported, tag string) bool {
	r := normalizeVersion(reported)
	return r != "" && r != "dev" && r == normalizeVersion(tag)
}
