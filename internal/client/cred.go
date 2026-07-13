package client

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

// CredFields are the three per-host credential fields (bin/cbus:765).
var CredFields = []string{"token", "cf-id", "cf-secret"}

const credServicePrefix = "cbus-relay-" // Keychain service = credServicePrefix + host

// CredStore reads and writes per-host credentials. On macOS it shells to
// security(1) (the login Keychain, or an explicit keychain for tests); elsewhere
// it uses XDG 0600 files. Locations are frozen for no-reseed coexistence with the
// bash client (port-map §5 A6).
type CredStore struct{ backend credBackend }

type credBackend interface {
	get(host, field string) (string, error)
	put(host, field, value string) error
	where(host string) string
}

// NewCredStore returns the platform-default store: Keychain on darwin, files
// elsewhere.
func NewCredStore() *CredStore {
	if runtime.GOOS == "darwin" {
		return &CredStore{backend: keychainBackend{run: execRunner{}}}
	}
	return &CredStore{backend: fileBackend{}}
}

// NewFileCredStore forces the XDG file backend on any platform (for hermetic
// tests and the Linux path).
func NewFileCredStore() *CredStore { return &CredStore{backend: fileBackend{}} }

// NewKeychainCredStore forces the security(1) backend against an explicit
// keychain path — used ONLY by the opt-in integration test so it never touches
// the login keychain or the search list.
func NewKeychainCredStore(keychainPath string) *CredStore {
	return &CredStore{backend: keychainBackend{run: execRunner{}, keychain: keychainPath}}
}

// Get returns the stored value for host/field, or "" if absent (errors are
// swallowed as absent, matching the bash client's 2>/dev/null reads).
func (s *CredStore) Get(host, field string) (string, error) { return s.backend.get(host, field) }

// Put stores value for host/field.
func (s *CredStore) Put(host, field, value string) error { return s.backend.put(host, field, value) }

// Where describes the storage location for host (for the "stored ..." message).
func (s *CredStore) Where(host string) string { return s.backend.where(host) }

// runner executes a command with optional stdin; injected so unit tests can
// assert the exact argv/stdin without executing security(1).
type runner interface {
	run(name string, args []string, stdin string) (stdout string, err error)
}

type execRunner struct{}

func (execRunner) run(name string, args []string, stdin string) (string, error) {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	// leave Stderr nil -> child stderr goes to the null device (matches 2>/dev/null)
	err := cmd.Run()
	return out.String(), err
}

type keychainBackend struct {
	run      runner
	keychain string // "" = default login keychain; set only by the integration test
}

func (k keychainBackend) get(host, field string) (string, error) {
	args := []string{"find-generic-password", "-s", credServicePrefix + host, "-a", field, "-w"}
	if k.keychain != "" {
		args = append(args, k.keychain)
	}
	out, err := k.run.run("security", args, "")
	if err != nil {
		return "", nil // not found -> absent (bash swallows via 2>/dev/null)
	}
	return strings.TrimRight(out, "\n"), nil // -w prints a trailing newline; $() strips it
}

func (k keychainBackend) put(host, field, value string) error {
	// security -i reads the command from stdin so the secret never enters argv
	// (bin/cbus:175-177). Reproduce the exact stdin bytes, appending the explicit
	// keychain only in the integration path.
	cmd := fmt.Sprintf(`add-generic-password -U -s "cbus-relay-%s" -a "%s" -w "%s"`, host, field, value)
	if k.keychain != "" {
		cmd += fmt.Sprintf(` "%s"`, k.keychain)
	}
	_, err := k.run.run("security", []string{"-i"}, cmd+"\n")
	return err
}

func (k keychainBackend) where(host string) string {
	return "macOS Keychain (" + credServicePrefix + host + ")"
}

type fileBackend struct{}

// credStoreDir is ${XDG_CONFIG_HOME:-~/.config}/cbus/<host> (bin/cbus:165).
func credStoreDir(host string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "cbus", host)
}

func (fileBackend) get(host, field string) (string, error) {
	b, err := os.ReadFile(filepath.Join(credStoreDir(host), field))
	if err != nil {
		return "", nil // absent
	}
	return string(b), nil
}

func (fileBackend) put(host, field, value string) error {
	d := credStoreDir(host)
	if err := os.MkdirAll(d, 0o700); err != nil { // dir 0700 (bash: umask 077)
		return err
	}
	return os.WriteFile(filepath.Join(d, field), []byte(value), 0o600) // file 0600, no trailing newline
}

func (fileBackend) where(host string) string { return credStoreDir(host) + " (0600)" }

// StripWhitespace removes ALL whitespace from a credential value, matching
// `tr -d '[:space:]'` on `auth set` (bin/cbus:751).
func StripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// MaskTail returns the last n bytes of s (fewer if shorter) — the `…%s` mask on
// `auth status` (bin/cbus:767, bash `${v: -4}`).
func MaskTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
