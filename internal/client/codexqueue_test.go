package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The Go test executable supplies a real child and pipe lifecycle without
// starting a Codex runtime, touching user storage, or issuing model requests.
func fakeCodexQueue(t *testing.T, mode string, timeout time.Duration) *codexQueue {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := boundedCmd(ctx, os.Args[0], "-test.run=^TestCodexQueueHelperProcess$", "--", "app-server", "--stdio")
	cmd.Env = append(os.Environ(), "CBUS_CODEX_QUEUE_TEST_MODE="+mode)
	q, err := startCodexQueue(cmd, cancel, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	})
	return q
}

func TestCodexQueueHelperProcess(t *testing.T) {
	mode := os.Getenv("CBUS_CODEX_QUEUE_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "stderr" {
		fmt.Fprint(os.Stderr, strings.Repeat("x", 65536)+"tail")
	}
	initialized := false
	acknowledged := false
	historyPages := 0
	queuePages := 0
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req struct {
			ID     int                        `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(30)
		}
		get := func(key string) string { var s string; _ = json.Unmarshal(req.Params[key], &s); return s }
		reply := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": req.ID, "result": v}) }
		reject := func(code int, text string) {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": code, "message": text}})
		}
		if req.Method != "initialize" && req.Method != "initialized" && (!initialized || !acknowledged) {
			os.Exit(31)
		}
		switch req.Method {
		case "initialize":
			var caps map[string]bool
			if json.Unmarshal(req.Params["capabilities"], &caps) != nil || !caps["experimentalApi"] {
				os.Exit(32)
			}
			initialized = true
			reply(map[string]any{"userAgent": "fake-codex/0.154.0"})
		case "initialized":
			acknowledged = true
		case "thread/read":
			var include bool
			_ = json.Unmarshal(req.Params["includeTurns"], &include)
			if include {
				// Receipt checks must page stored items, never hydrate full history.
				os.Exit(34)
			}
			id := get("threadId")
			if mode == "wrong-thread" {
				id = "someone-else"
			}
			thread := map[string]any{"id": id, "source": "cli", "cliVersion": "0.154.0", "cwd": "/work"}
			// Notifications may interleave with responses and must not stall RPC.
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "thread/status/changed", "params": map[string]any{}})
			reply(map[string]any{"thread": thread})
		case "thread/queue/list":
			queuePages++
			if mode == "unsupported" {
				reject(-32601, "Method not found")
				continue
			}
			if mode == "queue-rejected" || mode == "history-queue-rejected" {
				reject(-32000, "queue unavailable")
				continue
			}
			if mode == "queue-invalid" {
				reply(map[string]any{"data": nil})
				continue
			}
			items := []map[string]string{}
			next := ""
			if mode == "queued" || mode == "history-and-queued" || mode == "queue-missing-id" || (mode == "paged-queue" && get("cursor") == "page-2") {
				id := "queue-1"
				if mode == "queue-missing-id" {
					id = ""
				}
				items = append(items, map[string]string{"id": id, "clientUserMessageId": "message-1"})
			}
			if mode == "paged-queue" && get("cursor") == "" {
				next = "page-2"
			}
			if mode == "cycle" {
				next = "same"
			}
			if mode == "queue-cycle" {
				next = fmt.Sprintf("cycle-%d", queuePages%2)
			}
			if mode == "queue-limit" {
				next = fmt.Sprintf("page-%d", queuePages)
			}
			reply(map[string]any{"data": items, "nextCursor": next})
		case "thread/items/list":
			historyPages++
			if get("sortDirection") != "asc" {
				os.Exit(35)
			}
			if mode == "history-rejected" {
				reject(-32000, "history unavailable")
				continue
			}
			if mode == "history-transport" {
				os.Exit(0)
			}
			if mode == "history-invalid" {
				reply(map[string]any{"data": nil})
				continue
			}
			items := []map[string]any{}
			next := ""
			item := func(id, kind, clientID string) map[string]any {
				return map[string]any{"turnId": "turn-1", "item": map[string]string{"id": id, "type": kind, "clientId": clientID}}
			}
			if get("threadId") == "thread-1" {
				switch mode {
				case "history", "history-and-queued", "history-queue-rejected":
					items = append(items, item("item-first", "userMessage", "message-1"), item("item-duplicate", "userMessage", "message-1"))
				case "history-non-user":
					items = append(items, item("item-agent", "agentMessage", "message-1"), item("item-other", "userMessage", "message-other"))
				case "history-missing-id":
					items = append(items, item("", "userMessage", "message-1"))
				case "paged-history":
					if get("cursor") == "" {
						items = append(items, item("item-agent", "agentMessage", "message-1"))
						next = "next-items"
					} else if get("cursor") == "next-items" {
						items = append(items, item("item-first", "userMessage", "message-1"))
						next = "must-not-read"
					} else {
						reject(-32000, "looked past first receipt")
						continue
					}
				case "history-cycle":
					next = fmt.Sprintf("cycle-%d", historyPages%2)
				case "history-limit":
					next = fmt.Sprintf("page-%d", historyPages)
				}
			}
			reply(map[string]any{"data": items, "nextCursor": next})
		case "thread/queue/add":
			switch mode {
			case "rejected":
				reject(-32600, "queue full")
				continue
			case "lost-ack":
				os.Exit(0)
			case "timeout":
				continue
			case "invalid-json":
				fmt.Fprintln(os.Stdout, "invalid")
				continue
			}
			clientID := get("clientUserMessageId")
			if mode == "bad-ack" {
				clientID = "another-message"
			}
			reply(map[string]any{"queuedSubmission": map[string]string{"id": "queue-1", "clientUserMessageId": clientID}})
			if mode == "ack-exit" {
				os.Exit(0)
			}
		default:
			// Any thread/start, thread/resume or turn request fails the fake.
			os.Exit(33)
		}
	}
	os.Exit(0)
}

func TestCodexQueueInspectAndEnqueue(t *testing.T) {
	q := fakeCodexQueue(t, "normal", time.Second)
	thread, err := q.inspect("thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread-1" || thread.Source != "cli" || thread.CliVersion != "0.154.0" || thread.Cwd != "/work" {
		t.Fatalf("unexpected metadata: %+v", thread)
	}
	id, err := q.enqueue("thread-1", "message-1", "hello")
	if err != nil || id != "queue-1" {
		t.Fatalf("enqueue=(%q,%v)", id, err)
	}
	if _, err := q.call("thread/resume", map[string]string{"threadId": "thread-1"}); err == nil {
		t.Fatal("sidecar allowed a thread ownership operation")
	}
}

func TestCodexQueueEnvUsesRecipientHomeAndLocalExecutor(t *testing.T) {
	got := codexQueueEnv([]string{"PATH=/bin", "CODEX_HOME=/launcher", "CODEX_EXEC_SERVER_URL=https://launcher.invalid", "CODEX_THREAD_ID=launcher-thread", "CODEX_SESSION_ID=launcher-session", "CBUS_SESSION_ID=launcher-session", "CLAUDE_CODE_SESSION_ID=launcher-session", "GROK_SESSION_ID=launcher-session", "CBUS_CHANNEL=launcher-channel", "CBUS_ALIAS=launcher-alias", "OPENAI_API_KEY=launcher-placeholder"}, "/recipient")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "launcher") || strings.Contains(joined, "CODEX_EXEC_SERVER_URL") || !strings.Contains(joined, "CODEX_HOME=/recipient") || strings.Contains(joined, "OPENAI_API_KEY=") {
		t.Fatalf("incorrect environment: %v", got)
	}
}

func TestCodexQueueFinalAcknowledgementPrecedesEOF(t *testing.T) {
	q := fakeCodexQueue(t, "ack-exit", time.Second)
	id, err := q.enqueue("thread-1", "message-1", "hello")
	if err != nil || id != "queue-1" {
		t.Fatalf("final acknowledgement lost: (%q, %v)", id, err)
	}
}

func TestCodexQueueInspectRejectsWrongThreadAndUnsupportedQueue(t *testing.T) {
	for _, mode := range []string{"wrong-thread", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			q := fakeCodexQueue(t, mode, time.Second)
			_, err := q.inspect("thread-1")
			if err == nil {
				t.Fatal("inspection accepted incompatible backend")
			}
			if mode == "unsupported" {
				var rpc *rpcError
				if !errors.As(err, &rpc) || rpc.Code != -32601 {
					t.Fatalf("RPC rejection lost: %v", err)
				}
			}
		})
	}
}

func TestCodexQueueFindMessage(t *testing.T) {
	for _, mode := range []string{"queued", "paged-queue", "history", "paged-history", "normal"} {
		t.Run(mode, func(t *testing.T) {
			q := fakeCodexQueue(t, mode, time.Second)
			found, err := q.findMessage("thread-1", "message-1")
			if err != nil || found != (mode != "normal") {
				t.Fatalf("find=(%t,%v)", found, err)
			}
		})
	}
	q := fakeCodexQueue(t, "cycle", time.Second)
	if _, err := q.findMessage("thread-1", "message-1"); err == nil {
		t.Fatal("pagination loop was accepted")
	}
}

func TestCodexQueueLookupMessageDistinguishesReceipt(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		thread string
		want   codexMessageLookup
	}{
		{"normal", "thread-1", codexMessageLookup{State: codexMessageNotFound}},
		{"queued", "thread-1", codexMessageLookup{State: codexMessageQueued, QueueID: "queue-1"}},
		{"paged-queue", "thread-1", codexMessageLookup{State: codexMessageQueued, QueueID: "queue-1"}},
		{"history", "thread-1", codexMessageLookup{State: codexMessageReceived, ItemID: "item-first"}},
		{"paged-history", "thread-1", codexMessageLookup{State: codexMessageReceived, ItemID: "item-first"}},
		{"history-and-queued", "thread-1", codexMessageLookup{State: codexMessageReceived, ItemID: "item-first"}},
		{"history-queue-rejected", "thread-1", codexMessageLookup{State: codexMessageReceived, ItemID: "item-first"}},
		{"history-non-user", "thread-1", codexMessageLookup{State: codexMessageNotFound}},
		{"history", "another-thread", codexMessageLookup{State: codexMessageNotFound}},
	} {
		t.Run(tc.mode+"/"+tc.thread, func(t *testing.T) {
			q := fakeCodexQueue(t, tc.mode, time.Second)
			got, err := q.lookupMessage(tc.thread, "message-1")
			if err != nil || got != tc.want {
				t.Fatalf("lookup=(%+v,%v), want %+v", got, err, tc.want)
			}
		})
	}
}

func TestCodexQueueLookupDoesNotTurnErrorsIntoAbsence(t *testing.T) {
	for _, tc := range []struct {
		mode string
		text string
	}{
		{"history-cycle", "cursor repeated"},
		{"queue-cycle", "cursor repeated"},
		{"history-limit", "pagination limit"},
		{"queue-limit", "pagination limit"},
		{"history-invalid", "invalid Codex item history response"},
		{"queue-invalid", "invalid codex queue/list response"},
		{"history-missing-id", "history item has no ID"},
		{"queue-missing-id", "queue entry has no ID"},
		{"history-rejected", "history unavailable"},
		{"queue-rejected", "queue unavailable"},
		{"history-transport", "acceptance unknown"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			q := fakeCodexQueue(t, tc.mode, time.Second)
			got, err := q.lookupMessage("thread-1", "message-1")
			if err == nil || !strings.Contains(err.Error(), tc.text) || got != (codexMessageLookup{}) {
				t.Fatalf("lookup=(%+v,%v), want error containing %q and no state", got, err, tc.text)
			}
			if strings.HasSuffix(tc.mode, "-rejected") {
				var rpc *rpcError
				if !errors.As(err, &rpc) || rpc.Code != -32000 {
					t.Fatalf("RPC rejection lost: %v", err)
				}
			}
			if tc.mode == "history-transport" {
				var transport *codexQueueTransportError
				if !errors.As(err, &transport) || transport.Method != "thread/items/list" {
					t.Fatalf("transport error lost: %v", err)
				}
			}
		})
	}
}

func TestCodexQueueLookupRequiresExactIDs(t *testing.T) {
	q := fakeCodexQueue(t, "normal", time.Second)
	for _, ids := range [][2]string{{"", "message-1"}, {"thread-1", ""}} {
		if _, err := q.lookupMessage(ids[0], ids[1]); err == nil {
			t.Fatalf("accepted missing identity: %q", ids)
		}
	}
}

func TestCodexQueueEnqueueErrorsPreserveAmbiguity(t *testing.T) {
	for _, mode := range []string{"rejected", "lost-ack", "bad-ack", "timeout", "invalid-json"} {
		t.Run(mode, func(t *testing.T) {
			q := fakeCodexQueue(t, mode, time.Second)
			if mode == "timeout" {
				q.timeout = 50 * time.Millisecond
			}
			_, err := q.enqueue("thread-1", "message-1", "hello")
			if err == nil {
				t.Fatal("expected enqueue error")
			}
			var rpc *rpcError
			var transport *codexQueueTransportError
			if mode == "rejected" {
				if !errors.As(err, &rpc) || errors.As(err, &transport) {
					t.Fatalf("rejection misclassified: %v", err)
				}
			} else if !errors.As(err, &transport) || errors.As(err, &rpc) {
				t.Fatalf("ambiguous result misclassified: %v", err)
			}
			if mode == "timeout" {
				select {
				case <-q.waited:
				default:
					t.Fatal("timed-out child not reaped")
				}
			}
		})
	}
}

func TestCodexQueueCloseAndStderrAreBounded(t *testing.T) {
	q := fakeCodexQueue(t, "stderr", time.Second)
	if got := q.stderr.String(); len(got) != 16*1024 || !strings.HasSuffix(got, "tail") {
		t.Fatalf("stderr tail: len=%d suffix=%q", len(got), got[max(0, len(got)-4):])
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := q.enqueue("thread-1", "message-1", "hello")
	var transport *codexQueueTransportError
	if !errors.As(err, &transport) {
		t.Fatalf("closed transport: %v", err)
	}
}
