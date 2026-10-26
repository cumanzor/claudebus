package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var daemonHarnessConsts = map[string]bool{
	"daemonHarnessCodex": true, "daemonHarnessClaude": true, "daemonHarnessOpencode": true,
}

var harnessNames = map[string]bool{"claude": true, "codex": true, "opencode": true, "grok": true}

// harnessValued reports an expression that holds a harness name: daemonHarness(x),
// a .Harness field, a variable named harness, or strings.ToLower of one of those.
func harnessValued(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return harnessValued(e.X)
	case *ast.Ident:
		return e.Name == "harness"
	case *ast.SelectorExpr:
		return e.Sel.Name == "Harness"
	case *ast.CallExpr:
		if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "daemonHarness" {
			return true
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ToLower" && len(e.Args) == 1 {
			return harnessValued(e.Args[0])
		}
	}
	return false
}

// harnessName reports a harness constant or a string literal naming a harness.
func harnessName(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return daemonHarnessConsts[e.Name]
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return false
		}
		v, err := strconv.Unquote(e.Value)
		return err == nil && harnessNames[v]
	}
	return false
}

func harnessDispatchProblems(t *testing.T, dirs ...string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var problems []string
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BinaryExpr:
					if n.Op != token.EQL && n.Op != token.NEQ {
						return true
					}
					if (harnessValued(n.X) || harnessName(n.X)) && harnessName(n.Y) || harnessName(n.X) && harnessValued(n.Y) {
						problems = append(problems, fset.Position(n.Pos()).String()+": harness comparison; use a switch with an explicit default")
					}
				case *ast.SwitchStmt:
					harnessSwitch := n.Tag != nil && harnessValued(n.Tag)
					hasDefault := false
					for _, s := range n.Body.List {
						cc := s.(*ast.CaseClause)
						if cc.List == nil {
							hasDefault = true
						}
						for _, e := range cc.List {
							if harnessName(e) {
								harnessSwitch = true
							}
						}
					}
					if harnessSwitch && !hasDefault {
						problems = append(problems, fset.Position(n.Pos()).String()+": harness switch without a default")
					}
				}
				return true
			})
		}
	}
	return problems
}

// Two-way harness comparisons made every non-Claude connection a Codex one.
// Dispatch must be a switch whose default is decided explicitly.
func TestHarnessDispatchIsExplicit(t *testing.T) {
	for _, p := range harnessDispatchProblems(t, ".", filepath.Join("..", "..", "cmd", "cbus")) {
		t.Error(p)
	}
}

// The guard must catch each shape that once slipped past it.
func TestHarnessDispatchGuardCatchesEachShape(t *testing.T) {
	for name, body := range map[string]string{
		"call vs const":      `if daemonHarness(c.Harness) == daemonHarnessClaude {}`,
		"field vs literal":   `if c.Harness == "claude" {}`,
		"field vs const":     `if c.Harness != daemonHarnessCodex {}`,
		"local vs literal":   `if harness == "codex" {}`,
		"lowered vs literal": `if strings.ToLower(p.Harness) != "codex" {}`,
		"literal switch":     `switch c.Harness { case "claude": }`,
		"call switch":        `switch daemonHarness(c.Harness) { case daemonHarnessClaude: }`,
		"cases only":         `switch x { case "opencode": }`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := "package p\nimport \"strings\"\nvar _ = strings.ToLower\nfunc f(c, p struct{ Harness string }, harness, x string) {\n" + body + "\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if len(harnessDispatchProblems(t, dir)) == 0 {
				t.Errorf("guard missed %s", body)
			}
		})
	}
	for name, body := range map[string]string{
		"two harness values":  `if m.Harness != daemonHarness(c.Harness) {}`,
		"empty normalization": `if c.Harness == "" {}`,
		"switch with default": `switch c.Harness { case "claude": default: }`,
		"other literal":       `if c.Harness == "x-claude" {}`,
	} {
		t.Run("allows "+name, func(t *testing.T) {
			dir := t.TempDir()
			src := "package p\nfunc daemonHarness(string) string { return \"\" }\nfunc f(c, m struct{ Harness string }) {\n" + body + "\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if p := harnessDispatchProblems(t, dir); len(p) != 0 {
				t.Errorf("guard flagged an allowed shape: %v", p)
			}
		})
	}
}

func TestValidThreadIDPerHarness(t *testing.T) {
	const uuid = "d44e0ea7-1597-4870-85f4-fb7c7945afd9"
	const ses = "ses_ea0d7684affeaDXCT85BcZgp0m"
	for _, tc := range []struct {
		harness, id string
		want        bool
	}{
		{"", uuid, true}, {"codex", uuid, true}, {"claude", uuid, true},
		{"claude", ses, false}, {"codex", ses, false},
		{"opencode", ses, true}, {"opencode", uuid, false},
		{"opencode", "ses_short", false}, {"opencode", "ses_" + strings.Repeat("a", 65), false},
		{"opencode", "ses_ea0d7684affeaDXCT85BcZgp0-", false}, {"opencode", "SES_ea0d7684affeaDXCT85BcZgp0m", false},
		{"grok", uuid, false},
	} {
		if got := validThreadID(tc.harness, tc.id); got != tc.want {
			t.Errorf("validThreadID(%q, %q) = %v, want %v", tc.harness, tc.id, got, tc.want)
		}
	}
}

// A harness outside the known set must get refusals and no-ops, never Codex
// behavior. These paths are reachable once a later adapter is admitted.
func TestUnknownHarnessDefaultsAreNotCodex(t *testing.T) {
	c := &ConnectionState{Harness: "future-adapter", ThreadID: "ses_ea0d7684affeaDXCT85BcZgp0m",
		Config:   CodexQueueConfig{RuntimePID: 42, RuntimeStartToken: "t", Cwd: "/w", Home: "/h"},
		Consumer: &consumerObservation{PID: 42, StartToken: "t", State: "exited"}}
	if err := validateConnectionBinding(c); err == nil {
		t.Error("validateConnectionBinding accepted an unknown harness")
	}
	if got := connectionReadyState(c); got != "unsupported" {
		t.Errorf("connectionReadyState = %q", got)
	}
	if pid, start := pinnedConsumer(c); pid != 0 || start != "" {
		t.Errorf("pinnedConsumer = %d %q, want the Codex consumer not to be borrowed", pid, start)
	}
	if got := connectionCwd(c); got != "" {
		t.Errorf("connectionCwd = %q", got)
	}
	if sameConnectHome(c, ConnectRequest{Harness: "future-adapter", Config: CodexQueueConfig{Home: "/h"}}) {
		t.Error("sameConnectHome matched on the Codex home")
	}
	if _, err := prepareConnectBinding(ConnectRequest{Harness: "future-adapter"}, ""); err == nil {
		t.Error("prepareConnectBinding accepted an unknown harness")
	}
	if managedCodexConnection(c) || parkedClaude(c) || logsAttemptOutcomes(c) || claudeReceiptWaiting(c, time.Now()) {
		t.Error("a Codex or Claude predicate matched an unknown harness")
	}
	if provedNotSubmitted(c, &rpcError{Code: -32600}) || provedNotSubmitted(c, &claudeNotSubmittedError{}) {
		t.Error("an unknown harness released an attempt on another adapter's proof")
	}
	if claudeCleanWrite(c, &claudeAwaitingReceiptError{State: claudeSubmitted}) {
		t.Error("an unknown harness treated a Claude clean write as its own")
	}
	if got := consumerProbeInterval(c); got == consumerProbeInterval(&ConnectionState{}) {
		t.Errorf("consumerProbeInterval = %v, the Codex interval", got)
	}
	var d busDaemon
	if _, err := d.consumerProbe(c); err == nil {
		t.Error("consumerProbe accepted an unknown harness")
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("accept went past the harness gate toward a commit: %v", r)
			}
		}()
		if err := d.accept(c, nil); err == nil {
			t.Error("accept committed an unknown harness")
		}
	}()
}

func TestSpawnRefusesAHarnessItCannotLaunch(t *testing.T) {
	setupStore(t)
	for _, h := range []string{"opencode", "grok", "Claude"} {
		f := &fakeForker{}
		_, _, err := SpawnWithOptions("window", "demo", "", "", "", SpawnOptions{Harness: h}, f)
		if err == nil || !strings.Contains(err.Error(), "unsupported spawn harness") || f.called {
			t.Errorf("spawn harness %q: err=%v, forked=%v", h, err, f.called)
		}
	}
}

func TestFormationHarnessRefusal(t *testing.T) {
	backend := &FormationCodexBackend{Binary: "/codex"}
	for _, tc := range []struct {
		p    FormationPeer
		want string
	}{
		{FormationPeer{Alias: "a"}, ""},
		{FormationPeer{Alias: "a", Harness: "claude"}, ""},
		{FormationPeer{Alias: "a", Harness: "claude", CodexBackend: backend}, "harness=codex"},
		{FormationPeer{Alias: "a", Harness: "codex"}, "harness=codex"},
		{FormationPeer{Alias: "a", Harness: "opencode"}, "harness=opencode"},
	} {
		err := formationHarnessRefusal(&tc.p)
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%+v: %v, want %q", tc.p, err, tc.want)
		}
	}
}

func TestRecordsNonCodexHarness(t *testing.T) {
	for h, want := range map[string]bool{"": false, "codex": false, "Codex": false, "claude": true, "opencode": true} {
		if got := recordsNonCodexHarness(h); got != want {
			t.Errorf("recordsNonCodexHarness(%q) = %v, want %v", h, got, want)
		}
	}
}
