package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ClaudeConnectBinding contains observations, never the messaging token. Capture
// is inactive: it neither registers a bus peer nor sends to the native socket.
// No runtime version is claimed from a pathname or historical transcript.
type ClaudeConnectBinding struct {
	SessionID        string
	UserHome         string
	ConfigHome       string
	Cwd              string
	TranscriptPath   string
	TranscriptDev    uint64
	TranscriptIno    uint64
	TranscriptSize   int64
	TranscriptOffset int64 // End of the last complete line at capture time.
	Endpoint         claudeEndpoint
}

func claudeConnectIdentity(runtime func() (int, string, error)) (ClaudeConnectBinding, error) {
	var binding ClaudeConnectBinding
	sid := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if !uuidLike(sid) || SessionID() != sid {
		return binding, errors.New("exact CLAUDE_CODE_SESSION_ID required without conflicting cbus session identity")
	}
	claimed, err := strconv.Atoi(os.Getenv("CLAUDE_PID"))
	if err != nil || claimed <= 1 {
		return binding, errors.New("CLAUDE_PID must identify the current Claude session")
	}
	socket := os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if socket == "" {
		return binding, errors.New("CLAUDE_CODE_MESSAGING_SOCKET is missing; native messaging must be available in this session")
	}
	pid, start, err := runtime()
	if err != nil {
		return binding, fmt.Errorf("inspect current Claude ancestor: %w", err)
	}
	if pid != claimed || start == "" {
		return binding, errors.New("CLAUDE_PID does not match the current Claude ancestor")
	}
	endpoint, err := captureClaudeEndpoint(socket, pid, start)
	if err != nil {
		return binding, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return binding, err
	}
	// Reuse the existing directory canonicalizer; it contains no Codex lookup.
	if binding.UserHome, err = canonicalCodexDirectory(home, "user home"); err != nil {
		return ClaudeConnectBinding{}, err
	}
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		config = filepath.Join(binding.UserHome, ".claude")
	}
	if !filepath.IsAbs(config) {
		return ClaudeConnectBinding{}, errors.New("CLAUDE_CONFIG_DIR must be absolute; relative launch directories cannot be inferred")
	}
	if binding.ConfigHome, err = canonicalCodexDirectory(config, "Claude config home"); err != nil {
		return ClaudeConnectBinding{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ClaudeConnectBinding{}, err
	}
	if binding.Cwd, err = canonicalCodexDirectory(cwd, "current workspace"); err != nil {
		return ClaudeConnectBinding{}, err
	}
	binding.TranscriptPath, binding.TranscriptDev, binding.TranscriptIno, binding.TranscriptSize, binding.TranscriptOffset, err = exactClaudeTranscript(binding.ConfigHome, sid)
	if err != nil {
		return ClaudeConnectBinding{}, err
	}
	// Recheck the runtime/socket after filesystem discovery; never publish a
	// binding if its observed process incarnation or endpoint changed meanwhile.
	confirmedPID, confirmedStart, err := runtime()
	if err != nil || confirmedPID != pid || confirmedStart != start {
		return ClaudeConnectBinding{}, errors.New("Claude caller changed during binding")
	}
	if err := validateClaudeEndpoint(endpoint); err != nil {
		return ClaudeConnectBinding{}, err
	}
	binding.SessionID, binding.Endpoint = sid, endpoint
	return binding, nil
}

func claudeCallerPID(start int, lookup func(int) (procRecord, bool)) (int, error) {
	var previous procRecord
	for depth, pid := 0, start; pid > 1 && depth < maxWalkDepth; depth++ {
		rec, ok := lookup(pid)
		if !ok || depth > 0 && !ancestryPlausible(previous, rec) {
			break
		}
		identities := []string{rec.Comm}
		if args := strings.Fields(rec.Argv); len(args) > 0 {
			identities = append(identities, args[0])
		}
		for _, name := range identities {
			base := commBase(name)
			if isHarnessComm(base) || claudeNativeVersionPath(name) {
				if isHarnessComm(base) && normalizeHarness(base) != "claude" {
					return 0, errors.New("nearest coding harness is not Claude Code")
				}
				return pid, nil
			}
		}
		previous, pid = rec, rec.PPid
	}
	return 0, errors.New("current Claude Code ancestor could not be verified")
}

var claudeNativeVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`)

// Native installs keep the version path as both comm and argv[0]. Recognizing
// this install shape identifies an executable; it does not verify its version.
func claudeNativeVersionPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Base(filepath.Dir(path)) == "versions" &&
		filepath.Base(filepath.Dir(filepath.Dir(path))) == "claude" && claudeNativeVersion.MatchString(filepath.Base(path))
}

func exactClaudeTranscript(config, sid string) (string, uint64, uint64, int64, int64, error) {
	projects := filepath.Join(config, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return "", 0, 0, 0, 0, fmt.Errorf("read Claude project directories: %w", err)
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(projects, entry.Name(), sid+".jsonl")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return "", 0, 0, 0, 0, errors.New("exact Claude transcript could not be inspected as a regular file")
		}
		matches = append(matches, path)
	}
	if len(matches) != 1 {
		return "", 0, 0, 0, 0, fmt.Errorf("exact Claude session has %d transcript matches; refusing to guess from cwd or recency", len(matches))
	}
	path, err := filepath.EvalSymlinks(matches[0])
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	f, err := openClaudeTranscript(path)
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	defer f.Close()
	opened, err := f.Stat()
	dev, ino, size, ok := fileIdentityOf(f)
	if err != nil || !opened.Mode().IsRegular() || !ok {
		return "", 0, 0, 0, 0, errors.New("cannot identify opened Claude transcript")
	}
	if err := validateClaudeTranscript(f, sid); err != nil {
		return "", 0, 0, 0, 0, err
	}
	current, pathErr := os.Lstat(path)
	if pathErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return "", 0, 0, 0, 0, errors.New("Claude transcript changed during binding")
	}
	offset, err := completeClaudeTranscriptOffset(f, size)
	return path, dev, ino, size, offset, err
}

// Open the saved transcript epoch, not merely another file carrying its UUID.
func openBoundClaudeTranscript(binding ClaudeConnectBinding) (*os.File, error) {
	f, err := openClaudeTranscript(binding.TranscriptPath)
	if err != nil {
		return nil, err
	}
	info, statErr := f.Stat()
	dev, ino, _, ok := fileIdentityOf(f)
	if statErr != nil || !info.Mode().IsRegular() || info.Size() < binding.TranscriptSize || !ok || dev != binding.TranscriptDev || ino != binding.TranscriptIno {
		f.Close()
		return nil, errors.New("opened Claude transcript no longer matches its bound file identity")
	}
	if err := validateClaudeTranscript(f, binding.SessionID); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func validateClaudeTranscript(f *os.File, sid string) error {
	reader := bufio.NewReader(io.LimitReader(f, 8<<20))
	for records := 0; records < 256; records++ {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break // An unfinished physical tail is not an identity record.
		}
		var row struct {
			SessionID string `json:"sessionId"`
			Sidechain bool   `json:"isSidechain"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return errors.New("invalid Claude transcript identity record")
		}
		if row.SessionID != "" {
			if row.SessionID != sid || row.Sidechain {
				return errors.New("opened Claude transcript does not match the exact main session")
			}
			return nil
		}
	}
	return errors.New("opened Claude transcript has no complete exact-session identity record within the inspection limit")
}

// Use only the captured extent; a concurrently completed tail stays unread.
func completeClaudeTranscriptOffset(f *os.File, size int64) (int64, error) {
	var buf [4096]byte
	for end := size; end > 0; {
		start := max(int64(0), end-int64(len(buf)))
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil {
			return 0, fmt.Errorf("inspect Claude transcript boundary: %w", err)
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, errors.New("Claude transcript has no complete line")
}
