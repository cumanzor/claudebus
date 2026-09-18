#!/usr/bin/env python3
"""Opt-in offline test of native Codex queue busy/interruption semantics.

Run with CODEX_TEST_BINARY=/path/to/codex python3 scripts/codex_queue_lifecycle_canary.py.
Requires permission to bind a localhost HTTP socket. All Codex state is created
under a fresh retained directory in CBUS_TEST_TMPDIR (default /tmp).

This tests scratch app-server threads and a fake local Responses provider, not a
real CLI TUI, cbus delivery, user sessions, paid inference, or an agent reply. The
fake provider holds requests until the test releases them. Its SSE payloads match
the response.created/response.completed test fixtures in Codex rust-v0.154.0.

Expected gates in that release: ext/queue/src/service.rs excludes interrupted
threads from wake/drain; core/src/session/mod.rs restores Interrupted from the
last status-bearing rollout event on resume. A normal user turn and completion
allow the pending input to drain again. Public thread status can still be idle.
"""

import argparse
import gzip
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time


class FakeProvider:
    def __init__(self, output=None):
        self.condition = threading.Condition()
        self.requests = []
        provider = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args):
                pass

            def do_POST(self):
                raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                if self.headers.get("Content-Encoding") == "gzip":
                    raw = gzip.decompress(raw)
                try:
                    body = json.loads(raw)
                except Exception as error:
                    self.send_error(400, f"expected uncompressed JSON: {error}")
                    return
                with provider.condition:
                    number = len(provider.requests) + 1
                    record = {"number": number, "path": self.path, "body": body,
                              "receivedAt": time.time(), "release": threading.Event()}
                    provider.requests.append(record)
                    provider.condition.notify_all()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Connection", "close")
                self.end_headers()
                response_id = f"offline-response-{number}"
                try:
                    self.event({"type": "response.created", "response": {"id": response_id}})
                    if not record["release"].wait(90):
                        record["holdTimedOut"] = True
                        return
                    items = output(number, body) if output else []
                    for index, item in enumerate(items):
                        self.event({"type": "response.output_item.added", "output_index": index, "item": item})
                        self.event({"type": "response.output_item.done", "output_index": index, "item": item})
                    self.event({"type": "response.completed", "response": {
                        "id": response_id, "output": items, "usage": {"input_tokens": 0, "output_tokens": 0,
                        "input_tokens_details": None, "output_tokens_details": None, "total_tokens": 0}}})
                    record["completedAt"] = time.time()
                except (BrokenPipeError, ConnectionResetError):
                    record["clientDisconnected"] = True
                finally:
                    self.close_connection = True

            def event(self, event):
                self.wfile.write((f"event: {event['type']}\ndata: {json.dumps(event)}\n\n").encode())
                self.wfile.flush()

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def wait(self, number, timeout=25):
        with self.condition:
            if not self.condition.wait_for(lambda: len(self.requests) >= number, timeout):
                raise TimeoutError(f"provider request {number} did not arrive")
            return self.requests[number - 1]

    def count(self):
        with self.condition:
            return len(self.requests)

    def release(self, number):
        self.wait(number)["release"].set()

    def close(self):
        with self.condition:
            for record in self.requests:
                record["release"].set()
        self.server.shutdown()
        self.server.server_close()


class RPC:
    def __init__(self, binary, env, root, label):
        self.label, self.root = label, root
        self.condition = threading.Condition()
        self.messages = []
        self.responses = {}
        self.next_id = 0
        self.closed = False
        self.stderr = (root / f"{label}.stderr").open("w")
        self.process = subprocess.Popen([binary, "app-server", "--stdio"], env=env,
                                        cwd=root / "work", stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=self.stderr,
                                        text=True, start_new_session=True)
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()
        try:
            self.call("initialize", {"clientInfo": {"name": "cbus-lifecycle-canary", "version": "1"},
                                     "capabilities": {"experimentalApi": True}})
            self.process.stdin.write('{"method":"initialized","params":{}}\n')
            self.process.stdin.flush()
        except Exception:
            self.close()
            raise

    def read(self):
        try:
            for line in self.process.stdout:
                message = json.loads(line)
                with self.condition:
                    self.messages.append(message)
                    if "id" in message:
                        self.responses[message["id"]] = message
                    self.condition.notify_all()
        finally:
            with self.condition:
                self.closed = True
                self.condition.notify_all()

    def call(self, method, params):
        self.next_id += 1
        request_id = self.next_id
        self.process.stdin.write(json.dumps({"id": request_id, "method": method, "params": params}) + "\n")
        self.process.stdin.flush()
        with self.condition:
            ready = self.condition.wait_for(lambda: request_id in self.responses or self.closed, 20)
            if not ready or request_id not in self.responses:
                raise TimeoutError(f"{self.label}: no response to {method}")
            message = self.responses.pop(request_id)
        if "error" in message:
            raise RuntimeError(f"{method}: {message['error']}")
        return message["result"]

    def position(self):
        with self.condition:
            return len(self.messages)

    def wait_event(self, method, predicate=lambda _: True, after=0, timeout=25):
        deadline = time.monotonic() + timeout
        with self.condition:
            while True:
                for message in self.messages[after:]:
                    if message.get("method") == method and predicate(message.get("params", {})):
                        return message["params"]
                remaining = deadline - time.monotonic()
                if remaining <= 0 or self.closed:
                    raise TimeoutError(f"{self.label}: missing event {method}")
                self.condition.wait(remaining)

    def close(self):
        if self.process.poll() is None:
            try:
                os.killpg(self.process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                self.process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=3)
        self.reader.join(timeout=2)
        self.stderr.close()
        (self.root / f"{self.label}.events.json").write_text(json.dumps(self.messages, indent=2) + "\n")


def run(args):
    binary = shutil.which(args.codex)
    if binary is None:
        raise ValueError(f"Codex executable not found: {args.codex}")
    root = Path(tempfile.mkdtemp(prefix="cbus-queue-lifecycle-", dir=args.temp_root)).resolve()
    home = root / "codex"
    home.mkdir()
    user_home = root / "user-home"
    user_home.mkdir()
    (root / "work").mkdir()
    result = {"root": str(root), "codexBinary": binary, "proofLayer": "scratch app-server; local fake provider",
              "checks": {}, "passed": False, "snapshots": {}}
    print(f"Lifecycle artifacts: {root}", flush=True)
    provider = FakeProvider()
    clients = []
    env = os.environ.copy()
    for key in ("OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "CODEX_EXEC_SERVER_URL",
                "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CBUS_SESSION_ID", "CLAUDE_CODE_SESSION_ID",
                "GROK_SESSION_ID", "CBUS_CHANNEL", "CBUS_ALIAS", "HTTP_PROXY", "HTTPS_PROXY",
                "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
        env.pop(key, None)
    env.update(CODEX_HOME=str(home), HOME=str(user_home), NO_PROXY="127.0.0.1,localhost")
    (home / "config.toml").write_text(
        'model = "cbus-lifecycle-probe"\nmodel_provider = "cbus_local_mock"\n'
        '[model_providers.cbus_local_mock]\nname = "Local fake Responses provider"\n'
        f'base_url = "http://127.0.0.1:{provider.server.server_port}/v1"\n'
        'wire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false\n'
        'request_max_retries = 0\nstream_max_retries = 0\n'
        '[analytics]\nenabled = false\n[features]\nunbounded_connection_retries = false\n'
        'enable_request_compression = false\nshell_snapshot = false\n'
    )

    def check(name, value):
        result["checks"][name] = bool(value)
        if not value:
            raise AssertionError(name)

    def client(label):
        c = RPC(binary, env, root, label)
        clients.append(c)
        return c

    def contains(request, marker):
        return marker in json.dumps(request["body"])

    try:
        owner = client("owner")
        thread = owner.call("thread/start", {"cwd": str(root / "work"), "model": "cbus-lifecycle-probe",
                           "modelProvider": "cbus_local_mock", "approvalPolicy": "never", "sandbox": "read-only"})["thread"]
        tid = thread["id"]
        result.update(threadId=tid, source=thread["source"], cliVersion=thread["cliVersion"])
        initial = owner.call("turn/start", {"threadId": tid, "input": [{"type": "text", "text": "HOLD_INITIAL"}]})["turn"]["id"]
        provider.wait(1)
        writer = client("writer")

        def enqueue(marker):
            return writer.call("thread/queue/add", {"threadId": tid, "clientUserMessageId": marker,
                              "input": [{"type": "text", "text": marker}]})["queuedSubmission"]["id"]

        def queued():
            return writer.call("thread/queue/list", {"threadId": tid})["data"]

        busy_id = enqueue("BUSY_QUEUED_INPUT")
        time.sleep(args.watcher_window)
        result["snapshots"]["whileBusy"] = {"providerRequests": provider.count(), "queue": queued()}
        check("busy_turn_retains_queue", provider.count() == 1 and [x["id"] for x in queued()] == [busy_id])
        after = owner.position()
        provider.release(1)
        first_completed = owner.wait_event("turn/completed", lambda p: p["turn"]["id"] == initial, after=after)
        check("initial_turn_completed", first_completed["turn"]["status"] == "completed")
        request = provider.wait(2)
        check("busy_queue_drains_after_completion", contains(request, "BUSY_QUEUED_INPUT"))
        provider.release(2)
        owner.wait_event("turn/completed", lambda p: p["turn"]["id"] != initial, after=after)
        check("busy_queue_removed_after_start", queued() == [])

        interrupted_turn = owner.call("turn/start", {"threadId": tid, "input": [{"type": "text", "text": "HOLD_FOR_INTERRUPT"}]})["turn"]["id"]
        provider.wait(3)
        paused_id = enqueue("INTERRUPTED_PENDING_INPUT")
        after = owner.position()
        owner.call("turn/interrupt", {"threadId": tid, "turnId": interrupted_turn})
        interrupted = owner.wait_event("turn/completed", lambda p: p["turn"]["id"] == interrupted_turn, after=after)
        check("explicit_interrupt_reported", interrupted["turn"]["status"] == "interrupted")
        provider.release(3)
        time.sleep(args.watcher_window)
        result["snapshots"]["afterInterrupt"] = {"providerRequests": provider.count(), "queue": queued()}
        check("interrupt_pauses_native_watcher", provider.count() == 3 and [x["id"] for x in queued()] == [paused_id])

        owner.call("thread/resume", {"threadId": tid})
        time.sleep(args.watcher_window)
        result["snapshots"]["afterLiveResume"] = {"providerRequests": provider.count(), "queue": queued()}
        check("live_rejoin_preserves_interrupted_pause", provider.count() == 3 and [x["id"] for x in queued()] == [paused_id])

        continuation = owner.call("turn/start", {"threadId": tid, "input": [{"type": "text", "text": "EXPLICIT_USER_CONTINUATION"}]})["turn"]["id"]
        check("continuation_starts_before_pending_queue", contains(provider.wait(4), "EXPLICIT_USER_CONTINUATION")
              and [x["id"] for x in queued()] == [paused_id])
        after = owner.position()
        provider.release(4)
        owner.wait_event("turn/completed", lambda p: p["turn"]["id"] == continuation, after=after)
        check("continuation_completion_drains_paused_queue", contains(provider.wait(5), "INTERRUPTED_PENDING_INPUT"))
        provider.release(5)
        owner.wait_event("turn/completed", lambda p: p["turn"]["id"] != continuation, after=after)
        check("continued_queue_empty", queued() == [])

        # Resuming persisted interrupted history must also preserve the pause.
        # Public thread status "idle" alone does not prove the queue can drain.
        cold_turn = owner.call("turn/start", {"threadId": tid, "input": [{"type": "text", "text": "HOLD_FOR_COLD_RESUME"}]})["turn"]["id"]
        provider.wait(6)
        cold_id = enqueue("COLD_RESUME_PENDING_INPUT")
        after = owner.position()
        owner.call("turn/interrupt", {"threadId": tid, "turnId": cold_turn})
        owner.wait_event("turn/completed", lambda p: p["turn"]["id"] == cold_turn, after=after)
        provider.release(6)
        check("cold_resume_queue_persisted", [x["id"] for x in queued()] == [cold_id])
        owner.close()
        resumed = client("cold-resumed-owner")
        resumed_thread = resumed.call("thread/resume", {"threadId": tid})["thread"]
        time.sleep(args.watcher_window)
        result["snapshots"]["afterColdResume"] = {
            "providerRequests": provider.count(), "queue": queued(), "publicStatus": resumed_thread["status"]}
        check("cold_resume_preserves_interrupted_pause", provider.count() == 6
              and [x["id"] for x in queued()] == [cold_id])
        cold_continuation = resumed.call("turn/start", {"threadId": tid,
            "input": [{"type": "text", "text": "EXPLICIT_COLD_CONTINUATION"}]})["turn"]["id"]
        check("cold_continuation_starts_before_queue", contains(provider.wait(7), "EXPLICIT_COLD_CONTINUATION")
              and [x["id"] for x in queued()] == [cold_id])
        after = resumed.position()
        provider.release(7)
        resumed.wait_event("turn/completed", lambda p: p["turn"]["id"] == cold_continuation, after=after)
        check("cold_continuation_completion_drains_queue", contains(provider.wait(8), "COLD_RESUME_PENDING_INPUT"))
        provider.release(8)
        resumed.wait_event("turn/completed", lambda p: p["turn"]["id"] != cold_continuation, after=after)
        check("cold_resumed_queue_empty", queued() == [])
        check("writer_never_loaded_thread", writer.call("thread/loaded/list", {})["data"] == [])
        check("exactly_expected_provider_requests", provider.count() == 8)
        result["passed"] = True
    except Exception as error:
        result["error"] = str(error)
    finally:
        for c in clients:
            c.close()
        provider.close()
        result["cleanup"] = {"allClientProcessesExited": all(c.process.poll() is not None for c in clients),
                             "providerThreadExited": not provider.thread.is_alive()}
        result["passed"] = result["passed"] and all(result["cleanup"].values())
        result["providerRequests"] = [{k: v for k, v in record.items() if k != "release"}
                                      for record in provider.requests]
        (root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"passed": result["passed"], "checks": result["checks"],
                      "error": result.get("error"), "result": str(root / "result.json")}), flush=True)
    return 0 if result["passed"] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default=os.getenv("CBUS_TEST_TMPDIR", "/tmp"))
    parser.add_argument("--watcher-window", type=float, default=11,
                        help="pause observation window; must exceed Codex's 10-second watcher interval")
    args = parser.parse_args()
    if args.watcher_window <= 10:
        parser.error("--watcher-window must exceed 10 seconds")
    return run(args)


if __name__ == "__main__":
    sys.exit(main())
