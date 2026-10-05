package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const pipeTestSession = "01a0b0c6-ffab-7fd0-9319-1ab0275adc21"

var (
	procCreateNamedPipe  = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateNamedPipeW")
	procConnectNamedPipe = syscall.NewLazyDLL("kernel32.dll").NewProc("ConnectNamedPipe")
)

// pipeServer serves one connection on a fresh pipe in this process and hands
// back everything the client wrote, or nothing when read is false.
func pipeServer(t *testing.T, read bool) (string, <-chan []byte) {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\LOCAL\cbus-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	n, _ := syscall.UTF16PtrFromString(name)
	h, _, e := procCreateNamedPipe.Call(uintptr(unsafe.Pointer(n)), 3, 0, 1, 4096, 4096, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		t.Fatalf("create pipe: %v", e)
	}
	server := os.NewFile(h, name)
	t.Cleanup(func() { server.Close() })
	got := make(chan []byte, 1)
	go func() {
		procConnectNamedPipe.Call(h, 0)
		if !read {
			return
		}
		data, _ := io.ReadAll(server)
		got <- data
	}()
	return name, got
}

func pipeTestContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestClaudePipeCarriesAuthThenMessage(t *testing.T) {
	name, got := pipeServer(t, true)
	target := claudeSocketTarget{Endpoint: name, SessionID: pipeTestSession, Validate: func(_ context.Context, conn claudeConn) error {
		return validateClaudeSocketPeer(conn, os.Getpid())
	}}
	result, err := submitClaudeSocket(pipeTestContext(t), target, "secret-token", "attempt-1", "hello windows")
	if err != nil || result.State != claudeSubmitted {
		t.Fatalf("submission over the pipe: %+v, %v", result, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(<-got), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want an auth line and a message line, got %q", lines)
	}
	var auth map[string]string
	var msg struct {
		SessionID string `json:"session_id"`
		Message   struct{ Content string }
	}
	if json.Unmarshal([]byte(lines[0]), &auth) != nil || auth["type"] != "auth" || auth["token"] != "secret-token" {
		t.Fatalf("first line must authenticate: %s", lines[0])
	}
	if json.Unmarshal([]byte(lines[1]), &msg) != nil || msg.SessionID != pipeTestSession || msg.Message.Content != "hello windows" {
		t.Fatalf("second line must carry the message: %s", lines[1])
	}
}

func TestClaudePipeServedByAnotherProcessGetsNoToken(t *testing.T) {
	name, got := pipeServer(t, true)
	target := claudeSocketTarget{Endpoint: name, SessionID: pipeTestSession, Validate: func(_ context.Context, conn claudeConn) error {
		return validateClaudeSocketPeer(conn, os.Getpid()+1)
	}}
	result, err := submitClaudeSocket(pipeTestContext(t), target, "secret-token", "attempt-1", "hello")
	if err == nil || result.State != claudeNotSubmitted {
		t.Fatalf("a pipe served by another process must be refused before writing: %+v, %v", result, err)
	}
	select {
	case data := <-got:
		if len(data) != 0 {
			t.Fatalf("refused pipe still received %q", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the refused client disconnect")
	}
}

func TestClaudePipeStuckServerCannotHangTheSend(t *testing.T) {
	name, _ := pipeServer(t, false)
	target := claudeSocketTarget{Endpoint: name, SessionID: pipeTestSession, Validate: func(context.Context, claudeConn) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, err := submitClaudeSocket(ctx, target, "secret-token", "attempt-1", strings.Repeat("x", 900<<10))
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("a server that never reads held the send for %v", took)
	}
	if err == nil || result.State == claudeSubmitted {
		t.Fatalf("an unread write must not report submission: %+v, %v", result, err)
	}
}

func TestClaudePipeNames(t *testing.T) {
	for name, want := range map[string]bool{
		`\\.\pipe\LOCAL\cc-msg-f35dce7d35219a7d25c165ed4d893cb0`: true,
		`\\.\pipe\cc-custom`:            true,
		`C:\Users\u\.claude\inbox.sock`: false,
		`\\.\pipe\LOCAL\..\other`:       false,
		`\\server\pipe\cc-msg-1`:        false,
		`\\.\pipe\`:                     false,
	} {
		if validClaudeEndpointName(name) != want {
			t.Errorf("validClaudeEndpointName(%q) = %v, want %v", name, !want, want)
		}
	}
}
