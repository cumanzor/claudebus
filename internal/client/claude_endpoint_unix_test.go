//go:build darwin || linux

package client

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func testClaudeEndpoint(t *testing.T) (claudeEndpoint, *net.UnixListener, *net.UnixConn) {
	t.Helper()
	// Unix socket paths are short on macOS; Go's test-name directory can exceed
	// sockaddr_un even when the actual listener name is small.
	dir, err := os.MkdirTemp("", "cbus-cc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "peer.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	start, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	e, err := captureClaudeEndpoint(path, os.Getpid(), start)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	server, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return e, l, conn
}

func TestClaudeEndpointConnectedPeer(t *testing.T) {
	e, _, conn := testClaudeEndpoint(t)
	if err := e.validateConnected(conn); err != nil {
		t.Fatal(err)
	}
	if err := validateClaudeSocketPeer(conn, os.Getpid()+1); err == nil {
		t.Fatal("accepted a different connected peer PID")
	}
	stale := e
	stale.StartToken += "-different-incarnation"
	if err := stale.validateConnected(conn); err == nil {
		t.Fatal("accepted stale process incarnation")
	}
	stale = e
	stale.PID = 0
	if err := validateClaudeEndpoint(stale); err == nil {
		t.Fatal("accepted missing owner")
	}
}

func TestClaudeEndpointReplacementFencesEvenConnectedPeer(t *testing.T) {
	e, l, conn := testClaudeEndpoint(t)
	// Keep the old inode allocated so replacement cannot coincidentally reuse it.
	old := e.Socket + ".old"
	if err := os.Rename(e.Socket, old); err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: e.Socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replacement.Close() })
	if err := os.Chmod(e.Socket, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.validateConnected(conn); err == nil {
		t.Fatal("accepted changed socket despite old connection still being live")
	}
}

func TestClaudeEndpointRejectsUnsafePath(t *testing.T) {
	e, _, _ := testClaudeEndpoint(t)
	t.Run("socket permissions", func(t *testing.T) {
		if err := os.Chmod(e.Socket, 0666); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(e.Socket, 0600)
		if _, err := captureClaudeEndpoint(e.Socket, e.PID, e.StartToken); err == nil {
			t.Fatal("accepted publicly writable socket")
		}
	})
	t.Run("directory permissions", func(t *testing.T) {
		dir := filepath.Dir(e.Socket)
		if err := os.Chmod(dir, 0777); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0700)
		if _, err := captureClaudeEndpoint(e.Socket, e.PID, e.StartToken); err == nil {
			t.Fatal("accepted publicly writable socket directory")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		link := e.Socket + ".link"
		if err := os.Symlink(e.Socket, link); err != nil {
			t.Fatal(err)
		}
		if _, err := captureClaudeEndpoint(link, e.PID, e.StartToken); err == nil {
			t.Fatal("accepted symlinked socket")
		}
	})
	t.Run("regular file", func(t *testing.T) {
		path := e.Socket + ".file"
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := captureClaudeEndpoint(path, e.PID, e.StartToken); err == nil {
			t.Fatal("accepted regular file")
		}
	})
}
