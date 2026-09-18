package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type codexWriterFD struct {
	PID          int
	Access, Path string
}

// A writable exact-thread rollout descriptor is positive loaded-session
// evidence. Queue DB descriptors identify storage, never which thread is loaded.
// Absence of a writer while its process lives remains unknown: Codex may close
// and reopen a rollout writer after an IO error.
func observeCodexConsumer(ctx context.Context, c *ConnectionState) (consumerProbe, error) {
	unknown := consumerProbe{State: "unknown"}
	rollout, err := openConsumerRollout(c.RolloutPath, c.ThreadID)
	if err != nil {
		return unknown, err
	}
	defer rollout.Close()
	identity, err := rollout.Stat()
	if err != nil {
		return unknown, err
	}
	path := rollout.Name()
	oldPID, oldStart := c.Config.RuntimePID, c.Config.RuntimeStartToken
	if c.Consumer != nil && c.Consumer.PID > 0 {
		oldPID, oldStart = c.Consumer.PID, c.Consumer.StartToken
	}
	oldExited := false
	if oldPID > 0 && oldStart != "" {
		oldExited, err = codexOwnerExited(oldPID, oldStart)
		if err != nil {
			return unknown, err
		}
	}
	fds, err := codexRolloutWriters(ctx, path)
	if err != nil {
		return unknown, err
	}
	seen := map[int]bool{}
	var found []consumerProbe
	for _, fd := range fds {
		if seen[fd.PID] || (fd.Access != "w" && fd.Access != "u") {
			continue
		}
		seen[fd.PID] = true
		if err := ctx.Err(); err != nil {
			return unknown, err
		}
		before, err := procStartTime(fd.PID)
		if err != nil {
			return unknown, fmt.Errorf("inspect rollout owner: %w", err)
		}
		comm, _, err := procParent(fd.PID)
		if err != nil {
			return unknown, err
		}
		if !strings.EqualFold(commBase(comm), "codex") {
			continue
		}
		argv, err := procArgs(fd.PID)
		if err != nil {
			return unknown, err
		}
		if !interactiveCodexProcess(argv) || codexDesktopAncestor(fd.PID, procLookup()) {
			continue
		}
		// Re-read this candidate's descriptors within its process-start fence;
		// the first scan may race process exit or a PID being reused.
		current, err := codexProcessFiles(ctx, fd.PID)
		if err != nil {
			return unknown, err
		}
		writer, queue := false, false
		for _, f := range current {
			if f.Access != "w" && f.Access != "u" {
				continue
			}
			if sameOpenFile(identity, f.Path) {
				writer = true
			}
			if sameExistingFile(f.Path, filepath.Join(c.Config.SQLiteHome, "queue_1.sqlite")) {
				queue = true
			}
		}
		after, err := procStartTime(fd.PID)
		if err != nil || before != after {
			return unknown, errors.New("rollout owner changed during inspection")
		}
		if writer && queue && !procZombie(fd.PID) {
			found = append(found, consumerProbe{State: "online", PID: fd.PID, StartToken: before, Detail: "exact rollout writer and queue store observed"})
		}
	}
	if !sameOpenFile(identity, path) {
		return unknown, errors.New("rollout identity changed during inspection")
	}
	if len(found) > 1 {
		return unknown, errors.New("multiple CLI writers hold this exact rollout; consumer ownership is ambiguous")
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if oldExited {
		return consumerProbe{State: "exited", Detail: "previous CLI process exited; no replacement writer observed"}, nil
	}
	return consumerProbe{State: "unknown", Detail: "no exact CLI rollout writer observed"}, nil
}

func codexOwnerExited(pid int, start string) (bool, error) {
	if procZombie(pid) {
		return true, nil
	}
	current, err := procStartTime(pid)
	if errors.Is(err, syscall.ESRCH) || os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("CLI ownership probe is inconclusive: %w", err)
	}
	return current != start, nil
}

func sameExistingFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && os.SameFile(left, right)
}

func sameOpenFile(identity os.FileInfo, path string) bool {
	current, err := os.Stat(path)
	return err == nil && os.SameFile(identity, current)
}

func interactiveCodexProcess(argv string) bool {
	args := strings.Fields(argv)
	if len(args) == 0 || !strings.EqualFold(commBase(args[0]), "codex") {
		return false
	}
	// Inspect the command position, not words in the user's prompt. Unknown
	// options remain inconclusive because their arity can move that position.
	for i := 1; i < len(args); i++ {
		arg, _, attached := strings.Cut(args[i], "=")
		switch arg {
		case "--":
			return true
		case "-c", "--config", "--enable", "--disable", "-i", "--image", "-m", "--model", "--local-provider", "-p", "--profile", "-s", "--sandbox", "-C", "--cd", "--add-dir", "-a", "--ask-for-approval":
			if !attached {
				i++
				if i >= len(args) {
					return false
				}
			}
		case "--strict-config", "--oss", "--approve-for-me", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--worktree", "--search", "--no-alt-screen":
			if attached {
				return false
			}
		case "agents", "exec", "e", "review", "login", "logout", "mcp", "plugin", "app-server", "remote-control", "app", "completion", "update", "doctor", "sandbox", "debug", "apply", "a", "queue", "archive", "delete", "migrate-rollouts", "unarchive", "cloud", "exec-server", "features", "help", "serve", "desktop":
			return false
		default:
			return !strings.HasPrefix(arg, "-")
		}
	}
	return true
}

func validateConsumerRollout(path, thread string) (string, error) {
	f, err := openConsumerRollout(path, thread)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return f.Name(), nil
}

// Keep this descriptor open while observing writers: a replaced path must not
// lend the validated UUID to a different rollout inode.
func openConsumerRollout(path, thread string) (*os.File, error) {
	if !filepath.IsAbs(path) || !uuidLike(thread) {
		return nil, errors.New("exact CLI rollout path is unavailable")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve CLI rollout: %w", err)
	}
	f, err := os.Open(canonical)
	if err != nil {
		return nil, err
	}
	line, err := readDaemonLine(bufio.NewReader(f))
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("read CLI rollout identity: %w", err)
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &meta); err != nil || meta.Type != "session_meta" || meta.Payload.ID != thread {
		f.Close()
		return nil, errors.New("rollout metadata does not match the exact registered thread")
	}
	return f, nil
}
