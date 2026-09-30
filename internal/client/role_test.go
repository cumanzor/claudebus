package client

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeRoleFile(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "roles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRoleModelParsing(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"# r\nMODEL: fable\n\n## Mission\n", "fable"},
		{"# r\n\n## Mission\nno model line\n", ""},
		{"# r\nMODEL: -x\n", ""},                      // flag-shaped: same screen as --model
		{"# r\nMODEL: bad model\n", ""},               // not alias-legal
		{"# r\nMODEL:\n", ""},                         // empty value
		{"# r\nMODEL: opus\nMODEL: sonnet\n", "opus"}, // first line wins
	} {
		if got := roleModel(tc.body); got != tc.want {
			t.Fatalf("roleModel(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// TestLoadRoleCBUSDirFallback: outside any git toplevel, roles resolve from
// $CBUS_DIR/roles.
func TestLoadRoleCBUSDirFallback(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "tester", "# tester\nMODEL: sonnet\n\n## Mission\nbe tested.\n")
	body, model, err := LoadRole("tester")
	if err != nil {
		t.Fatal(err)
	}
	if model != "sonnet" {
		t.Fatalf("model = %q", model)
	}
	if !strings.Contains(body, "## Mission") {
		t.Fatalf("body = %q", body)
	}
}

// TestLoadRoleRepoToplevel: run from inside the repo, the committed
// roles/coder.md resolves first (its MODEL line is ruled: claude-opus-5-5,
// a full id on purpose, since the bare "opus" alias moves with each release).
func TestLoadRoleRepoToplevel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	body, model, err := LoadRole("coder")
	if err != nil {
		t.Skipf("repo roles/ not present here: %v", err)
	}
	if model != "claude-opus-5-5" {
		t.Fatalf("coder MODEL = %q, want claude-opus-5-5", model)
	}
	if !strings.Contains(strings.ToLower(body), "# coder") {
		t.Fatalf("body head = %q", body[:min(80, len(body))])
	}
}

func TestLoadRoleNotFoundAndBadName(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	// the tried-path fragment is filepath.Join-built in LoadRole, so the expectation is too
	wantPath := filepath.Join("roles", "ghost.md")
	if _, _, err := LoadRole("ghost"); err == nil || !strings.Contains(err.Error(), `role "ghost" not found`) || !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("not-found err = %v", err)
	}
	for _, bad := range []string{"-x", "a b", ""} {
		if _, _, err := LoadRole(bad); err == nil || !strings.Contains(err.Error(), "bad role") {
			t.Fatalf("bad role %q err = %v", bad, err)
		}
	}
}

// TestSpawnRoleDefaultsAliasAndModel: --role alone fixes the alias to the role
// name, takes the model from the file's MODEL line, and rides the brief after
// the aliased spawn prompt.
func TestSpawnRoleDefaultsAliasAndModel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(t.TempDir())
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	f := &fakeForker{}
	_, child, err := Spawn("window", "dev", "", "", "documenter", f)
	if err != nil {
		t.Fatal(err)
	}
	if child != "documenter" {
		t.Fatalf("child = %q, want documenter", child)
	}
	argv := f.spec.Argv
	if i := slices.Index(argv, "--name"); i < 0 || argv[i+1] != "documenter" {
		t.Fatalf("argv = %v", argv)
	}
	if i := slices.Index(argv, "--model"); i < 0 || argv[i+1] != "sonnet" {
		t.Fatalf("argv = %v", argv)
	}
	prompt := argv[len(argv)-1]
	base := SpawnPromptAliased("dev", "documenter")
	if !strings.HasPrefix(prompt, base) {
		t.Fatalf("prompt must open with the aliased spawn prompt:\n%s", prompt)
	}
	if brief := strings.TrimPrefix(prompt, base); !strings.Contains(brief, "## Mission") {
		t.Fatalf("role brief must ride AFTER the spawn prompt, got tail %q", brief)
	}
	if !fileExists(filepath.Join(CBUSDir(), "dev", "documenter", "meta.json")) {
		t.Fatal("--role must reserve the role-named alias")
	}
}

// TestSpawnRoleExplicitOverrides: explicit --model and --name beat the role
// file's defaults; the brief still rides.
func TestSpawnRoleExplicitOverrides(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(t.TempDir())
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	f := &fakeForker{}
	_, child, err := Spawn("window", "dev", "opus", "scribe", "documenter", f)
	if err != nil {
		t.Fatal(err)
	}
	if child != "scribe" {
		t.Fatalf("child = %q, want scribe", child)
	}
	argv := f.spec.Argv
	if i := slices.Index(argv, "--model"); i < 0 || argv[i+1] != "opus" {
		t.Fatalf("explicit model must win: %v", argv)
	}
	if !strings.Contains(argv[len(argv)-1], "## Mission") {
		t.Fatal("brief must still ride with overrides")
	}
}

// TestSpawnRoleRemote: a role pre-assigns the relay alias and the brief rides
// the remote aliased prompt; no local state is created.
func TestSpawnRoleRemote(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "tester", "# tester\nMODEL: sonnet\n\n## Mission\nbe tested.\n")
	f := &fakeForker{}
	_, child, err := Spawn("tab", "dev@server", "", "", "tester", f)
	if err != nil {
		t.Fatal(err)
	}
	if child != "tester" {
		t.Fatalf("remote child = %q", child)
	}
	prompt := f.spec.Argv[len(f.spec.Argv)-1]
	if !strings.HasPrefix(prompt, SpawnPromptAliased("dev@server", "tester")) || !strings.Contains(prompt, "## Mission") {
		t.Fatalf("remote prompt = %q", prompt)
	}
	if dirExists(filepath.Join(CBUSDir(), "dev@server")) {
		t.Fatal("remote spawn must not create local state")
	}
}

// TestSpawnRoleMissingFailsBeforeReserve: an unknown role errors before any
// side effect (no reservation, no fork).
func TestSpawnRoleMissingFailsBeforeReserve(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	f := &fakeForker{}
	if _, _, err := Spawn("window", "dev", "", "", "ghost", f); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	if f.called {
		t.Fatal("forker must not be called when the role is missing")
	}
	if dirExists(filepath.Join(CBUSDir(), "dev")) {
		t.Fatal("no alias may be reserved when the role is missing")
	}
}

// TestSpawnRoleNamesTheSpawnerAsCoordinator: a spawner registered on the channel
// coordinates the child it launches with a role.
func TestSpawnRoleNamesTheSpawnerAsCoordinator(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
	t.Chdir(t.TempDir())
	plantPeer(t, "dev", "lead", "sid-spawner")
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	f := &fakeForker{}
	if _, _, err := Spawn("window", "dev", "", "", "documenter", f); err != nil {
		t.Fatal(err)
	}
	prompt := f.spec.Argv[len(f.spec.Argv)-1]
	if !strings.Contains(prompt, "Your coordinator is dev/lead, the seat that launched you.") {
		t.Fatalf("a joined spawner must be named as the child's coordinator:\n%s", prompt)
	}
	if !strings.Contains(prompt, delegationOutOfEffort) || !strings.Contains(prompt, delegationReserved) {
		t.Fatalf("spawn --role must carry the out-of-effort and reserved lines:\n%s", prompt)
	}
	if strings.Index(prompt, "--- delegation ---") < strings.Index(prompt, "## Mission") {
		t.Fatalf("delegation must follow the role brief:\n%s", prompt)
	}
}

// TestSpawnRoleWithoutSpawnerOnChannelNamesNoCoordinator: spawn does not require
// joining, and a spawner that is not on the channel coordinates nobody there.
func TestSpawnRoleWithoutSpawnerOnChannelNamesNoCoordinator(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
	t.Chdir(t.TempDir())
	plantPeer(t, "other", "lead", "sid-spawner")
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	for _, addr := range []string{"dev", "dev@server"} {
		f := &fakeForker{}
		if _, _, err := Spawn("window", addr, "", "", "documenter", f); err != nil {
			t.Fatal(err)
		}
		prompt := f.spec.Argv[len(f.spec.Argv)-1]
		if !strings.Contains(prompt, delegationNone) || strings.Contains(prompt, "Your coordinator is") || strings.Contains(prompt, "nobody on the bus") {
			t.Fatalf("%s: a spawner off the channel must not be named coordinator:\n%s", addr, prompt)
		}
	}
}

// TestSpawnWithoutRoleCarriesNoDelegation: no role, no scope to delegate.
func TestSpawnWithoutRoleCarriesNoDelegation(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
	t.Chdir(t.TempDir())
	plantPeer(t, "dev", "lead", "sid-spawner")
	f := &fakeForker{}
	if _, _, err := Spawn("window", "dev", "", "worker", "", f); err != nil {
		t.Fatal(err)
	}
	if prompt := f.spec.Argv[len(f.spec.Argv)-1]; strings.Contains(prompt, "--- delegation ---") {
		t.Fatalf("a spawn without a role must not carry a delegation:\n%s", prompt)
	}
}

// TestSpawnRoleFixesTheEffortAndOrchestratorCoordinates: spawn carries no effort, so
// the first assignment fixes it; an orchestrator gets the coordinator side whether or
// not its spawner is on the channel.
func TestSpawnRoleFixesTheEffortAndOrchestratorCoordinates(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
	t.Chdir(t.TempDir())
	plantPeer(t, "dev", "lead", "sid-spawner")
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	writeRoleFile(t, os.Getenv("CBUS_DIR"), "orchestrator", "# orchestrator\nMODEL: sonnet\n\n## Mission\ncoordinate.\n")
	f := &fakeForker{}
	if _, _, err := Spawn("window", "dev", "", "", "documenter", f); err != nil {
		t.Fatal(err)
	}
	if prompt := f.spec.Argv[len(f.spec.Argv)-1]; !strings.Contains(prompt, delegationNoEffort) {
		t.Fatalf("spawn --role must fix the effort to the first assignment:\n%s", prompt)
	}
	for _, addr := range []string{"dev", "solo"} {
		f := &fakeForker{}
		if _, _, err := Spawn("window", addr, "", "", "orchestrator", f); err != nil {
			t.Fatal(err)
		}
		prompt := f.spec.Argv[len(f.spec.Argv)-1]
		if !strings.Contains(prompt, "You coordinate the peers you launch.") || !strings.Contains(prompt, delegationCoordNoEff) {
			t.Fatalf("%s: spawn --role orchestrator must get the coordinator side:\n%s", addr, prompt)
		}
		if strings.Contains(prompt, "Your coordinator is") || strings.Contains(prompt, "No coordinator is named") {
			t.Fatalf("%s: an orchestrator must not be given a peer or unknown clause:\n%s", addr, prompt)
		}
	}
}
