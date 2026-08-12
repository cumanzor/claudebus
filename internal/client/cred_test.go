package client

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// mockRunner records the last invocation and returns a canned response, so a unit
// test can assert the EXACT security(1) argv/stdin without executing it.
type mockRunner struct {
	name, stdin string
	args        []string
	out         string
	err         error
}

func (m *mockRunner) run(name string, args []string, stdin string) (string, error) {
	m.name, m.args, m.stdin = name, args, stdin
	return m.out, m.err
}

func TestKeychainBackendArgv(t *testing.T) {
	// put: the secret rides `security -i` stdin, never argv
	m := &mockRunner{}
	if err := (keychainBackend{run: m}).put("nuc", "token", "SECRET"); err != nil {
		t.Fatal(err)
	}
	if m.name != "security" || !slices.Equal(m.args, []string{"-i"}) {
		t.Fatalf("put argv = %q %q, want security [-i]", m.name, m.args)
	}
	wantStdin := `add-generic-password -U -s "cbus-relay-nuc" -a "token" -w "SECRET"` + "\n"
	if m.stdin != wantStdin {
		t.Fatalf("put stdin = %q, want %q", m.stdin, wantStdin)
	}

	// get: exact find-generic-password argv; the -w trailing newline is trimmed
	m2 := &mockRunner{out: "SECRET\n"}
	v, _ := (keychainBackend{run: m2}).get("nuc", "cf-id")
	wantArgs := []string{"find-generic-password", "-s", "cbus-relay-nuc", "-a", "cf-id", "-w"}
	if m2.name != "security" || !slices.Equal(m2.args, wantArgs) {
		t.Fatalf("get argv = %q, want %q", m2.args, wantArgs)
	}
	if v != "SECRET" {
		t.Fatalf("get value = %q, want SECRET", v)
	}

	// not found -> absent (bash swallows via 2>/dev/null)
	m3 := &mockRunner{err: errors.New("not found")}
	if v, _ := (keychainBackend{run: m3}).get("nuc", "token"); v != "" {
		t.Fatalf("get on error = %q, want empty", v)
	}
}

func TestKeychainBackendExplicitKeychain(t *testing.T) {
	m := &mockRunner{}
	_ = (keychainBackend{run: m, keychain: "/tmp/it.keychain"}).put("nuc", "token", "S")
	wantStdin := `add-generic-password -U -s "cbus-relay-nuc" -a "token" -w "S" "/tmp/it.keychain"` + "\n"
	if m.stdin != wantStdin {
		t.Fatalf("put stdin = %q, want %q", m.stdin, wantStdin)
	}
	m2 := &mockRunner{out: "S\n"}
	_, _ = (keychainBackend{run: m2, keychain: "/tmp/it.keychain"}).get("nuc", "token")
	if last := m2.args[len(m2.args)-1]; last != "/tmp/it.keychain" {
		t.Fatalf("get argv missing keychain path: %q", m2.args)
	}
}

func TestFileBackend(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	fb := fileBackend{}
	if err := fb.put("nuc", "token", "abc123"); err != nil {
		t.Fatal(err)
	}
	if v, _ := fb.get("nuc", "token"); v != "abc123" {
		t.Fatalf("round-trip get = %q", v)
	}
	if v, _ := fb.get("nuc", "cf-id"); v != "" {
		t.Fatalf("absent get = %q, want empty", v)
	}
	// the mode-bit half lives in TestFileBackendPermissions: it is the only unix-only
	// part, and a t.Skip needs its own function to be visible as a skip at all.
	if b, _ := os.ReadFile(filepath.Join(dir, "cbus", "nuc", "token")); string(b) != "abc123" {
		t.Errorf("file content = %q, want abc123 (no trailing newline)", b)
	}
}

// TestFileBackendPermissions is the unix-only half of the file backend's contract: the
// store dir is 0700 and each secret file 0600. Windows has no POSIX mode bits, so NTFS
// reports the classic 0777/0666 and these cannot hold there; what the contract should BE
// on windows is cbus-que.4 material rather than a mode comparison that reads as a defect.
//
// It is a separate function so the windows result is a visible SKIP. Guarding the asserts
// inline would have left the test reporting PASS on a platform where it checked nothing,
// which is the shape this formation keeps refusing.
func TestFileBackendPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on windows: the file-backend permission contract there is cbus-que.4")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := (fileBackend{}).put("nuc", "token", "abc123"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "cbus", "nuc")); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %o, want 700", fi.Mode().Perm())
	}
	ffi, _ := os.Stat(filepath.Join(dir, "cbus", "nuc", "token"))
	if ffi.Mode().Perm() != 0o600 {
		t.Errorf("file perm = %o, want 600", ffi.Mode().Perm())
	}
}

func TestStripWhitespaceAndMaskTail(t *testing.T) {
	if got := StripWhitespace("  ab\tc\nd \r\n"); got != "abcd" {
		t.Errorf("StripWhitespace = %q, want abcd", got)
	}
	if got := MaskTail("abcdef", 4); got != "cdef" {
		t.Errorf("MaskTail(abcdef,4) = %q, want cdef", got)
	}
	if got := MaskTail("ab", 4); got != "ab" {
		t.Errorf("MaskTail short = %q, want ab", got)
	}
	if got := MaskTail("", 4); got != "" {
		t.Errorf("MaskTail empty = %q", got)
	}
}

// TestKeychainIntegration exercises the REAL security(1) argv against a throwaway
// keychain, so the write-side add-generic-password argv is validated before a real
// user first runs it at P2 cutover. Opt-in (CBUS_KEYCHAIN_IT); macOS only; an
// explicit keychain path on every call — NEVER the search list; delete-keychain on
// cleanup.
func TestKeychainIntegration(t *testing.T) {
	if os.Getenv("CBUS_KEYCHAIN_IT") == "" {
		t.Skip("set CBUS_KEYCHAIN_IT=1 to run the real-security(1) temp-keychain test")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("security(1) is macOS-only")
	}
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("security(1) not found")
	}
	kc := filepath.Join(t.TempDir(), "cbus-it-"+randHex(t)+".keychain")
	const pass = "itpass"
	if out, err := exec.Command("security", "create-keychain", "-p", pass, kc).CombinedOutput(); err != nil {
		t.Fatalf("create-keychain: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("security", "delete-keychain", kc).Run() })
	if out, err := exec.Command("security", "unlock-keychain", "-p", pass, kc).CombinedOutput(); err != nil {
		t.Fatalf("unlock-keychain: %v\n%s", err, out)
	}

	store := NewKeychainCredStore(kc)
	if err := store.Put("ithost", "token", "s3cr3t-VALUE"); err != nil {
		t.Fatalf("Put via real security(1): %v", err)
	}
	if v, err := store.Get("ithost", "token"); err != nil || v != "s3cr3t-VALUE" {
		t.Fatalf("round-trip via real security(1) = %q (%v), want s3cr3t-VALUE", v, err)
	}
	if v, _ := store.Get("ithost", "cf-id"); v != "" {
		t.Fatalf("absent field via real security(1) = %q, want empty", v)
	}
}

func randHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
