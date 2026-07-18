package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every test here drives the real CLI entry (run/runClose) and every peer it seeds
// has ownerPid null or a session-id that trips the self-refusal, so the matrix
// exercises resolution, refusal and reporting WITHOUT a signal ever being sent.

func closePeer(t *testing.T, root, ch, al, sid, ownerPid string) {
	t.Helper()
	seedPeer(t, root, ch, al, fmt.Sprintf(
		`{"alias":%q,"channel":%q,"sessionId":%q,"cwd":"/w","listenerPid":null,"ownerPid":%s,"host":"h","ts":"2026-07-18T00:00:00Z"}`,
		al, ch, sid, ownerPid))
}

// TestCloseUsageErrors: no targets is a usage error, and an unknown flag DIES
// rather than being taken for a target — close has no free-text body to protect,
// so a typo'd --forse must never reach a peer.
func TestCloseUsageErrors(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	for name, args := range map[string][]string{
		"no targets":     {},
		"only a flag":    {"--force"},
		"typo'd --force": {"ch/al", "--forse"},
	} {
		t.Run(name, func(t *testing.T) {
			var rc int
			out := captureStderr(t, func() { rc = runClose(args) })
			if rc == 0 {
				t.Fatalf("%s must fail, got rc=0 (stderr %q)", name, out)
			}
			if out == "" {
				t.Errorf("%s must explain itself on stderr", name)
			}
		})
	}
}

// TestCloseRefusesRemoteBeforeResolving is the load-bearing refusal: ClosePeer takes
// a LOCAL (channel, alias) and cannot express a host, so an @host target accepted
// this far would tear down the same-named local peer instead. Proven behaviorally —
// a local ch/al that WOULD report "already gone" is seeded, and the remote form must
// neither report it nor exit 0.
func TestCloseRefusesRemoteBeforeResolving(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	closePeer(t, root, "ch", "al", "other-session", "null")

	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"ch@host/al"}) })
	if rc == 0 {
		t.Fatalf("a remote target must fail, got rc=0: %q", out)
	}
	if strings.Contains(out, "already gone") {
		t.Fatalf("remote target reached the local peer — refusal came too late: %q", out)
	}
	if !strings.Contains(out, "local-only") {
		t.Errorf("refusal should say close is local-only, got %q", out)
	}
}

// TestCloseAlreadyGoneSucceeds pins B1: a peer with no live process is a SUCCESS,
// exit 0, so a scripted sweep can close the same roster twice. The exit code is the
// assertion that matters — "already gone" as a failure breaks teardown scripts.
func TestCloseAlreadyGoneSucceeds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	closePeer(t, root, "ch", "gone", "other-session", "null")

	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"ch/gone"}) })
	if rc != 0 {
		t.Fatalf("already-gone must exit 0, got rc=%d: %q", rc, out)
	}
	if !strings.Contains(out, "already gone") {
		t.Errorf("output should say already gone, got %q", out)
	}
}

// TestCloseUnknownPeerIsDistinctFromAlreadyGone: a peer that never existed is an
// error, and must not be reported as a successful teardown of nothing.
func TestCloseUnknownPeerIsDistinctFromAlreadyGone(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"ch/nobody"}) })
	if rc == 0 {
		t.Fatalf("unknown peer must fail, got rc=0: %q", out)
	}
	if strings.Contains(out, "already gone") {
		t.Errorf("an absent peer must not read as already-gone: %q", out)
	}
}

// TestCloseRefusesThisSession: closing yourself is not how you exit. Seeded with the
// running session's id, so the refusal is address identity (sessionId), not an alias
// string the caller could spell differently.
func TestCloseRefusesThisSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "my-session")
	closePeer(t, root, "ch", "me", "my-session", "null")

	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"ch/me"}) })
	if rc == 0 {
		t.Fatalf("closing this session must fail, got rc=0: %q", out)
	}
	if !strings.Contains(out, "THIS session") {
		t.Errorf("refusal should name the self case, got %q", out)
	}
}

// TestCloseResolvesBareAlias: a bare alias searches THIS session's channels, exactly
// as send does. The self-registration is what makes the lookup possible.
func TestCloseResolvesBareAlias(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "my-session")
	closePeer(t, root, "ch", "me", "my-session", "null") // this session's own registration
	closePeer(t, root, "ch", "peer", "other-session", "null")

	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"peer"}) })
	if rc != 0 {
		t.Fatalf("bare alias should resolve within own channels, rc=%d: %q", rc, out)
	}
	if !strings.Contains(out, "ch/peer") {
		t.Errorf("report should name the resolved address, got %q", out)
	}
}

// TestCloseBareAliasOutsideOwnChannels fails with the addressing hint rather than
// guessing a channel the caller is not on.
func TestCloseBareAliasOutsideOwnChannels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "my-session")
	closePeer(t, root, "elsewhere", "peer", "other-session", "null")

	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"peer"}) })
	if rc == 0 {
		t.Fatalf("unreachable bare alias must fail, got rc=0: %q", out)
	}
	if !strings.Contains(out, "<channel>/<alias>") {
		t.Errorf("failure should tell the user the addressed form, got %q", out)
	}
}

// TestCloseMultipleTargets: one line per target IN ORDER, a bad target does not
// abort the rest, and the exit code is nonzero if ANY target failed.
func TestCloseMultipleTargets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	closePeer(t, root, "ch", "a", "other-session", "null")
	closePeer(t, root, "ch", "c", "other-session", "null")

	var rc int
	out := captureStdout(t, func() { rc = runClose([]string{"ch/a", "ch/nobody", "ch/c"}) })
	if rc == 0 {
		t.Fatal("a failed target must make the run nonzero")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want one line per target, got %d:\n%s", len(lines), out)
	}
	for i, want := range []string{"ch/a", "ch/nobody", "ch/c"} {
		if !strings.HasPrefix(lines[i], want+":") {
			t.Errorf("line %d = %q, want it to report %s", i, lines[i], want)
		}
	}
	// the target AFTER the failure still ran — the failure did not abort the sweep
	if !strings.Contains(lines[2], "already gone") {
		t.Errorf("target after a failure must still be closed, got %q", lines[2])
	}
}

// TestCloseForceIsNotATarget: --force is a flag wherever it appears, never an alias.
// Both positions are pinned because the shared scanner stops at the first positional,
// so the TRAILING form — the one the usage line advertises and users actually type —
// is the one that silently regresses into a target named "--force".
func TestCloseForceIsNotATarget(t *testing.T) {
	for name, args := range map[string][]string{
		"trailing": {"ch/a", "--force"},
		"leading":  {"--force", "ch/a"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CBUS_DIR", root)
			closePeer(t, root, "ch", "a", "other-session", "null")

			var rc int
			out := captureStdout(t, func() { rc = runClose(args) })
			if rc != 0 {
				t.Fatalf("rc=%d: %q", rc, out)
			}
			if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 1 {
				t.Fatalf("--force must not become a target, got %d lines:\n%s", n, out)
			}
		})
	}
}

// TestCloseLeavesRegistrationsAlone is the blast-radius pin: the SessionEnd hook and
// prune own the registration files, so a close must not add, remove or rewrite any
// of them. Asserted as a byte-level fingerprint of the whole store, not a spot check.
func TestCloseLeavesRegistrationsAlone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	closePeer(t, root, "ch", "gone", "other-session", "null")
	closePeer(t, root, "ch", "bystander", "other-session", "null")

	before := storeFingerprint(t, root)
	captureStdout(t, func() { runClose([]string{"ch/gone"}) })
	if after := storeFingerprint(t, root); after != before {
		t.Errorf("close mutated the store:\n before %s\n after  %s", before, after)
	}
}

// storeFingerprint hashes every file path + content under root, so ANY registration
// change (content, deletion, new file) moves the digest.
func storeFingerprint(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		entries = append(entries, fmt.Sprintf("%s:%x", rel, sha256.Sum256(b)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n")
}

// TestUsageAdvertisesClose: a verb threaded through the CLI but absent from the help
// is dead surface — reachable only by guessing. Same rule the pane target is held to.
func TestUsageAdvertisesClose(t *testing.T) {
	out := captureStdout(t, func() { run([]string{"--help"}) })
	if !strings.Contains(out, "cbus close") {
		t.Errorf("help must advertise close:\n%s", out)
	}
	// the two non-obvious halves: it is local-only, and it does NOT prune
	if !strings.Contains(out, "--force") {
		t.Errorf("help must document --force:\n%s", out)
	}
	if !strings.Contains(out, "local only") && !strings.Contains(out, "local-only") {
		t.Errorf("help must say close is local-only:\n%s", out)
	}
}

// TestCloseVerbIsDispatched drives the top-level entry, not just runClose — a verb
// wired into usage but missing from the dispatch switch would still print "unknown
// command", which is the failure this pins.
func TestCloseVerbIsDispatched(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	out := captureStderr(t, func() { run([]string{"close"}) })
	if strings.Contains(out, "unknown command") {
		t.Fatalf("close must be dispatched, got %q", out)
	}
	if !strings.Contains(out, "usage: cbus close") {
		t.Errorf("bare close should print its usage, got %q", out)
	}
}
