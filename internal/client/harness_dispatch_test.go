package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var daemonHarnessConsts = map[string]bool{
	"daemonHarnessCodex": true, "daemonHarnessClaude": true, "daemonHarnessOpencode": true,
}

func isDaemonHarnessCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "daemonHarness"
}

func mentionsHarnessConst(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && daemonHarnessConsts[id.Name]
}

// Two-way harness comparisons made every non-Claude connection a Codex one.
// Dispatch must be a switch whose default is decided explicitly.
func TestHarnessDispatchIsExplicit(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op != token.EQL && n.Op != token.NEQ {
					return true
				}
				_, xLit := n.X.(*ast.BasicLit)
				_, yLit := n.Y.(*ast.BasicLit)
				if mentionsHarnessConst(n.X) || mentionsHarnessConst(n.Y) ||
					(isDaemonHarnessCall(n.X) && yLit) || (isDaemonHarnessCall(n.Y) && xLit) {
					problems = append(problems, fset.Position(n.Pos()).String()+": harness comparison; use a switch with an explicit default")
				}
			case *ast.SwitchStmt:
				harnessSwitch := n.Tag != nil && isDaemonHarnessCall(n.Tag)
				hasDefault := false
				for _, s := range n.Body.List {
					cc := s.(*ast.CaseClause)
					if cc.List == nil {
						hasDefault = true
					}
					for _, e := range cc.List {
						if mentionsHarnessConst(e) {
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
	for _, p := range problems {
		t.Error(p)
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

func TestOpencodeHarnessRefusedUntilAdapterLands(t *testing.T) {
	err := validateDaemonHarness("opencode")
	if err == nil || !strings.Contains(err.Error(), "OpenCode adapter is not available") {
		t.Fatalf("validateDaemonHarness(opencode) = %v", err)
	}
	if err := validateConnectionAdapter(&ConnectionState{Harness: "opencode"}); err == nil {
		t.Fatal("connection adapter accepted opencode before its adapter exists")
	}
}

// A harness outside the known set must get refusals and no-ops, never Codex
// behavior. These paths are reachable once a later adapter is admitted.
func TestUnknownHarnessDefaultsAreNotCodex(t *testing.T) {
	c := &ConnectionState{Harness: "opencode", ThreadID: "ses_ea0d7684affeaDXCT85BcZgp0m",
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
	if sameConnectHome(c, ConnectRequest{Harness: "opencode", Config: CodexQueueConfig{Home: "/h"}}) {
		t.Error("sameConnectHome matched on the Codex home")
	}
	if _, err := prepareConnectBinding(ConnectRequest{Harness: "opencode"}, ""); err == nil {
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
