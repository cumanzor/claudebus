package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CodexConnectIdentity captures the exact current thread and local backend configuration.
// Native thread identity is required; neither a recent transcript nor a cwd match identifies
// the caller. This is not proof of CLI origin: the caller must inspect this exact thread's
// source and queue capability before registering it. Recorded cliVersion is likewise not
// proof of the version of the process currently serving the thread.
func CodexConnectIdentity() (CodexQueueConfig, string, error) {
	return CodexConnectIdentityWithOptions(CodexConnectOptions{})
}

// CodexConnectIdentityWithOptions binds to a witnessed runtime store or an
// explicit caller-supplied store. Profiles and per-launch overrides need not be
// replayed: the non-owning sidecar is pinned directly to the existing queue DB.
func CodexConnectIdentityWithOptions(opts CodexConnectOptions) (CodexQueueConfig, string, error) {
	return codexConnectIdentity(opts, currentCodexRuntimeBinding)
}

func codexConnectIdentity(opts CodexConnectOptions, witness func() (codexRuntimeBinding, error)) (CodexQueueConfig, string, error) {
	var cfg CodexQueueConfig
	sid := os.Getenv("CODEX_THREAD_ID")
	if sid == "" {
		return cfg, "", fmt.Errorf("CODEX_THREAD_ID is missing: run connect from the Codex CLI session you want to join; cbus will not guess a session from history")
	}
	if !uuidLike(sid) {
		return cfg, "", fmt.Errorf("CODEX_THREAD_ID must be an exact session UUID; cbus will not resolve session names or select a recent thread")
	}
	if SessionID() != sid {
		return cfg, "", fmt.Errorf("cbus session identity conflicts with CODEX_THREAD_ID: clear conflicting CBUS_SESSION_ID, CLAUDE_CODE_SESSION_ID or GROK_SESSION_ID values for cbus commands, or use the current thread's explicit session id")
	}
	if codexDesktopAncestor(os.Getppid(), procLookup()) {
		return cfg, "", fmt.Errorf("Codex desktop is outside this CLI-only connection pilot; run connect from the intended Codex CLI session")
	}
	if os.Getenv("CODEX_EXEC_SERVER_URL") != "" {
		return cfg, "", fmt.Errorf("CODEX_EXEC_SERVER_URL is set: this connect path requires the Codex backend on the same host and CODEX_HOME as the current shell; remote execution needs an explicit backend connection")
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return cfg, "", fmt.Errorf("resolve default Codex home: %w", err)
		}
		home = filepath.Join(userHome, ".codex")
	}
	var err error
	if cfg.Home, err = canonicalCodexDirectory(home, "CODEX_HOME"); err != nil {
		return cfg, "", err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return cfg, "", fmt.Errorf("resolve user home: %w", err)
	}
	if cfg.UserHome, err = canonicalCodexDirectory(userHome, "user home"); err != nil {
		return cfg, "", err
	}
	if cfg.Cwd, err = os.Getwd(); err != nil {
		return cfg, "", fmt.Errorf("resolve current workspace: %w", err)
	}
	var runtime codexRuntimeBinding
	if opts.SQLiteHome != "" {
		if !filepath.IsAbs(opts.SQLiteHome) {
			return cfg, "", fmt.Errorf("--codex-sqlite-home must be an absolute path to the current CLI's existing queue store")
		}
		cfg.SQLiteHome, err = canonicalCodexDirectory(opts.SQLiteHome, "explicit Codex SQLite home")
		if err != nil {
			return cfg, "", err
		}
		cfg.BindingSource = "explicit-sqlite-home"
		// Explicit binding also serves detached/external callers. Their nearest
		// Codex ancestor may be an unrelated orchestrating session, so do not
		// borrow its executable or advertise its PID as this thread's owner.
	} else {
		var inspectErr error
		runtime, inspectErr = witness()
		if inspectErr != nil {
			return cfg, "", fmt.Errorf("cannot bind the current Codex queue store: %w; retry with normal command approval if inspection was denied, or supply --codex-sqlite-home with the exact existing store for a detached backend", inspectErr)
		}
		if runtime.SQLiteHome == "" {
			return cfg, "", fmt.Errorf("current Codex queue store is not visible in this process ancestry; use --codex-sqlite-home with the exact existing store for this CLI (including profile or sqlite_home overrides); cbus will not guess from CODEX_HOME")
		}
		cfg.SQLiteHome = runtime.SQLiteHome
		cfg.BindingSource = "runtime-open-queue"
	}
	if err := validateCodexQueueHome(cfg.SQLiteHome); err != nil {
		return cfg, "", err
	}
	binary := runtime.Binary
	if binary == "" {
		binary, err = exec.LookPath("codex")
		if err != nil {
			return cfg, "", fmt.Errorf("find codex on PATH: %w", err)
		}
	}
	if cfg.Binary, err = filepath.Abs(binary); err != nil {
		return cfg, "", fmt.Errorf("resolve codex executable: %w", err)
	}
	if cfg.Binary, err = filepath.EvalSymlinks(cfg.Binary); err != nil {
		return cfg, "", fmt.Errorf("resolve codex executable: %w", err)
	}
	if runtime.PID > 0 {
		cfg.RuntimeVersion = os.Getenv("CODEX_VERSION")
	}
	cfg.RuntimePID = runtime.PID
	cfg.RuntimeStartToken = runtime.StartToken
	return cfg, sid, nil
}

// codexDesktopAncestor rejects positive evidence of the macOS desktop frontend,
// including a CLI-born transcript reopened there. Absence is not proof of CLI origin:
// process inspection may be unavailable, and a detached backend can lose its frontend
// ancestry. The adapter must still check the exact thread's source before connecting.
func codexDesktopAncestor(start int, lookup func(int) (procRecord, bool)) bool {
	const desktopExecutable = "/Codex.app/Contents/MacOS/Codex"
	var previous procRecord
	for depth, pid := 0, start; depth < maxWalkDepth && pid > procWalkRoot; depth++ {
		rec, ok := lookup(pid)
		if !ok || (depth > 0 && !ancestryPlausible(previous, rec)) {
			return false
		}
		// Check the process identity, not a shell's arguments mentioning the app.
		// Resources/codex is a usable CLI executable and is deliberately not a match.
		if strings.HasSuffix(rec.Comm, desktopExecutable) || (rec.Comm == "Codex" && strings.Contains(rec.Argv, desktopExecutable)) {
			return true
		}
		previous, pid = rec, rec.PPid
	}
	return false
}
