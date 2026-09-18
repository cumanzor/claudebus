package client

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexRuntimeStoreWinsOverInheritedEnvironment(t *testing.T) {
	setupConnectIdentity(t)
	t.Setenv("CODEX_SQLITE_HOME", "/wrong-inherited-store")
	cfg, _, err := testCodexConnectIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SQLiteHome != canonicalTestPath(t, os.Getenv("CBUS_TEST_CODEX_SQLITE_HOME")) || cfg.BindingSource != "runtime-open-queue" {
		t.Fatalf("binding = %+v", cfg)
	}
}

func TestCodexExplicitStoreFallbackDoesNotBorrowAncestorOwnership(t *testing.T) {
	binary := setupConnectIdentity(t)
	home := os.Getenv("CBUS_TEST_CODEX_SQLITE_HOME")
	cfg, _, err := codexConnectIdentity(CodexConnectOptions{SQLiteHome: home}, func() (codexRuntimeBinding, error) {
		t.Fatal("explicit binding inspected unrelated ancestry")
		return codexRuntimeBinding{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BindingSource != "explicit-sqlite-home" || cfg.RuntimePID != 0 || cfg.RuntimeStartToken != "" || cfg.Binary != canonicalTestPath(t, binary) {
		t.Fatalf("explicit binding=%+v", cfg)
	}
}

func TestCodexBindingNeverGuessesDefaultStore(t *testing.T) {
	setupConnectIdentity(t)
	for _, problem := range []error{nil, os.ErrPermission} {
		_, _, err := codexConnectIdentity(CodexConnectOptions{}, func() (codexRuntimeBinding, error) { return codexRuntimeBinding{}, problem })
		if err == nil || !strings.Contains(err.Error(), "--codex-sqlite-home") {
			t.Fatalf("missing witness: %v", err)
		}
		if strings.Contains(err.Error(), "restart") {
			t.Fatalf("must not demand restart: %v", err)
		}
	}
	for _, home := range []string{"relative", t.TempDir()} {
		_, _, err := codexConnectIdentity(CodexConnectOptions{SQLiteHome: home}, func() (codexRuntimeBinding, error) { return codexRuntimeBinding{}, nil })
		if err == nil {
			t.Fatalf("invalid explicit home %q accepted", home)
		}
	}
}

func TestCodexQueueHomeRequiresUnambiguousMainDatabase(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
		bad   bool
	}{
		{name: "no queue", paths: []string{filepath.Join(a, "state_5.sqlite"), filepath.Join(a, "queue_1.sqlite-wal")}},
		{name: "main repeated", paths: []string{filepath.Join(a, "queue_1.sqlite"), filepath.Join(a, "queue_1.sqlite")}, want: canonicalTestPath(t, a)},
		{name: "two stores", paths: []string{filepath.Join(a, "queue_1.sqlite"), filepath.Join(b, "queue_1.sqlite")}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codexQueueHome(tc.paths)
			if got != tc.want || (err != nil) != tc.bad {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}

func TestCodexRuntimeWitnessDoesNotFallThroughOuterSession(t *testing.T) {
	records := map[int]procRecord{30: {PPid: 20, Comm: "sh"}, 20: {PPid: 10, Comm: "codex"}, 10: {PPid: 1, Comm: "codex"}}
	var inspected []int
	_, err := codexRuntimeWitness(30, func(pid int) (procRecord, bool) { r, ok := records[pid]; return r, ok }, func(pid int) (codexRuntimeBinding, error) {
		inspected = append(inspected, pid)
		return codexRuntimeBinding{}, errors.New("detached backend")
	})
	if err == nil || !reflect.DeepEqual(inspected, []int{20}) {
		t.Fatalf("inspected=%v err=%v", inspected, err)
	}
}

func TestCodexQueueConfigEnvPinsCallerHomes(t *testing.T) {
	got := codexQueueConfigEnv([]string{"HOME=/daemon", "USERPROFILE=/daemon", "CODEX_HOME=/daemon/codex", "CODEX_SQLITE_HOME=/daemon/db", "CODEX_VERSION=old", "CODEX_PERMISSION_PROFILE=other", "CODEX_EXEC_SERVER_URL=remote", "PATH=/bin"}, CodexQueueConfig{Home: "/recipient/codex", UserHome: "/recipient", SQLiteHome: "/recipient/custom-db"})
	want := []string{"PATH=/bin", "CODEX_HOME=/recipient/codex", "HOME=/recipient", "USERPROFILE=/recipient", "CODEX_SQLITE_HOME=/recipient/custom-db", "CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env=%v want=%v", got, want)
	}
}

func TestCodexQueueArgsPinsStoreAsSingleConfigValue(t *testing.T) {
	path := "/a path/quote\"/back\\slash/\tstore"
	got := codexQueueArgs(CodexQueueConfig{SQLiteHome: path})
	want := []string{"app-server", "--stdio", "-c", `sqlite_home="/a path/quote\"/back\\slash/\tstore"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}

func TestCodexQueueHomeRejectsCorruptStoreBeforeSidecar(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "queue_1.sqlite")
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateCodexQueueHome(home); err == nil || !strings.Contains(err.Error(), "invalid SQLite header") {
		t.Fatalf("validation=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "not a SQLite database" {
		t.Fatal("validation changed store")
	}
}
