//go:build darwin || linux

package client

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const claudeCredentialTestID = "019c6e27-e55b-43d1-87d8-4e01f1f75043"

func claudeCredentialFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ref, err := storeClaudeCredential(root, claudeCredentialTestID, "fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	return root, ref, filepath.Join(root, claudeCredentialDir, ref)
}

func TestClaudeCredentialImmutablePrivateRoundTrip(t *testing.T) {
	root, ref, path := claudeCredentialFixture(t)
	token, err := readClaudeCredential(root, ref)
	if err != nil || token != "fixture-secret" || ref != claudeCredentialTestID+".token" {
		t.Fatal("credential round trip failed")
	}
	for path, mode := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private mode not established: %v", err)
		}
	}
	if replacement, err := storeClaudeCredential(root, claudeCredentialTestID, "replacement"); err == nil || replacement != "" {
		t.Fatal("existing reference was overwritten")
	}
	if token, err = readClaudeCredential(root, ref); err != nil || token != "fixture-secret" {
		t.Fatal("immutable credential changed")
	}
}

func TestClaudeCredentialRejectsInvalidReferencesAndTokens(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"", "../" + claudeCredentialTestID, "/" + claudeCredentialTestID, strings.ToUpper(claudeCredentialTestID), claudeCredentialTestID + ".token"} {
		if ref, err := storeClaudeCredential(root, id, "fixture-secret"); err == nil || ref != "" {
			t.Fatal("unsafe binding reference accepted")
		}
		if token, err := readClaudeCredential(root, id+".token"); err == nil || token != "" {
			t.Fatal("unsafe read reference accepted")
		}
	}
	for _, token := range []string{"", "fake\nsecret", "fake\rsecret", "fake\x00secret", "\xff", strings.Repeat("x", claudeCredentialMaxBytes+1)} {
		if ref, err := storeClaudeCredential(root, claudeCredentialTestID, token); err == nil || ref != "" {
			t.Fatal("invalid token accepted")
		}
	}
	if _, err := os.Lstat(filepath.Join(root, claudeCredentialDir)); !os.IsNotExist(err) {
		t.Fatal("invalid input mutated storage")
	}
}

func TestClaudeCredentialRejectsInsecureModesWithoutRepair(t *testing.T) {
	for _, part := range []string{"root", "directory", "file"} {
		t.Run(part, func(t *testing.T) {
			root, ref, path := claudeCredentialFixture(t)
			target, badMode := path, os.FileMode(0o640)
			if part == "root" {
				target, badMode = root, 0o750
			}
			if part == "directory" {
				target, badMode = filepath.Dir(path), 0o750
			}
			if err := os.Chmod(target, badMode); err != nil {
				t.Fatal(err)
			}
			if token, err := readClaudeCredential(root, ref); err == nil || token != "" {
				t.Fatal("insecure credential read")
			}
			if ref, err := storeClaudeCredential(root, claudeCredentialTestID, "replacement"); err == nil || ref != "" {
				t.Fatal("insecure credential stored")
			}
			if info, err := os.Stat(target); err != nil || info.Mode().Perm() != badMode {
				t.Fatal("silently repaired insecure permissions")
			}
		})
	}
}

func TestClaudeCredentialRejectsSymlinkRootsDirectoriesAndFiles(t *testing.T) {
	for _, part := range []string{"root", "directory", "file"} {
		t.Run(part, func(t *testing.T) {
			root, ref, path := claudeCredentialFixture(t)
			target := path
			if part == "root" {
				target = root
			}
			if part == "directory" {
				target = filepath.Dir(path)
			}
			real := target + "-real"
			if err := os.Rename(target, real); err != nil {
				t.Fatal(err)
			}
			defer os.Rename(real, target)
			if err := os.Symlink(filepath.Base(real), target); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(target)
			if token, err := readClaudeCredential(root, ref); err == nil || token != "" {
				t.Fatal("read followed a symlink")
			}
			if ref, err := storeClaudeCredential(root, claudeCredentialTestID, "replacement"); err == nil || ref != "" {
				t.Fatal("write followed a symlink")
			}
		})
	}
}

func TestClaudeCredentialRejectsBadStoredContentAndFileTypes(t *testing.T) {
	root, ref, path := claudeCredentialFixture(t)
	for _, data := range []string{"", "fake\nsecret", strings.Repeat("x", claudeCredentialMaxBytes+1)} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if token, err := readClaudeCredential(root, ref); err == nil || token != "" {
			t.Fatal("invalid stored content read")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := readClaudeCredential(root, ref); err == nil || token != "" {
		t.Fatal("FIFO accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+"-hardlink"); err != nil {
		t.Fatal(err)
	}
	if token, err := readClaudeCredential(root, ref); err == nil || token != "" {
		t.Fatal("hard-linked token accepted")
	}
}

type claudeCredentialForeignOwner struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (f claudeCredentialForeignOwner) Sys() any { return &f.stat }

func TestClaudeCredentialRejectsForeignOwnerAndRedactsErrors(t *testing.T) {
	root, ref, path := claudeCredentialFixture(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	foreign := claudeCredentialForeignOwner{FileInfo: info, stat: *info.Sys().(*syscall.Stat_t)}
	foreign.stat.Uid++ // Exercise foreign UID without requiring privileged chown.
	if privateClaudeCredentialInfo(foreign, false) {
		t.Fatal("foreign owner accepted")
	}
	secret := "fixture-credential-must-not-leak"
	for _, read := range []func() (string, error){
		func() (string, error) { return readClaudeCredential(filepath.Join(root, secret), ref) },
		func() (string, error) { return readClaudeCredential(root, secret) },
		func() (string, error) { return storeClaudeCredential(root, claudeCredentialTestID, secret+"\n") },
		func() (string, error) { return storeClaudeCredential(root, claudeCredentialTestID, secret) },
	} {
		value, err := read()
		if value != "" || err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("error exposed credential input")
		}
	}
}
