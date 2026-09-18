package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CodexQueueConfig identifies the recipient's local Codex storage and executable.
// The sidecar reads this configuration but never loads, resumes, or owns a thread.
type CodexQueueConfig struct {
	Binary            string
	Home              string
	Cwd               string
	SQLiteHome        string
	UserHome          string
	BindingSource     string
	RuntimeVersion    string
	RuntimePID        int
	RuntimeStartToken string
}

type codexQueueThread struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	CliVersion string `json:"cliVersion"`
	Cwd        string `json:"cwd"`
	Path       string `json:"path"`
}

// A transport error leaves enqueue acceptance unknown. RPC errors also require
// classification: internal/unknown failures may follow insertion. Callers must
// reconcile unless a rejection is known to precede enqueue, never blindly retry.
type codexQueueTransportError struct {
	Method string
	Err    error
}

func (e *codexQueueTransportError) Error() string {
	return fmt.Sprintf("codex queue %s: acceptance unknown: %v", e.Method, e.Err)
}
func (e *codexQueueTransportError) Unwrap() error { return e.Err }

const codexQueueRequestTimeout = 15 * time.Second

type codexQueue struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr codexQueueLog

	timeout  time.Duration
	wMu      sync.Mutex
	mu       sync.Mutex
	nextID   int
	pending  map[int]chan rpcResult
	endErr   error
	closed   chan struct{}
	ended    sync.Once
	waited   chan struct{}
	close    sync.Once
	closeErr error
}

func newCodexQueue(cfg CodexQueueConfig) (*codexQueue, error) {
	return newCodexQueueContext(context.Background(), cfg)
}

func newCodexQueueContext(parent context.Context, cfg CodexQueueConfig) (*codexQueue, error) {
	binary := cfg.Binary
	if binary == "" {
		binary = "codex"
	}
	ctx, cancel := context.WithCancel(parent)
	args := codexQueueArgs(cfg)
	cmd := boundedCmd(ctx, binary, args...)
	cmd.Dir = cfg.Cwd
	cmd.Env = codexQueueConfigEnv(os.Environ(), cfg)
	return startCodexQueue(cmd, cancel, codexQueueRequestTimeout)
}

func codexQueueArgs(cfg CodexQueueConfig) []string {
	args := []string{"app-server", "--stdio"}
	if cfg.SQLiteHome != "" {
		// JSON's string escaping is a valid TOML basic string for filesystem paths.
		// Pin the store even if profile/config files change after registration.
		quoted, _ := json.Marshal(cfg.SQLiteHome)
		args = append(args, "-c", "sqlite_home="+string(quoted))
	}
	return args
}

func codexQueueEnv(inherited []string, home string) []string {
	return codexQueueConfigEnv(inherited, CodexQueueConfig{Home: home})
}

func codexQueueConfigEnv(inherited []string, cfg CodexQueueConfig) []string {
	home := cfg.Home
	env := make([]string, 0, len(inherited)+4)
	// Keep only process/runtime necessities. The queue sidecar does not make
	// model calls and must not inherit its daemon launcher's credentials, proxy,
	// remote executor, plugin, or harness identity settings.
	allowed := map[string]bool{
		"PATH": true, "TMPDIR": true, "TMP": true, "TEMP": true,
		"SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "PATHEXT": true,
		"LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true,
	}
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[strings.ToUpper(key)] {
			env = append(env, entry)
		}
	}
	if home != "" {
		env = append(env, "CODEX_HOME="+home)
	}
	if cfg.UserHome != "" {
		env = append(env, "HOME="+cfg.UserHome, "USERPROFILE="+cfg.UserHome)
	}
	if cfg.SQLiteHome != "" {
		env = append(env, "CODEX_SQLITE_HOME="+cfg.SQLiteHome)
	}
	// Pinned Codex 0.154 consumes this process-only setting before startup.
	// It prevents a queue-only sidecar from adopting persisted remote control;
	// the owning CLI's setting and persisted enrollment are untouched.
	env = append(env, "CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED=1")
	return env
}

// startCodexQueue is also used by subprocess protocol tests, without running Codex.
func startCodexQueue(cmd *exec.Cmd, cancel context.CancelFunc, timeout time.Duration) (*codexQueue, error) {
	q := &codexQueue{cmd: cmd, cancel: cancel, timeout: timeout,
		pending: make(map[int]chan rpcResult), closed: make(chan struct{}), waited: make(chan struct{})}
	cmd.Stderr = &q.stderr
	var err error
	if q.stdin, err = cmd.StdinPipe(); err != nil {
		cancel()
		return nil, err
	}
	// Own this pipe rather than StdoutPipe: cmd.Wait must not close our reader
	// before the read loop consumes a final acknowledgement followed by exit.
	stdout, output, err := os.Pipe()
	if err != nil {
		q.stdin.Close()
		cancel()
		return nil, err
	}
	q.stdout = stdout
	cmd.Stdout = output
	if err := cmd.Start(); err != nil {
		q.stdin.Close()
		q.stdout.Close()
		output.Close()
		cancel()
		return nil, fmt.Errorf("start codex queue sidecar: %w", err)
	}
	output.Close()
	go q.readLoop()
	go func() {
		// The read loop, rather than Wait, closes the RPC stream. It must
		// deliver a final response before reporting the following EOF.
		_ = cmd.Wait()
		close(q.waited)
	}()
	if _, err := q.call("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "cbus-codex-queue", "version": "1"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}); err != nil {
		q.Close()
		return nil, fmt.Errorf("initialize codex queue sidecar: %w", err)
	}
	if err := q.write(map[string]any{"method": "initialized", "params": map[string]any{}}, q.timeout); err != nil {
		q.Close()
		return nil, fmt.Errorf("acknowledge codex queue initialization: %w", err)
	}
	return q, nil
}

func (q *codexQueue) Close() error {
	q.close.Do(func() {
		q.fail(errors.New("sidecar closed"))
		q.stdin.Close()
		q.cancel() // boundedCmd kills the owned process group on Unix.
		q.stdout.Close()
		select {
		case <-q.waited:
		case <-time.After(2 * time.Second):
			q.closeErr = errors.New("codex queue sidecar did not exit after cancellation")
		}
	})
	return q.closeErr
}

func (q *codexQueue) fail(err error) {
	q.ended.Do(func() {
		q.mu.Lock()
		q.endErr = err
		q.mu.Unlock()
		close(q.closed)
		q.cancel()
	})
}

func (q *codexQueue) transportError(method string, err error) error {
	return &codexQueueTransportError{Method: method, Err: err}
}

func (q *codexQueue) write(v any, timeout time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		q.wMu.Lock()
		defer q.wMu.Unlock()
		select {
		case <-q.closed:
			q.mu.Lock()
			err := q.endErr
			q.mu.Unlock()
			done <- err
			return
		default:
		}
		_, err := q.stdin.Write(append(b, '\n'))
		done <- err
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-q.closed:
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.endErr
	case <-timer.C:
		q.Close() // also unblocks a pipe write stalled behind an unresponsive child.
		return errors.New("request write timed out")
	}
}

// call accepts only the non-owning operations this adapter needs. In particular,
// no recovery path may start/resume a recipient thread in this sidecar.
func (q *codexQueue) call(method string, params any) (json.RawMessage, error) {
	switch method {
	case "initialize", "thread/read", "thread/queue/list", "thread/queue/add", "thread/items/list":
	default:
		return nil, fmt.Errorf("codex queue method %q is not permitted", method)
	}
	q.mu.Lock()
	q.nextID++
	id := q.nextID
	ch := make(chan rpcResult, 1)
	q.pending[id] = ch
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.pending, id)
		q.mu.Unlock()
	}()
	deadline := time.Now().Add(q.timeout)
	if err := q.write(map[string]any{"id": id, "method": method, "params": params}, q.timeout); err != nil {
		return nil, q.transportError(method, err)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	var r rpcResult
	select {
	case r = <-ch:
	case <-q.closed:
		// EOF may follow a valid response in the same read batch.
		select {
		case r = <-ch:
		default:
			q.mu.Lock()
			err := q.endErr
			q.mu.Unlock()
			return nil, q.transportError(method, err)
		}
	case <-timer.C:
		q.Close()
		return nil, q.transportError(method, errors.New("response timed out"))
	}
	if r.err != nil {
		return nil, r.err
	}
	return r.result, nil
}

func (q *codexQueue) readLoop() {
	scan := bufio.NewScanner(q.stdout)
	scan.Buffer(make([]byte, 64*1024), maxFrameSize)
	for scan.Scan() {
		var msg struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(scan.Bytes(), &msg); err != nil {
			q.fail(fmt.Errorf("invalid sidecar JSON: %w", err))
			return
		}
		if msg.Method != "" {
			if msg.ID != nil {
				// An idle sidecar must not execute approval/tool requests on behalf
				// of other sessions. Stop instead of making an implicit decision.
				q.fail(fmt.Errorf("unexpected sidecar request %q", msg.Method))
				return
			}
			continue
		}
		if msg.ID == nil || (msg.Result == nil && msg.Error == nil) {
			q.fail(errors.New("invalid sidecar response envelope"))
			return
		}
		q.mu.Lock()
		ch := q.pending[*msg.ID]
		delete(q.pending, *msg.ID)
		q.mu.Unlock()
		if ch != nil {
			ch <- rpcResult{result: msg.Result, err: msg.Error}
		}
	}
	err := scan.Err()
	if err == nil {
		err = io.EOF
	}
	q.fail(err)
}

func (q *codexQueue) inspect(threadID string) (codexQueueThread, error) {
	var result struct {
		Thread struct {
			ID         string          `json:"id"`
			Source     json.RawMessage `json:"source"`
			CliVersion string          `json:"cliVersion"`
			Cwd        string          `json:"cwd"`
			Path       string          `json:"path"`
		} `json:"thread"`
	}
	raw, err := q.call("thread/read", map[string]any{"threadId": threadID, "includeTurns": false})
	if err != nil {
		return codexQueueThread{}, err
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Thread.ID != threadID || threadID == "" {
		return codexQueueThread{}, errors.New("codex thread/read returned invalid or mismatched thread identity")
	}
	thread := codexQueueThread{ID: result.Thread.ID, CliVersion: result.Thread.CliVersion, Cwd: result.Thread.Cwd, Path: result.Thread.Path}
	if err := json.Unmarshal(result.Thread.Source, &thread.Source); err != nil {
		thread.Source = "unknown" // structured subagent sources cannot qualify as CLI roots.
	}
	// A metadata read succeeding does not establish that this runtime can queue.
	if _, _, err := q.queuePage(threadID, ""); err != nil {
		return codexQueueThread{}, fmt.Errorf("native Codex queue unavailable: %w", err)
	}
	return thread, nil
}

func (q *codexQueue) enqueue(threadID, clientID, text string) (string, error) {
	if threadID == "" || clientID == "" || strings.TrimSpace(text) == "" {
		return "", errors.New("codex enqueue requires a thread ID, client message ID, and message")
	}
	raw, err := q.call("thread/queue/add", map[string]any{
		"threadId": threadID, "clientUserMessageId": clientID,
		"input": []map[string]string{{"type": "text", "text": text}},
	})
	if err != nil {
		return "", err
	}
	var result struct {
		QueuedSubmission struct {
			ID       string `json:"id"`
			ClientID string `json:"clientUserMessageId"`
		} `json:"queuedSubmission"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.QueuedSubmission.ID == "" || result.QueuedSubmission.ClientID != clientID {
		return "", q.transportError("thread/queue/add", errors.New("invalid enqueue acknowledgement"))
	}
	return result.QueuedSubmission.ID, nil
}

type codexQueuedSubmission struct {
	ID       string `json:"id"`
	ClientID string `json:"clientUserMessageId"`
}

func (q *codexQueue) queuePage(threadID, cursor string) ([]codexQueuedSubmission, string, error) {
	params := map[string]any{"threadId": threadID, "limit": 100}
	if cursor != "" {
		params["cursor"] = cursor
	}
	raw, err := q.call("thread/queue/list", params)
	if err != nil {
		return nil, "", err
	}
	var result struct {
		Data       []codexQueuedSubmission `json:"data"`
		NextCursor string                  `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Data == nil {
		return nil, "", errors.New("invalid codex queue/list response")
	}
	return result.Data, result.NextCursor, nil
}

const (
	codexMessageNotFound = "not-found"
	codexMessageQueued   = "queued"
	codexMessageReceived = "received"
)

type codexMessageLookup struct {
	State   string `json:"state"`
	QueueID string `json:"queueId,omitempty"`
	ItemID  string `json:"itemId,omitempty"`
}

// lookupMessage distinguishes durable queue presence from a userMessage in the
// exact thread's history. History is checked first so a receipt wins over a
// duplicate queued submission. A receipt proves neither model completion/reply
// nor that the thread's runtime is still alive. Not-found never proves rejection:
// dequeue and transcript persistence are not atomic.
func (q *codexQueue) lookupMessage(threadID, clientID string) (codexMessageLookup, error) {
	if threadID == "" || clientID == "" {
		return codexMessageLookup{}, errors.New("message lookup requires exact IDs")
	}
	itemID, err := q.findHistoryItem(threadID, clientID)
	if err != nil {
		return codexMessageLookup{}, err
	}
	if itemID != "" {
		return codexMessageLookup{State: codexMessageReceived, ItemID: itemID}, nil
	}
	cursor := ""
	seen := map[string]bool{"": true}
	for page := 0; page < 100; page++ {
		items, next, err := q.queuePage(threadID, cursor)
		if err != nil {
			return codexMessageLookup{}, err
		}
		for _, item := range items {
			if item.ClientID == clientID {
				if item.ID == "" {
					return codexMessageLookup{}, errors.New("matching Codex queue entry has no ID")
				}
				return codexMessageLookup{State: codexMessageQueued, QueueID: item.ID}, nil
			}
		}
		if next == "" {
			return codexMessageLookup{State: codexMessageNotFound}, nil
		}
		if seen[next] {
			return codexMessageLookup{}, errors.New("Codex queue pagination cursor repeated")
		}
		seen[next] = true
		cursor = next
	}
	return codexMessageLookup{}, errors.New("Codex queue pagination limit reached")
}

// findMessage preserves the acceptance-only API for callers that need positive
// reconciliation evidence but do not distinguish queued from received.
func (q *codexQueue) findMessage(threadID, clientID string) (bool, error) {
	result, err := q.lookupMessage(threadID, clientID)
	return err == nil && (result.State == codexMessageQueued || result.State == codexMessageReceived), err
}

type codexQueueHistoryItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	ClientID string `json:"clientId"`
}

func (q *codexQueue) findHistoryItem(threadID, clientID string) (string, error) {
	cursor := ""
	seen := map[string]bool{"": true}
	for page := 0; page < 100; page++ {
		// Ascending order returns the first historical receipt if the native
		// client ID was mistakenly submitted more than once. No thread is loaded.
		params := map[string]any{"threadId": threadID, "limit": 100, "sortDirection": "asc"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := q.call("thread/items/list", params)
		if err != nil {
			return "", err
		}
		var result struct {
			Data []struct {
				Item codexQueueHistoryItem `json:"item"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || result.Data == nil {
			return "", errors.New("invalid Codex item history response")
		}
		for _, entry := range result.Data {
			if entry.Item.Type == "userMessage" && entry.Item.ClientID == clientID {
				if entry.Item.ID == "" {
					return "", errors.New("matching Codex history item has no ID")
				}
				return entry.Item.ID, nil
			}
		}
		if result.NextCursor == "" {
			return "", nil
		}
		if seen[result.NextCursor] {
			return "", errors.New("Codex history pagination cursor repeated")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
	return "", errors.New("Codex history pagination limit reached")
}

// Keep only a bounded diagnostic tail; never stream sidecar logs into the bus.
type codexQueueLog struct {
	mu  sync.Mutex
	buf []byte
}

func (l *codexQueueLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	const limit = 16 * 1024
	n := len(p)
	if n >= limit {
		l.buf = append(l.buf[:0], p[n-limit:]...)
		return n, nil
	}
	if excess := len(l.buf) + n - limit; excess > 0 {
		l.buf = append(l.buf[:0], l.buf[excess:]...)
	}
	l.buf = append(l.buf, p...)
	return n, nil
}

func (l *codexQueueLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}
