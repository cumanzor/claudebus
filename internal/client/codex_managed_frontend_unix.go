//go:build darwin || linux

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// the exact thread's TUI endpoint belongs to the frontend, which may outlive
// or replace the parent that originally launched a shared backend.
func managedCodexFrontend(ctx context.Context, c *ConnectionState, backend int) (*codexFrontend, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	sock := filepath.Join(c.Config.Home, "app-server-control", "app-server-control.sock")
	if len(sock) >= 104 && runtime.GOOS == "linux" {
		dir, err := os.Open(filepath.Dir(sock))
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		sock = fmt.Sprintf("/proc/self/fd/%d/%s", dir.Fd(), filepath.Base(sock))
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("inspect managed Codex control socket: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := validateUnixSocketPeer(conn.(*net.UnixConn), backend); err != nil {
		return nil, err
	}
	rpc, err := newCodexConn(conn)
	if err != nil {
		return nil, err
	}
	defer rpc.close()
	if _, err := rpc.call("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "cbus-consumer-observer", "version": "1"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}); err != nil {
		return nil, err
	}
	if err := rpc.writeFrame(opText, []byte(`{"method":"initialized","params":{}}`)); err != nil {
		return nil, err
	}
	origin, err := managedCodexOrigin(rpc, c.ThreadID)
	if err != nil {
		return nil, err
	}
	frontend, err := inspectCodexFrontend(ctx, origin)
	if err != nil || frontend == nil {
		return frontend, err
	}
	// a cached endpoint must not transfer an old CLI's identity to a reused port.
	if c.Consumer != nil && c.Consumer.Frontend != nil {
		old := c.Consumer.Frontend
		if old.Origin == origin && (old.PID != frontend.PID || old.StartToken != frontend.StartToken) {
			return nil, errors.New("managed Codex TUI endpoint was reused by a different process")
		}
	}
	again, err := managedCodexOrigin(rpc, c.ThreadID)
	if err != nil {
		return nil, err
	}
	confirmed, err := inspectCodexFrontend(ctx, again)
	if err != nil {
		return nil, err
	}
	if again != origin || !sameCodexFrontend(frontend, confirmed) {
		return nil, errors.New("managed Codex TUI changed during inspection")
	}
	return frontend, nil
}

func managedCodexOrigin(rpc *codexConn, thread string) (string, error) {
	data, err := rpc.call("mcpServerStatus/list", map[string]any{"threadId": thread, "serverName": "codex_tui", "detail": "toolsAndAuthOnly"})
	if err != nil {
		return "", err
	}
	var result struct {
		Data []struct {
			Name   string `json:"name"`
			Origin string `json:"httpOrigin"`
		} `json:"data"`
		Next *string `json:"nextCursor"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if len(result.Data) != 1 || result.Next != nil || result.Data[0].Name != "codex_tui" {
		return "", errors.New("managed Codex did not report this exact thread's TUI endpoint")
	}
	origin := result.Data[0].Origin
	if _, err := codexFrontendPort(origin); err != nil {
		return "", err
	}
	return origin, nil
}

func codexFrontendPort(origin string) (int, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return 0, errors.New("managed Codex TUI endpoint must be an exact loopback HTTP origin")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || origin != "http://127.0.0.1:"+strconv.Itoa(port) {
		return 0, errors.New("managed Codex TUI endpoint has an invalid port")
	}
	return port, nil
}

func inspectCodexFrontend(ctx context.Context, origin string) (*codexFrontend, error) {
	port, err := codexFrontendPort(origin)
	if err != nil {
		return nil, err
	}
	pids, err := codexTCPListeners(ctx, port)
	if err != nil || len(pids) == 0 {
		return nil, err
	}
	if len(pids) != 1 {
		return nil, errors.New("managed Codex TUI listener ownership is ambiguous")
	}
	pid := pids[0]
	before, err := procStartTime(pid)
	if err != nil {
		return nil, err
	}
	comm, _, err := procParent(pid)
	if err != nil {
		return nil, err
	}
	argv, err := procArgs(pid)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(commBase(comm), "codex") || !interactiveCodexProcess(argv) || procZombie(pid) || codexDesktopAncestor(pid, procLookup()) {
		return nil, errors.New("managed Codex TUI endpoint is not owned by an interactive CLI")
	}
	after, err := procStartTime(pid)
	if err != nil || before != after {
		return nil, errors.New("managed Codex TUI process changed during inspection")
	}
	return &codexFrontend{PID: pid, StartToken: before, Origin: origin}, nil
}
