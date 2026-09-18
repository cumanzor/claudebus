package client

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testCodexConnectIdentity() (CodexQueueConfig, string, error) {
	return codexConnectIdentity(CodexConnectOptions{}, func() (codexRuntimeBinding, error) {
		home, err := canonicalCodexDirectory(os.Getenv("CBUS_TEST_CODEX_SQLITE_HOME"), "test store")
		return codexRuntimeBinding{SQLiteHome: home}, err
	})
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const connectIdentityThread = "01a06968-3620-7a63-a5ba-1b25c394ccbd"

func setupConnectIdentity(t *testing.T) string {
	t.Helper()
	clearSessionEnv(t)
	t.Setenv("CODEX_THREAD_ID", connectIdentityThread)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "queue_1.sqlite"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CBUS_TEST_CODEX_SQLITE_HOME", store)
	t.Setenv("CODEX_EXEC_SERVER_URL", "")
	binDir := t.TempDir()
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(binDir, name)
	// Identity discovery must only resolve the binary; this file is never executed.
	if err := os.WriteFile(binary, []byte("not an executable program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	return binary
}

func TestCodexConnectIdentityCapturesExactThreadAndBackend(t *testing.T) {
	binary := setupConnectIdentity(t)
	cfg, sid, err := testCodexConnectIdentity()
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if sid != connectIdentityThread || cfg.Binary != canonicalTestPath(t, binary) || cfg.Home != canonicalTestPath(t, os.Getenv("CODEX_HOME")) || cfg.Cwd != wd {
		t.Fatalf("identity = %+v, %q; want exact thread, executable, home and cwd", cfg, sid)
	}
	if SessionID() != sid {
		t.Fatal("subsequent cbus commands would resolve a different session")
	}
}

func TestCodexConnectIdentityDefaultAndRelativeHome(t *testing.T) {
	setupConnectIdentity(t)
	relativeHome := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeHome, err = filepath.Rel(wd, relativeHome)
	if err != nil {
		t.Fatal(err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, home, want string }{
		{"default", "", filepath.Join(userHome, ".codex")},
		{"relative", relativeHome, relativeHome},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODEX_HOME", tc.home)
			want, err := canonicalCodexDirectory(tc.want, "test home")
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, err := testCodexConnectIdentity()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Home != want || !filepath.IsAbs(cfg.Home) {
				t.Fatalf("home = %q, want %q", cfg.Home, want)
			}
		})
	}
}

func TestCodexConnectIdentityRefusesUnboundOrConflictingIdentity(t *testing.T) {
	for _, tc := range []struct{ name, key, value, diagnostic string }{
		{"missing native id", "CODEX_THREAD_ID", "", "CODEX_THREAD_ID is missing"},
		{"session name", "CODEX_THREAD_ID", "advisor", "exact session UUID"},
		{"padded uuid", "CODEX_THREAD_ID", " " + connectIdentityThread, "exact session UUID"},
		{"inherited bus id", "CBUS_SESSION_ID", "parent-session", "identity conflicts"},
		{"inherited claude id", "CLAUDE_CODE_SESSION_ID", "parent-session", "identity conflicts"},
		{"inherited grok id", "GROK_SESSION_ID", "parent-session", "identity conflicts"},
		{"remote execution", "CODEX_EXEC_SERVER_URL", "http://remote.invalid", "explicit backend connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupConnectIdentity(t)
			t.Setenv(tc.key, tc.value)
			_, sid, err := testCodexConnectIdentity()
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) || sid != "" {
				t.Fatalf("identity = %q, %v; want rejection containing %q", sid, err, tc.diagnostic)
			}
			if strings.Contains(err.Error(), "restart") || strings.Contains(err.Error(), "upgrade") {
				t.Fatalf("identity failure must not masquerade as a runtime version issue: %v", err)
			}
		})
	}
}

func TestCodexConnectIdentityAllowsMatchingExplicitID(t *testing.T) {
	setupConnectIdentity(t)
	t.Setenv("CBUS_SESSION_ID", connectIdentityThread)
	if _, sid, err := testCodexConnectIdentity(); err != nil || sid != connectIdentityThread {
		t.Fatalf("matching explicit identity = %q, %v", sid, err)
	}
}

func TestCodexConnectIdentityMissingBinaryIsNotRuntimeAdvice(t *testing.T) {
	setupConnectIdentity(t)
	t.Setenv("PATH", t.TempDir())
	_, sid, err := testCodexConnectIdentity()
	if err == nil || sid != "" || !strings.Contains(err.Error(), "find codex on PATH") {
		t.Fatalf("missing binary = %q, %v", sid, err)
	}
	if strings.Contains(err.Error(), "restart") || strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("missing executable is not a runtime version failure: %v", err)
	}
}

func TestCodexDesktopAncestorRejectsFrontendNotBundledCLI(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  procRecord
		want bool
	}{
		{"desktop accounting name", procRecord{Comm: "Codex", Argv: "/Applications/Codex.app/Contents/MacOS/Codex"}, true},
		{"desktop full executable", procRecord{Comm: "/Applications/Codex.app/Contents/MacOS/Codex"}, true},
		{"desktop path with spaces", procRecord{Comm: "Codex", Argv: "/Users/A User/Apps/Codex.app/Contents/MacOS/Codex"}, true},
		{"bundled cli", procRecord{Comm: "codex", Argv: "/Applications/Codex.app/Contents/Resources/codex app-server"}, false},
		{"shell mentions desktop", procRecord{Comm: "zsh", Argv: "zsh -c open /Applications/Codex.app/Contents/MacOS/Codex"}, false},
		{"ordinary cli", procRecord{Comm: "codex", Argv: "/usr/local/bin/codex"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.rec.PPid = 1
			lookup := func(pid int) (procRecord, bool) {
				if pid == 20 {
					return procRecord{PPid: 10, Comm: "codex", Argv: "codex app-server"}, true
				}
				return tc.rec, pid == 10
			}
			if got := codexDesktopAncestor(20, lookup); got != tc.want {
				t.Fatalf("desktop ancestor = %v, want %v", got, tc.want)
			}
		})
	}
}
