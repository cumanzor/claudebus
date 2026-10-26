package client

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// opencodePluginProtocol is the TUI plugin socket protocol this binary speaks
// (docs/architecture/protocol.md, section 15.3).
const opencodePluginProtocol = 1

// OpencodeConnectBinding is the caller's exact OpenCode session and the TUI
// plugin endpoint showing it, captured by `cbus connect` in the agent shell.
type OpencodeConnectBinding struct {
	SessionID string
	Cwd       string
	Endpoint  opencodeEndpoint
}

// opencodeEndpoint pins one TUI process and the socket inode its plugin bound.
type opencodeEndpoint struct {
	Socket     string
	PID        int
	StartToken string
	Dev, Ino   uint64
}

type OpencodeConnectionConfig struct {
	Binding OpencodeConnectBinding
}

type opencodeHello struct {
	OK        bool    `json:"ok"`
	Protocol  int     `json:"protocol"`
	Plugin    string  `json:"plugin"`
	PID       int     `json:"pid"`
	SessionID *string `json:"sessionID"`
	Code      string  `json:"code"`
	Error     string  `json:"error"`
}

func opencodeEndpointDir() string { return filepath.Join(CBUSDir(), ".opencode") }

var opencodeSocketName = regexp.MustCompile(`^([1-9][0-9]{0,9})\.sock$`)

// opencodeSocketPID reads the pid the plugin names its socket after; the peer
// credential check confirms it.
func opencodeSocketPID(path string) (int, bool) {
	m := opencodeSocketName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, false
	}
	pid, err := strconv.Atoi(m[1])
	return pid, err == nil && pid > 1
}

func (e opencodeEndpoint) wellFormed() bool {
	pid, ok := opencodeSocketPID(e.Socket)
	return filepath.IsAbs(e.Socket) && filepath.Clean(e.Socket) == e.Socket && ok && pid == e.PID &&
		e.StartToken != "" && e.Ino != 0
}

func validateOpencodeBinding(threadID string, b OpencodeConnectBinding) error {
	if !validOpencodeSessionID(threadID) || b.SessionID != threadID || !filepath.IsAbs(b.Cwd) || !b.Endpoint.wellFormed() {
		return errors.New("OpenCode connection requires the exact session, working directory and TUI endpoint")
	}
	return nil
}

func (h opencodeHello) shown() string {
	if h.SessionID == nil {
		return ""
	}
	return *h.SessionID
}

func (h opencodeHello) check(pid int) error {
	if !h.OK {
		return fmt.Errorf("OpenCode plugin refused hello: %s %s", h.Code, h.Error)
	}
	if h.Protocol != opencodePluginProtocol {
		return fmt.Errorf("OpenCode plugin speaks protocol %d, this cbus speaks %d; run cbus install-opencode and restart OpenCode", h.Protocol, opencodePluginProtocol)
	}
	if h.PID != pid {
		return errors.New("OpenCode plugin reported a different process than its socket peer")
	}
	return nil
}

func opencodeSessionList(ids []string) string {
	return strings.Join(ids, ", ")
}
