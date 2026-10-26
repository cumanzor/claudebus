//go:build !windows

package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	opencodeRequestTimeout = 10 * time.Second
	opencodeMaxLine        = 1 << 20
)

func captureOpencodeEndpoint(path string) (opencodeEndpoint, error) {
	pid, ok := opencodeSocketPID(path)
	if !ok {
		return opencodeEndpoint{}, fmt.Errorf("%s is not an OpenCode plugin socket", path)
	}
	start, err := procStartTime(pid)
	if err != nil {
		return opencodeEndpoint{}, fmt.Errorf("OpenCode process %d: %w", pid, err)
	}
	e := opencodeEndpoint{Socket: path, PID: pid, StartToken: start}
	info, err := privateSocket(path, "OpenCode")
	if err != nil {
		return e, err
	}
	e.Dev, e.Ino, _, _ = statIdentity(info)
	return e, validateOpencodeEndpoint(e)
}

func validateOpencodeEndpoint(e opencodeEndpoint) error {
	if !e.wellFormed() {
		return errors.New("OpenCode endpoint lacks an exact process/socket binding")
	}
	start, err := procStartTime(e.PID)
	if err != nil || start != e.StartToken || procZombie(e.PID) {
		return errOpencodeEndpointGone
	}
	info, err := privateSocket(e.Socket, "OpenCode")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errOpencodeEndpointGone
		}
		return err
	}
	dev, ino, _, ok := statIdentity(info)
	if !ok || dev != e.Dev || ino != e.Ino {
		return errors.New("OpenCode endpoint socket was replaced; reconnect from that session")
	}
	return nil
}

var errOpencodeEndpointGone = errors.New("OpenCode TUI exited or changed; reconnect from that session")

// errOpencodeNotDialed marks a failure before any request byte was written.
var errOpencodeNotDialed = errors.New("OpenCode plugin not reached")

// opencodeRequest sends one request line and reads one reply line. The socket
// peer must be the bound TUI process before the request goes out.
func opencodeRequest(e opencodeEndpoint, req, reply any) error {
	if err := validateOpencodeEndpoint(e); err != nil {
		return err
	}
	c, err := net.DialTimeout("unix", e.Socket, opencodeRequestTimeout)
	if err != nil {
		return fmt.Errorf("%w: %v", errOpencodeNotDialed, err)
	}
	defer c.Close()
	conn := c.(*net.UnixConn)
	if err := validateUnixSocketPeer(conn, e.PID); err != nil {
		return fmt.Errorf("%w: %v", errOpencodeNotDialed, err)
	}
	if err := conn.SetDeadline(time.Now().Add(opencodeRequestTimeout)); err != nil {
		return err
	}
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write OpenCode plugin request: %w", err)
	}
	r := bufio.NewReaderSize(io.LimitReader(conn, opencodeMaxLine+1), 4096)
	raw, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read OpenCode plugin reply: %w", err)
	}
	return json.Unmarshal(raw, reply)
}

func opencodeHelloAt(e opencodeEndpoint) (opencodeHello, error) {
	var h opencodeHello
	if err := opencodeRequest(e, map[string]string{"op": "hello"}, &h); err != nil {
		return h, err
	}
	return h, h.check(e.PID)
}

// discoverOpencodeEndpoint finds the one TUI showing sessionID. It never picks
// between candidates: none or several is an error naming what was found.
func discoverOpencodeEndpoint(sessionID string) (opencodeEndpoint, error) {
	dir := opencodeEndpointDir()
	paths, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err != nil {
		return opencodeEndpoint{}, err
	}
	sort.Strings(paths)
	var matches []opencodeEndpoint
	var problems []string
	for _, p := range paths {
		e, err := captureOpencodeEndpoint(p)
		if err != nil {
			if !errors.Is(err, errOpencodeEndpointGone) {
				problems = append(problems, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			}
			continue
		}
		h, err := opencodeHelloAt(e)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			continue
		}
		if h.shown() == sessionID {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		msg := fmt.Sprintf("no OpenCode TUI in %s shows session %s; open it in a TUI that has the cbus plugin (cbus install-opencode)", dir, sessionID)
		if errs, _ := filepath.Glob(filepath.Join(dir, "*.error")); len(errs) > 0 {
			for _, f := range errs {
				if b, err := os.ReadFile(f); err == nil {
					problems = append(problems, fmt.Sprintf("%s: %s", filepath.Base(f), strings.TrimSpace(string(b))))
				}
			}
		}
		if len(problems) > 0 {
			msg += "; " + strings.Join(problems, "; ")
		}
		return opencodeEndpoint{}, errors.New(msg)
	default:
		var pids []string
		for _, m := range matches {
			pids = append(pids, fmt.Sprint(m.PID))
		}
		return opencodeEndpoint{}, fmt.Errorf("%d OpenCode TUIs (pids %s) show session %s; close all but one before connecting, since two panes on one session can duplicate a delivery", len(matches), strings.Join(pids, ", "), sessionID)
	}
}

// opencodeWatch streams the plugin's watch events to onEvent until the stream
// ends, the context is cancelled, or the endpoint stops being the bound TUI.
func opencodeWatch(ctx context.Context, e opencodeEndpoint, onEvent func(opencodeWatchEvent)) error {
	if err := validateOpencodeEndpoint(e); err != nil {
		return err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", e.Socket)
	if err != nil {
		return fmt.Errorf("%w: %v", errOpencodeNotDialed, err)
	}
	defer c.Close()
	conn := c.(*net.UnixConn)
	if err := validateUnixSocketPeer(conn, e.PID); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if _, err := conn.Write([]byte(`{"op":"watch"}` + "\n")); err != nil {
		return err
	}
	r := bufio.NewReaderSize(conn, 4096)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if len(line) > opencodeMaxLine {
			return errors.New("OpenCode watch event exceeds the line limit")
		}
		var ev opencodeWatchEvent
		if json.Unmarshal(line, &ev) == nil {
			onEvent(ev)
		}
	}
}
