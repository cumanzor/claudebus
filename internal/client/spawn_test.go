package client

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSpawnFreshArgvCCSProfile(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	t.Setenv("PATH", "/usr/bin:/bin")
	f := &fakeForker{}
	addr, child, err := Spawn("window", "dev", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "dev" {
		t.Fatalf("addr = %q", addr)
	}
	if child == "" {
		t.Fatal("local spawn must reserve a child alias")
	}
	if !f.called {
		t.Fatal("forker not called")
	}
	if f.spec.Argv[0] != "ccs" || f.spec.Argv[1] != "personal" {
		t.Fatalf("argv = %v", f.spec.Argv)
	}
	if slices.Contains(f.spec.Argv, "--resume") || slices.Contains(f.spec.Argv, "--fork-session") {
		t.Fatalf("fresh spawn must not resume/fork: %v", f.spec.Argv)
	}
	if got := f.spec.Argv[len(f.spec.Argv)-1]; got != SpawnPromptAliased("dev", child) {
		t.Fatalf("prompt positional = %q", got)
	}
	if f.spec.Env["PATH"] != "/usr/bin:/bin" || f.spec.Env["CLAUDE_CONFIG_DIR"] != "/Users/x/.ccs/instances/personal" {
		t.Fatalf("env replication = %v", f.spec.Env)
	}
	if f.spec.Dir == "" {
		t.Fatal("dir not replicated")
	}
}

func TestSpawnFreshArgvBareClaude(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := &fakeForker{}
	if _, _, err := Spawn("tab", "dev", "", "", f); err != nil {
		t.Fatal(err)
	}
	if f.spec.Argv[0] != "claude" {
		t.Fatalf("argv = %v", f.spec.Argv)
	}
}

func TestSpawnRemoteAddress(t *testing.T) {
	f := &fakeForker{}
	addr, child, err := Spawn("tab", "dev@nuc", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "dev@nuc" {
		t.Fatalf("addr = %q", addr)
	}
	if child != "" {
		t.Fatalf("remote spawn without --name must not fix an alias, got %q", child)
	}
	prompt := f.spec.Argv[len(f.spec.Argv)-1]
	for _, want := range []string{"ws arm spec", "dev@nuc", "1006", "cbus list @nuc"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("remote prompt missing %q:\n%s", want, prompt)
		}
	}
	// alias unknowable — the title falls back to the address
	if i := slices.Index(f.spec.Argv, "--name"); i < 0 || f.spec.Argv[i+1] != "dev@nuc" {
		t.Fatalf("remote default title should be the address: %v", f.spec.Argv)
	}
}

func TestSpawnPromptLocalContent(t *testing.T) {
	p := SpawnPrompt("dev")
	for _, want := range []string{"cbus join dev", "NEVER Bash", "cbus list dev"} {
		if !strings.Contains(p, want) {
			t.Fatalf("local prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "$addr") || strings.Contains(p, "$host") {
		t.Fatalf("unexpanded placeholder:\n%s", p)
	}
}

func TestSpawnPromptAliasedContent(t *testing.T) {
	p := SpawnPromptAliased("dev", "worker3")
	for _, want := range []string{"cbus join dev worker3", "cbus tail dev/worker3", "cbus:dev/worker3", "NEVER Bash"} {
		if !strings.Contains(p, want) {
			t.Fatalf("aliased local prompt missing %q:\n%s", want, p)
		}
	}
	r := SpawnPromptAliased("dev@nuc", "mbp2")
	for _, want := range []string{"cbus tail dev@nuc/mbp2", "cbus:dev@nuc/mbp2", "1006", "cbus list @nuc"} {
		if !strings.Contains(r, want) {
			t.Fatalf("aliased remote prompt missing %q:\n%s", want, r)
		}
	}
	for _, p := range []string{p, r} {
		if strings.Contains(p, "$addr") || strings.Contains(p, "$host") || strings.Contains(p, "$alias") {
			t.Fatalf("unexpanded placeholder:\n%s", p)
		}
	}
}

func TestSpawnRejectsAliasAndBadNames(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := &fakeForker{}
	for _, tc := range []struct{ target, addr, wantErr string }{
		{"window", "dev/main", "no alias"},
		{"window", "dev@nuc/mbp", "no alias"},
		{"pane", "dev", "target must be"},
		{"window", "a b", `bad channel "a b"`},
		{"window", "dev@", `bad host ""`},
		{"window", "@nuc", `bad channel ""`},
	} {
		if _, _, err := Spawn(tc.target, tc.addr, "", "", f); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("Spawn(%q,%q) err = %v, want %q", tc.target, tc.addr, err, tc.wantErr)
		}
	}
	if f.called {
		t.Fatal("forker must not be called on validation errors")
	}
}

func TestSpawnDefaultDerivesGlobalOutsideGit(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Chdir(t.TempDir())
	f := &fakeForker{}
	addr, _, err := Spawn("window", "", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "global" {
		t.Fatalf("default addr = %q, want global", addr)
	}
}

func TestSpawnModelFlag(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "/Users/x/.ccs/instances/personal")
	f := &fakeForker{}
	_, child, err := Spawn("window", "dev", "sonnet", "", f)
	if err != nil {
		t.Fatal(err)
	}
	argv := f.spec.Argv
	i := slices.Index(argv, "--model")
	if i < 0 || argv[i+1] != "sonnet" {
		t.Fatalf("argv = %v", argv)
	}
	if argv[len(argv)-1] != SpawnPromptAliased("dev", child) {
		t.Fatalf("prompt must stay the final positional: %v", argv)
	}
}

func TestBranchModelFlagAndBadModel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-model-test")
	f := &fakeForker{}
	if _, _, _, err := Branch("tab", "modelchan", "opus", "", f); err != nil {
		t.Fatal(err)
	}
	argv := f.spec.Argv
	i := slices.Index(argv, "--model")
	if i < 0 || argv[i+1] != "opus" {
		t.Fatalf("argv = %v", argv)
	}
	if slices.Index(argv, "--fork-session") > i {
		t.Fatalf("--model must follow --fork-session: %v", argv)
	}
	if _, _, _, err := Branch("tab", "modelchan", "bad model", "", f); err == nil || !strings.Contains(err.Error(), `bad model "bad model"`) {
		t.Fatalf("bad model err = %v", err)
	}
	if _, _, err := Spawn("tab", "dev", "-x", "", f); err == nil || !strings.Contains(err.Error(), `bad model "-x"`) {
		t.Fatalf("spawn bad model err = %v", err)
	}
}

// TestSpawnNameFixesAlias: --name reserves that alias locally (and pre-assigns it
// remotely); a name is an alias now, so free text and flag shapes are rejected.
func TestSpawnNameFixesAlias(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := &fakeForker{}
	_, child, err := Spawn("window", "dev", "", "runner", f)
	if err != nil {
		t.Fatal(err)
	}
	if child != "runner" {
		t.Fatalf("child = %q, want runner", child)
	}
	argv := f.spec.Argv
	if i := slices.Index(argv, "--name"); i < 0 || argv[i+1] != "runner" {
		t.Fatalf("argv = %v", argv)
	}
	if !fileExists(filepath.Join(CBUSDir(), "dev", "runner", "meta.json")) {
		t.Fatal("explicit --name must reserve the alias")
	}
	// remote with --name: pre-assigned, no local reservation
	f = &fakeForker{}
	_, child, err = Spawn("tab", "dev@nuc", "", "mbp2", f)
	if err != nil {
		t.Fatal(err)
	}
	if child != "mbp2" {
		t.Fatalf("remote child = %q", child)
	}
	if got := f.spec.Argv[len(f.spec.Argv)-1]; got != SpawnPromptAliased("dev@nuc", "mbp2") {
		t.Fatalf("remote prompt = %q", got)
	}
	if dirExists(filepath.Join(CBUSDir(), "dev@nuc")) {
		t.Fatal("remote spawn must not create local state")
	}
	for _, bad := range []string{"runner 2", "-x"} {
		if _, _, err := Spawn("window", "dev", "", bad, f); err == nil || !strings.Contains(err.Error(), "bad name") {
			t.Fatalf("bad name %q err = %v", bad, err)
		}
	}
}

// TestSpawnAutoReservesAlias: omitted --name auto-picks and reserves; two spawns get
// distinct aliases; the reservation meta is a reclaimable placeholder.
func TestSpawnAutoReservesAlias(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := &fakeForker{}
	_, first, err := Spawn("window", "dev", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := Spawn("window", "dev", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if first != "main" || second != "fork-1" {
		t.Fatalf("aliases = %q, %q — want main, fork-1", first, second)
	}
	if got := f.spec.Argv[len(f.spec.Argv)-1]; got != SpawnPromptAliased("dev", second) {
		t.Fatalf("prompt = %q", got)
	}
	m, ok := ReadPeerMeta(filepath.Join(CBUSDir(), "dev", "main", "meta.json"))
	if !ok || m.ListenerPid != 0 || m.OwnerPid != 0 {
		t.Fatalf("reservation must be a null-pid placeholder: %+v ok=%v", m, ok)
	}
}

func TestBranchNameFixesAliasAndDefault(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-name-test")
	f := &fakeForker{}
	_, parent, child, err := Branch("tab", "namechan", "", "tester2", f)
	if err != nil {
		t.Fatal(err)
	}
	if child != "tester2" {
		t.Fatalf("child = %q", child)
	}
	argv := f.spec.Argv
	if i := slices.Index(argv, "--name"); i < 0 || argv[i+1] != "tester2" {
		t.Fatalf("argv = %v", argv)
	}
	if !strings.Contains(argv[len(argv)-1], "cbus join namechan tester2") {
		t.Fatalf("prompt must carry the explicit join: %q", argv[len(argv)-1])
	}
	// default: auto-picked, distinct from the parent's alias
	_, _, child2, err := Branch("tab", "namechan", "", "", f)
	if err != nil {
		t.Fatal(err)
	}
	if child2 == "" || child2 == parent || child2 == child {
		t.Fatalf("auto child = %q (parent %q, taken %q)", child2, parent, child)
	}
	// a name is an alias: free text and flag shapes rejected
	for _, bad := range []string{"custom title", "-x"} {
		if _, _, _, err := Branch("tab", "namechan", "", bad, f); err == nil || !strings.Contains(err.Error(), "bad name") {
			t.Fatalf("bad name %q err = %v", bad, err)
		}
	}
	// reserving the parent's own alias is refused (reclaim would eat its registration)
	if _, _, _, err := Branch("tab", "namechan", "", parent, f); err == nil || !strings.Contains(err.Error(), "own alias") {
		t.Fatalf("own-alias err = %v", err)
	}
}

// TestReserveAliasReclaimAndUnreserve: a reservation is reclaimable by an explicit
// join (never listener-alive), and Unreserve drops it plus an empty channel dir.
func TestReserveAliasReclaimAndUnreserve(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-reserve-test")
	alias, err := ReserveAlias("resv", "kid")
	if err != nil {
		t.Fatal(err)
	}
	if alias != "kid" {
		t.Fatalf("alias = %q", alias)
	}
	// the "child" (this test session) joins explicitly and reclaims the placeholder
	chosen, already, err := Join("resv", "kid")
	if err != nil || already || chosen != "kid" {
		t.Fatalf("join over reservation = %q already=%v err=%v", chosen, already, err)
	}
	m, ok := ReadPeerMeta(filepath.Join(CBUSDir(), "resv", "kid", "meta.json"))
	if !ok || m.Cwd == "" {
		t.Fatalf("reclaimed meta unreadable: %+v", m)
	}
	if _, err := Leave("resv"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveAlias("resv", "kid"); err != nil {
		t.Fatal(err)
	}
	Unreserve("resv", "kid")
	if dirExists(filepath.Join(CBUSDir(), "resv")) {
		t.Fatal("Unreserve must drop the reservation and the empty channel dir")
	}
}
