#!/usr/bin/env python3
"""Offline capability probes for waking an ordinary idle Claude Code PTY.

--transport background checks Bash completion and its output-file read.
--transport socket checks the native per-session messaging socket. A permitted
Bash command exports its socket capability privately to this test process; the
token remains in memory and is never written into the retained evidence.
"""

import argparse
import fcntl
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--idle-seconds", type=float, default=12)
    parser.add_argument("--transport", choices=("background", "socket"), default="background")
    args = parser.parse_args()
    if args.idle_seconds < 10:
        parser.error("--idle-seconds must be at least 10")
    selected_binary = os.environ.get("CLAUDE_TEST_BINARY") or shutil.which("claude")
    if not selected_binary:
        parser.error("claude not found; set CLAUDE_TEST_BINARY")
    binary = str(Path(selected_binary).resolve())
    root = Path(tempfile.mkdtemp(prefix="cbus-cc-wake-", dir=os.environ.get("CBUS_TEST_TMPDIR", "/tmp"))).resolve()
    home, config, work = (root / name for name in ("home", "config", "project"))
    for path in (home, config, work, root / "tmp"):
        path.mkdir()
    session = str(uuid.uuid4())
    seed, marker = "CBUS_WAKE_SEED_" + uuid.uuid4().hex, "CBUS_WAKE_SIGNAL_" + uuid.uuid4().hex
    trigger = work / "signal"
    waiter = work / "wait_for_signal.py"
    waiter.write_text("import os, pathlib, time\np=pathlib.Path('signal')\npathlib.Path('waiter.pid').write_text(str(os.getpid()))\nwhile not p.exists(): time.sleep(.05)\nprint(p.read_text(), flush=True)\n")
    command = f"{sys.executable} {waiter}"
    requests, timeline = [], []
    state = {"mainRequests": 0}
    capability = {}

    class Provider(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_CONNECT(self):
            timeline.append({"event": "blocked_proxy", "path": self.path, "at": time.time()})
            self.send_error(403, "offline probe")

        def do_GET(self):
            self.send_error(404)

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            if self.path == "/private-capability":
                capability.update(body)
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"ok")
                return
            entry = {"at": time.time(), "path": self.path, "body": body}
            requests.append(entry)
            (root / "provider-requests.json").write_text(json.dumps(requests, indent=2))
            if "count_tokens" in self.path:
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"input_tokens":100}')
                return
            if not self.path.startswith("/v1/messages"):
                self.send_error(403, "offline probe")
                return
            messages = json.dumps(body.get("messages", []))
            main_request = seed in messages and any(t.get("name") == "Bash" for t in body.get("tools", []))
            entry["mainRequest"] = main_request
            if main_request:
                state["mainRequests"] += 1
            n = state["mainRequests"]
            if main_request and n == 1:
                content = [{"type": "tool_use", "id": "toolu_cbus_wake", "name": "Bash", "input": {"command": command, "description": "Prepare isolated canary capability", "run_in_background": args.transport == "background"}}]
                stop = "tool_use"
            elif args.transport == "background" and main_request and n == 3 and "<task-notification>" in messages:
                match = re.search(r"<output-file>([^<]+)</output-file>", messages)
                content = [{"type": "tool_use", "id": "toolu_cbus_read", "name": "Read", "input": {"file_path": match.group(1)}}]
                stop = "tool_use"
            else:
                answer = "CBUS_WAKE_RECEIVED" if marker in messages else "CBUS_WAKE_IDLE" if main_request else "Canary"
                content = [{"type": "text", "text": answer}]
                stop = "end_turn"
            message = {"id": "msg_" + uuid.uuid4().hex, "type": "message", "role": "assistant", "model": body.get("model", "claude-sonnet-4-6"), "content": content, "stop_reason": stop, "stop_sequence": None, "usage": {"input_tokens": 100, "output_tokens": 10}}
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream" if body.get("stream") else "application/json")
            self.end_headers()
            if not body.get("stream"):
                self.wfile.write(json.dumps(message).encode())
                return
            def event(kind, data):
                self.wfile.write(("event: " + kind + "\ndata: " + json.dumps({"type": kind, **data}) + "\n\n").encode())
                self.wfile.flush()
            event("message_start", {"message": {**message, "content": [], "stop_reason": None, "usage": {"input_tokens": 100, "output_tokens": 0}}})
            for i, block in enumerate(content):
                if block["type"] == "tool_use":
                    event("content_block_start", {"index": i, "content_block": {**block, "input": {}}})
                    event("content_block_delta", {"index": i, "delta": {"type": "input_json_delta", "partial_json": json.dumps(block["input"])}})
                else:
                    event("content_block_start", {"index": i, "content_block": {"type": "text", "text": ""}})
                    event("content_block_delta", {"index": i, "delta": {"type": "text_delta", "text": block["text"]}})
                event("content_block_stop", {"index": i})
            event("message_delta", {"delta": {"stop_reason": stop, "stop_sequence": None}, "usage": {"output_tokens": 10}})
            event("message_stop", {})
            entry["responseCompletedAt"] = time.time()
            (root / "provider-requests.json").write_text(json.dumps(requests, indent=2))

    server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    key = "sk-ant-offline-cbus-canary-not-real"
    conf = {"hasCompletedOnboarding": True, "lastOnboardingVersion": "2.1.277", "theme": "dark", "customApiKeyResponses": {"approved": [key[-20:]], "rejected": []}, "projects": {str(work): {"hasTrustDialogAccepted": True, "allowedTools": [], "hasCompletedProjectOnboarding": True}}}
    (config / ".claude.json").write_text(json.dumps(conf))
    (home / ".claude.json").write_text(json.dumps(conf))
    settings = {"permissions": {"allow": [f"Bash({command})"], "defaultMode": "default"}, "autoUpdatesChannel": "stable"}
    (config / "settings.json").write_text(json.dumps(settings))
    base = f"http://127.0.0.1:{server.server_port}"
    if args.transport == "socket":
        waiter.write_text("import json, os, urllib.request\n"
                          "body=json.dumps({'socket':os.environ.get('CLAUDE_CODE_MESSAGING_SOCKET'),'token':os.environ.get('CLAUDE_CODE_MESSAGING_TOKEN')}).encode()\n"
                          f"urllib.request.urlopen(urllib.request.Request({base + '/private-capability'!r}, data=body, headers={{'Content-Type':'application/json'}})).close()\n"
                          "print('Messaging capability exported privately; token not printed')\n")
    env = {"PATH": os.environ["PATH"], "HOME": str(home), "SHELL": "/bin/zsh", "TERM": "xterm-256color", "TMPDIR": str(root / "tmp"), "CLAUDE_CONFIG_DIR": str(config), "ANTHROPIC_API_KEY": key, "ANTHROPIC_BASE_URL": base, "DISABLE_AUTOUPDATER": "1", "DISABLE_ERROR_REPORTING": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "HTTP_PROXY": base, "HTTPS_PROXY": base, "ALL_PROXY": base, "NO_PROXY": "127.0.0.1,localhost"}
    argv = [binary, "--session-id", session, "--model", "claude-sonnet-4-6", "--permission-mode", "default", "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--debug-file", str(root / "debug.log"), seed]
    result = {
        "root": str(root), "transport": args.transport, "binary": binary,
        "binarySHA256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
        "scriptSHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "version": subprocess.check_output([binary, "--version"], env=env, text=True).strip(),
        "sessionId": session, "argv": argv, "command": command,
        "markers": {"seed": seed, "signal": marker}, "provider": base,
        "passed": False, "checks": {}, "limitations": [
            "Local fake provider and nonessential traffic disabled, not first-party live authentication",
            "Structural idle wake only, not reconnect recovery",
            "No cbus transport or delivery acknowledgment tested",
        ],
    }
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
    os.set_blocking(master, False)
    process = None
    output = bytearray()
    def pump(seconds):
        until = time.monotonic() + seconds
        while time.monotonic() < until:
            if select.select([master], [], [], min(.1, max(0, until-time.monotonic())))[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                (root / "terminal.log").write_bytes(output)
    def wait(predicate, name, timeout=35):
        until = time.monotonic() + timeout
        while not predicate():
            if time.monotonic() >= until:
                raise TimeoutError(name)
            if process.poll() is not None:
                raise RuntimeError("CC exited: " + str(process.returncode))
            pump(.1)
    try:
        process = subprocess.Popen(argv, cwd=work, env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)
        slave = None
        result["pid"] = process.pid
        wait(lambda: state["mainRequests"] >= 2 and b"CBUS_WAKE_IDLE" in output and ((work / "waiter.pid").exists() if args.transport == "background" else bool(capability.get("socket"))), "initial capability export and idle response", 45)
        before = state["mainRequests"]
        before_all = len(requests)
        idle_at = time.time()
        pump(args.idle_seconds)
        result["checks"]["idle_makes_no_model_requests"] = state["mainRequests"] == before
        result["checks"]["idle_makes_no_auxiliary_requests"] = len(requests) == before_all
        result["idleSeconds"] = time.time()-idle_at
        timeline.append({"event": "external_signal", "at": time.time()})
        if args.transport == "background":
            trigger.write_text(marker)
            wait(lambda: any("<task-notification>" in json.dumps(r["body"].get("messages", [])) for r in requests), "automatic background completion provider request", 30)
        else:
            result["capability"] = {"socket": capability["socket"], "tokenPresent": bool(capability.get("token"))}
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as inbox:
                inbox.settimeout(2)
                inbox.connect(capability["socket"].removeprefix("uds:"))
                inbox.sendall((json.dumps({"type": "auth", "token": capability["token"]}) + "\n").encode())
                inbox.sendall((json.dumps({"type": "user", "message": {"role": "user", "content": marker}}) + "\n").encode())
                inbox.shutdown(socket.SHUT_WR)
                try:
                    result["socketResponse"] = inbox.recv(4096).decode(errors="replace")
                except TimeoutError:
                    result["socketResponse"] = None
        wait(lambda: any(marker in json.dumps(r["body"].get("messages", [])) for r in requests), "provider receives signal marker", 20)
        wait(lambda: b"CBUS_WAKE_RECEIVED" in output, "wake reply rendered", 15)
        main_requests = [r for r in requests if r.get("mainRequest")]
        result["checks"].update({
            "ordinary_interactive_PTY": "-p" not in argv and "--bare" not in argv,
            "no_human_input_after_initial_prompt": True,
            f"{args.transport}_woke_model": True,
            "same_process_alive": process.poll() is None,
            "exact_session_in_all_main_requests": all(
                json.loads(r["body"].get("metadata", {}).get("user_id", "{}" )).get("session_id") == session
                for r in main_requests),
        })
        wait(lambda: any(marker in p.read_text() for p in config.glob("projects/**/*.jsonl")), "wake persisted to exact-session transcript", 10)
        transcripts = list(config.glob("projects/**/*.jsonl"))
        result["transcripts"] = [str(path) for path in transcripts]
        records = []
        for path in transcripts:
            for line in path.read_text().splitlines():
                try:
                    records.append(json.loads(line))
                except json.JSONDecodeError:
                    pass
        result["checks"]["same_session_transcript_contains_wake"] = any(r.get("sessionId") == session and marker in json.dumps(r) for r in records)
        modes = [r["permissionMode"] for r in records if r.get("type") == "permission-mode"]
        result["checks"]["default_permission_mode"] = bool(modes) and set(modes) == {"default"}
        if capability.get("token"):
            result["checks"]["token_not_in_evidence"] = all(
                capability["token"] not in p.read_text(errors="replace")
                for p in (root / "terminal.log", root / "debug.log", root / "provider-requests.json"))
        result["passed"] = all(result["checks"].values())
    except Exception as error:
        result["error"] = repr(error)
    finally:
        if slave is not None:
            os.close(slave)
        waiter_pid = int((work / "waiter.pid").read_text()) if (work / "waiter.pid").exists() else None
        if process is not None and process.poll() is None and result["passed"]:
            os.write(master, b"/exit")
            pump(.4)
            os.write(master, b"\r")
            deadline = time.monotonic() + 5
            while process.poll() is None and time.monotonic() < deadline:
                pump(.1)
        if process is not None and process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
        if waiter_pid:
            try:
                os.kill(waiter_pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        os.close(master)
        server.shutdown()
        server.server_close()
        result["mainProviderRequests"] = state["mainRequests"]
        def gone(pid):
            if pid is None:
                return True
            try:
                os.kill(pid, 0)
                return False
            except ProcessLookupError:
                return True
        deadline = time.monotonic() + 3
        while not gone(waiter_pid) and time.monotonic() < deadline:
            time.sleep(.05)
        result["cleanup"] = {"cliExited": process is None or process.poll() is not None, "serverClosed": server.fileno() == -1, "waiterExited": gone(waiter_pid)}
        if capability.get("socket"):
            result["cleanup"]["nativeSocketRemoved"] = not Path(capability["socket"].removeprefix("uds:")).exists()
        result["passed"] = result["passed"] and all(result["cleanup"].values())
        result["timeline"] = timeline
        (root / "provider-requests.json").write_text(json.dumps(requests, indent=2))
        (root / "result.json").write_text(json.dumps(result, indent=2)+"\n")
        print(json.dumps(result, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
