package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestInstallAssetsInstallsEveryAssetType(t *testing.T) {
	home := t.TempDir()
	testHome(t, home)
	t.Setenv("CBUS_DIR", filepath.Join(home, ".claude-bus"))
	t.Setenv("CODEX_HOME", "")
	var rc int
	captureStdout(t, func() { rc = runInstallAssets(nil) })
	if rc != 0 {
		t.Fatalf("install-assets exit %d", rc)
	}
	commands, _ := defaultCommandsDir()
	skills, _ := defaultCodexSkillsDir()
	mods, _ := defaultModsDir()
	for name, dir := range map[string]string{"commands": commands, "roles": defaultRolesDir(), "codex skills": skills, "mods": mods} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			t.Errorf("%s: nothing installed in %s (%v)", name, dir, err)
		}
	}
}

// every install-<asset> verb the usage advertises must be in assetInstalls, or
// an update would skip it.
func TestInstallAssetsCoversEveryInstallVerb(t *testing.T) {
	advertised := regexp.MustCompile(`(?m)^  cbus (install-[a-z-]+)`).FindAllStringSubmatch(usage, -1)
	var covered []string
	for _, a := range assetInstalls {
		covered = append(covered, a.verb)
	}
	for _, m := range advertised {
		if m[1] != "install-assets" && !slices.Contains(covered, m[1]) {
			t.Errorf("%s is advertised but install-assets does not run it", m[1])
		}
	}
	if len(advertised) < 5 {
		t.Fatalf("usage lists %d install verbs; the pattern no longer matches usage", len(advertised))
	}
}

// refreshFixture is a stand-in new binary that logs each invocation and
// advertises install-assets in its usage only when asked to.
func refreshFixture(t *testing.T, modern bool) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	help := "  cbus install-commands"
	if modern {
		help += "\n  cbus install-assets"
	}
	script := "#!/bin/sh\nif [ \"$1\" = --help ]; then printf '%s\\n' '" + help + "'; exit 0; fi\necho \"$*\" >> '" + log + "'\n"
	bin = filepath.Join(dir, "cbus-new")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestRefreshAssetsRunsTheNewBinarysInstallAssets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture binary is a #!/bin/sh script")
	}
	bin, log := refreshFixture(t, true)
	captureStdout(t, func() { refreshAssets(bin) })
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "install-assets" {
		t.Fatalf("a binary with install-assets must get exactly that one call, got %q", got)
	}
}

func TestRefreshAssetsFallsBackForAnOlderBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture binary is a #!/bin/sh script")
	}
	bin, log := refreshFixture(t, false)
	captureStdout(t, func() { refreshAssets(bin) })
	got, _ := os.ReadFile(log)
	want := "install-commands --force\ninstall-roles --force\ninstall-codex-skills\ninstall-mods --force"
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("an older binary must get the legacy list, got %q", got)
	}
}
