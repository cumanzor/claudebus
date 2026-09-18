#!/usr/bin/env python3
"""Local fake-provider probes for waking an ordinary Claude Code PTY.

--transport background checks Bash completion and its output-file read.
--transport socket checks the native per-session messaging socket. A permitted
Bash command exports its socket capability privately to this test process; the
token remains in memory and is never written into the retained evidence.

Run: python3 scripts/claude_interactive_wake_canary.py --transport socket
Select --socket-case accepted|busy|session-mismatch|hold|refuse. Use
CLAUDE_TEST_BINARY for an exact installed binary; evidence stays in /tmp.
The scratch profile suppresses marketplace auto-install and OS URL registration.
Inherited credentials are discarded, Git SSH is denied, and HTTP proxies reject
non-local traffic. These controls do not claim an OS-enforced network sandbox.
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
import stat
import struct
import subprocess
import sys
import tempfile
import termios
import textwrap
import threading
import time
import uuid

from claude_cbus_canary import BusProbe


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--idle-seconds", type=float, default=12)
    parser.add_argument("--transport", choices=("background", "socket", "cbus"), default="background")
    parser.add_argument("--socket-case", choices=("accepted", "busy", "session-mismatch", "hold", "refuse"), default="accepted")
    parser.add_argument("--message-uuid", type=lambda value: str(uuid.UUID(value)))
    parser.add_argument("--cbus", default=os.environ.get("CBUS_TEST_BINARY"))
    parser.add_argument("--cbus-sha256")
    parser.add_argument("--cbus-revision")
    args = parser.parse_args()
    if args.idle_seconds < 10:
        parser.error("--idle-seconds must be at least 10")
    if args.transport != "socket" and args.socket_case != "accepted":
        parser.error("--socket-case requires --transport socket")
    if args.transport == "cbus" and not all((args.cbus, args.cbus_sha256, args.cbus_revision)):
        parser.error("--transport cbus requires --cbus, --cbus-sha256, and --cbus-revision")
    selected_binary = os.environ.get("CLAUDE_TEST_BINARY") or shutil.which("claude")
    if not selected_binary:
        parser.error("claude not found; set CLAUDE_TEST_BINARY")
    binary = str(Path(selected_binary).resolve())
    root = Path(tempfile.mkdtemp(prefix="cbus-cc-wake-", dir=os.environ.get("CBUS_TEST_TMPDIR", "/tmp"))).resolve()
    home, config, work = (root / name for name in ("home", "config", "project"))
    for path in (home, config, work, root / "tmp"):
        path.mkdir()
    session = str(uuid.uuid4())
    message_uuid = args.message_uuid or str(uuid.uuid4())
    send_session = str(uuid.uuid4()) if args.socket_case == "session-mismatch" else session
    expects_wake = args.socket_case in ("accepted", "busy")
    seed, marker = "CBUS_WAKE_SEED_" + uuid.uuid4().hex, "CBUS_WAKE_SIGNAL_" + uuid.uuid4().hex
    trigger = work / "signal"
    waiter = work / "wait_for_signal.py"
    waiter.write_text("import os, pathlib, time\np=pathlib.Path('signal')\npathlib.Path('waiter.pid').write_text(str(os.getpid()))\nwhile not p.exists(): time.sleep(.05)\nprint(p.read_text(), flush=True)\n")
    command = f"{sys.executable} {waiter}"
    bus_probe = BusProbe(root, args, session, marker) if args.transport == "cbus" else None
    if bus_probe:
        command = bus_probe.connect_command
    requests, timeline = [], []
    state = {"mainRequests": 0}
    capability = {}
    response_release = threading.Event()

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
            if main_request and n == 2 and args.socket_case == "busy":
                response_release.wait(45)
            if bus_probe and main_request and marker in messages and not state.get("busReplyRequested"):
                state["busReplyRequested"] = True
                content = [{"type": "tool_use", "id": "toolu_cbus_reply", "name": "Bash", "input": {"command": bus_probe.reply_command, "description": "Reply to isolated verifier"}}]
                stop = "tool_use"
            elif main_request and n == 1:
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
    settings = {"permissions": {"allow": [f"Bash({command})"], "defaultMode": "default"}, "autoUpdatesChannel": "stable", "disableDeepLinkRegistration": "disable"}
    if bus_probe:
        settings["permissions"]["allow"].append(f"Bash({bus_probe.reply_command})")
    if args.socket_case in ("hold", "refuse"):
        settings["crossSessionInbound"] = args.socket_case
    (config / "settings.json").write_text(json.dumps(settings))
    base = f"http://127.0.0.1:{server.server_port}"
    if args.transport == "socket":
        waiter.write_text(textwrap.dedent(f"""\
            import ctypes, json, os, pathlib, struct, subprocess, sys, urllib.request
            ancestry, pid = [], os.getpid()
            for _ in range(8):
                row = subprocess.check_output(['ps', '-p', str(pid), '-o', 'pid=,ppid=,comm='], text=True).strip().split(None, 2)
                if len(row) != 3: break
                ancestry.append({{'pid':int(row[0]), 'ppid':int(row[1]), 'comm':row[2]}})
                if str(pid) == os.environ.get('CLAUDE_PID') or int(row[1]) <= 1: break
                pid = int(row[1])
            native_pid = int(os.environ['CLAUDE_PID'])
            if sys.platform == 'darwin':
                libc = ctypes.CDLL(None, use_errno=True)
                mib, size = (ctypes.c_int * 3)(1, 49, native_pid), ctypes.c_size_t()
                if libc.sysctl(mib, 3, None, ctypes.byref(size), None, 0): raise OSError(ctypes.get_errno())
                buf = ctypes.create_string_buffer(size.value)
                if libc.sysctl(mib, 3, buf, ctypes.byref(size), None, 0): raise OSError(ctypes.get_errno())
                raw = buf.raw[:size.value]
                pos = raw.index(b'\\0', 4) + 1
                while raw[pos] == 0: pos += 1
                argv0 = raw[pos:raw.index(b'\\0', pos)].decode()
            else:
                argv0 = pathlib.Path(f'/proc/{{native_pid}}/cmdline').read_bytes().split(b'\\0', 1)[0].decode()
            data = {{
                'socket':os.environ.get('CLAUDE_CODE_MESSAGING_SOCKET'),
                'token':os.environ.get('CLAUDE_CODE_MESSAGING_TOKEN'),
                'safeEnvironment':{{k:os.environ.get(k) for k in [
                    'CLAUDE_CODE_SESSION_ID','CLAUDE_SESSION_ID','CLAUDE_PID',
                    'CBUS_SESSION_ID','CLAUDE_CONFIG_DIR','CLAUDE_CODE_SESSION_LOG','HOME']}},
                'transcriptEnvironmentKeys':[k for k in os.environ if 'TRANSCRIPT' in k.upper()],
                'childPID':os.getpid(), 'childPPID':os.getppid(), 'ancestry':ancestry,
                'nativeArgv0':argv0,
            }}
            request = urllib.request.Request({base + '/private-capability'!r}, data=json.dumps(data).encode(), headers={{'Content-Type':'application/json'}})
            urllib.request.urlopen(request).close()
            print('Messaging capability exported privately; token not printed')
            """))
    env = {"PATH": os.environ["PATH"], "HOME": str(home), "SHELL": "/bin/sh", "TERM": "xterm-256color", "TMPDIR": str(root / "tmp"), "CLAUDE_CONFIG_DIR": str(config), "ANTHROPIC_API_KEY": key, "ANTHROPIC_BASE_URL": base, "DISABLE_AUTOUPDATER": "1", "DISABLE_ERROR_REPORTING": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "HTTP_PROXY": base, "HTTPS_PROXY": base, "ALL_PROXY": base, "NO_PROXY": "127.0.0.1,localhost"}
    env.update(http_proxy=base, https_proxy=base, all_proxy=base, no_proxy="127.0.0.1,localhost",
               GIT_SSH_COMMAND="/usr/bin/false", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null",
               GIT_TERMINAL_PROMPT="0", CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL="1")
    if bus_probe:
        env.update(CBUS_DIR=str(bus_probe.bus), CBUS_UPDATE_CHECK="0")
    argv = [binary, "--session-id", session, "--model", "claude-sonnet-4-6", "--permission-mode", "default", "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--debug-file", str(root / "debug.log"), seed]
    result = {
        "root": str(root), "transport": args.transport, "socketCase": args.socket_case, "binary": binary,
        "binarySHA256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
        "scriptSHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "version": subprocess.check_output([binary, "--version"], env=env, text=True).strip(),
        "sessionId": session, "argv": argv, "command": command,
        "messageUUID": message_uuid, "sendSessionId": send_session,
        "markers": {"seed": seed, "signal": marker}, "provider": base,
        "passed": False, "checks": {}, "limitations": [
            "Local fake provider and nonessential traffic disabled, not first-party live authentication",
            "Structural idle wake only, not reconnect recovery",
            "No cbus transport or delivery acknowledgment tested",
        ],
    }
    if bus_probe:
        result["cbus"] = bus_probe.result
        result["busSupportSHA256"] = hashlib.sha256(Path(sys.modules[BusProbe.__module__].__file__).read_bytes()).hexdigest()
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
    def transcript_rows():
        records = []
        for path in config.glob("projects/**/*.jsonl"):
            for line in path.read_text().splitlines(keepends=True):
                if line.endswith("\n"):
                    records.append(json.loads(line))
        return records

    def receipts():
        return [r for r in transcript_rows()
                if r.get("type") == "user" and r.get("sessionId") == session
                and marker in json.dumps(r)]

    def run_wake():
        wait(lambda: state["mainRequests"] >= 2 and (args.socket_case == "busy" or b"CBUS_WAKE_IDLE" in output) and ((work / "waiter.pid").exists() if args.transport == "background" else bool(capability.get("socket"))), "initial capability export and response", 45)
        before = state["mainRequests"]
        before_all = len(requests)
        idle_at = time.time()
        pump(args.idle_seconds)
        window = "busy" if args.socket_case == "busy" else "idle"
        result["checks"][f"{window}_makes_no_additional_model_requests"] = state["mainRequests"] == before
        result["checks"][f"{window}_makes_no_auxiliary_requests"] = len(requests) == before_all
        result[window + "Seconds"] = time.time()-idle_at
        timeline.append({"event": "external_signal", "at": time.time()})
        if args.transport == "background":
            trigger.write_text(marker)
            wait(lambda: any("<task-notification>" in json.dumps(r["body"].get("messages", [])) for r in requests), "automatic background completion provider request", 30)
        else:
            result["capability"] = {"socket": capability["socket"], "tokenPresent": bool(capability.get("token"))}
            result["childContext"] = {key: capability.get(key) for key in (
                "safeEnvironment", "transcriptEnvironmentKeys", "childPID", "childPPID", "ancestry", "nativeArgv0")}
            result["checks"]["socket_and_token_exported"] = bool(capability.get("socket")) and bool(capability.get("token"))
            endpoint = Path(capability["socket"].removeprefix("uds:"))
            endpoint_stat, directory_stat = endpoint.lstat(), endpoint.parent.resolve().stat()
            result["capability"].update(socketMode=oct(endpoint_stat.st_mode & 0o777),
                                        socketUID=endpoint_stat.st_uid, socketDev=endpoint_stat.st_dev,
                                        socketIno=endpoint_stat.st_ino,
                                        directoryMode=oct(directory_stat.st_mode & 0o777),
                                        directoryUID=directory_stat.st_uid)
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as inbox:
                inbox.settimeout(2)
                inbox.connect(capability["socket"].removeprefix("uds:"))
                inbox.sendall((json.dumps({"type": "auth", "token": capability["token"]}) + "\n").encode())
                inbox.sendall((json.dumps({"type": "user", "session_id": send_session, "uuid": message_uuid, "message": {"role": "user", "content": marker}}) + "\n").encode())
                inbox.shutdown(socket.SHUT_WR)
                try:
                    result["socketResponse"] = inbox.recv(4096).decode(errors="replace")
                except TimeoutError:
                    result["socketResponse"] = None
        if args.socket_case == "busy":
            pump(2)
            outstanding = [r for r in requests if r.get("mainRequest") and "responseCompletedAt" not in r]
            result["checks"]["busy_message_does_not_interrupt_active_request"] = state["mainRequests"] == before and len(outstanding) == 1
            timeline.append({"event": "release_busy_response", "at": time.time()})
            response_release.set()
        if expects_wake:
            wait(lambda: any(marker in json.dumps(r["body"].get("messages", [])) for r in requests), "provider receives signal marker", 20)
            wait(lambda: b"CBUS_WAKE_RECEIVED" in output, "wake reply rendered", 15)
        else:
            pump(5)
            result["checks"]["blocked_message_makes_no_model_request"] = state["mainRequests"] == before
            result["checks"]["blocked_marker_absent_from_provider"] = not any(marker in json.dumps(r["body"]) for r in requests)
        main_requests = [r for r in requests if r.get("mainRequest")]
        result["checks"].update({
            "ordinary_interactive_PTY": "-p" not in argv and "--bare" not in argv,
            "no_human_input_after_initial_prompt": True,
            f"{args.transport}_woke_model_as_expected": (state["mainRequests"] > before) == expects_wake,
            "same_process_alive": process.poll() is None,
            "exact_session_in_all_main_requests": all(
                json.loads(r["body"].get("metadata", {}).get("user_id", "{}" )).get("session_id") == session
                for r in main_requests),
        })
        if expects_wake:
            wait(lambda: any(args.transport != "socket" or r.get("uuid") == message_uuid for r in receipts()),
                 "exact user receipt persisted to exact-session transcript", 10)
        transcripts = list(config.glob("projects/**/*.jsonl"))
        result["transcripts"] = [str(path) for path in transcripts]
        records, receipt = transcript_rows(), receipts()
        result["checks"]["transcript_receipt_matches_expected"] = bool(receipt) == expects_wake
        if args.transport == "socket" and expects_wake:
            result["checks"]["supplied_uuid_persisted_exactly_once"] = len(receipt) == 1 and receipt[0].get("uuid") == message_uuid
            (root / "inbound-user-row.json").write_text(json.dumps(receipt[0], indent=2)+"\n")
        if not expects_wake:
            decision = {
                "session-mismatch": f'session_id mismatch (got "{send_session}", expected "{session}")',
                "hold": "held inbound peer message",
                "refuse": "refused inbound peer message",
            }[args.socket_case]
            result["nativeProcessingLines"] = [
                line for line in (root / "debug.log").read_text().splitlines()
                if "[cross-session-inbound]" in line or "[uds-messaging]" in line and "Dropping" in line]
            result["checks"]["native_decision_matches_expected"] = any(
                decision in line for line in result["nativeProcessingLines"])
        modes = [r["permissionMode"] for r in records if r.get("type") == "permission-mode"]
        result["checks"]["default_permission_mode"] = bool(modes) and set(modes) == {"default"}
        debug_text = (root / "debug.log").read_text()
        result["checks"]["marketplace_autoinstall_disabled"] = "Official marketplace auto-install disabled via env var" in debug_text
        result["checks"]["no_temporary_url_handler"] = not (home / "Applications/Claude Code URL Handler.app").exists()
        result["checks"]["no_marketplace_downloaded"] = not (config / "plugins/marketplaces").exists()
        if capability.get("token"):
            result["checks"]["token_not_in_evidence"] = all(
                capability["token"] not in p.read_text(errors="replace")
                for p in (root / "terminal.log", root / "debug.log", root / "provider-requests.json"))
        result["passed"] = all(result["checks"].values())

    def run_cbus():
        wait(lambda: state["mainRequests"] >= 2 and b"CBUS_WAKE_IDLE" in output, "actual Claude Bash self-connect", 45)
        bus_probe.connection = bus_probe.status()
        bus_probe.result["connected"] = bus_probe.connection
        before, before_all = state["mainRequests"], len(requests)
        idle_at = time.time()
        pump(args.idle_seconds)
        result["idleSeconds"] = time.time() - idle_at
        result["checks"]["idle_makes_no_additional_model_requests"] = state["mainRequests"] == before
        result["checks"]["idle_makes_no_auxiliary_requests"] = len(requests) == before_all
        timeline.append({"event": "external_cbus_send", "at": time.time()})
        bus_probe.command(["send", bus_probe.target, "--from", bus_probe.sender, marker])
        wait(lambda: state.get("busReplyRequested") and b"CBUS_WAKE_RECEIVED" in output, "inbound event and actual cbus reply", 25)
        wait(lambda: bool(bus_probe.acknowledgments()), "verifier inbox acknowledgment", 10)
        wait(bus_probe.receipt_ready, "daemon exact transcript receipt", 15)
        result["checks"].update(bus_probe.check_receipt(transcript_rows(), process.pid))
        main_requests = [r for r in requests if r.get("mainRequest")]
        result["checks"].update({
            "ordinary_interactive_PTY": "-p" not in argv and "--bare" not in argv,
            "no_human_input_after_initial_prompt": True,
            "same_process_alive": process.poll() is None,
            "exact_session_in_all_main_requests": all(json.loads(r["body"].get("metadata", {}).get("user_id", "{}")).get("session_id") == session for r in main_requests),
            "cbus_token_not_in_public_evidence": bus_probe.token_absent_from_evidence(),
            "no_temporary_url_handler": not (home / "Applications/Claude Code URL Handler.app").exists(),
            "no_marketplace_downloaded": not (config / "plugins/marketplaces").exists(),
            "no_nonlocal_proxy_attempts": not any(e["event"] == "blocked_proxy" for e in timeline),
        })
        modes = [r["permissionMode"] for r in transcript_rows() if r.get("type") == "permission-mode"]
        result["checks"]["default_permission_mode"] = bool(modes) and set(modes) == {"default"}
        result["limitations"] = ["Local fake provider, not real-account field proof", "Single local Claude recipient and passive verifier; no relay, other harness, reconnect, or recovery proof"]
        result["passed"] = all(result["checks"].values())
    try:
        if bus_probe:
            bus_probe.start(env, work)
        process = subprocess.Popen(argv, cwd=work, env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)
        slave = None
        result["pid"] = process.pid
        if bus_probe:
            run_cbus()
        else:
            run_wake()
    except Exception as error:
        result["error"] = repr(error)
    finally:
        response_release.set()
        if slave is not None:
            os.close(slave)
        waiter_pid = int((work / "waiter.pid").read_text()) if (work / "waiter.pid").exists() else None
        owned_socket = None
        # Pin only this launched process's logged socket, including startup failures
        # before Bash exports its capability. SIGTERM can otherwise leave it stale.
        if process is not None and (root / "debug.log").exists():
            for name in re.findall(r"\[uds-messaging\] Listening: ([^\n]+)", (root / "debug.log").read_text()):
                path = Path(name)
                if path.name != f"{process.pid}.sock":
                    continue
                try:
                    st = path.lstat()
                except FileNotFoundError:
                    continue
                if stat.S_ISSOCK(st.st_mode) and st.st_uid == os.geteuid():
                    owned_socket = (path, st.st_dev, st.st_ino)
        if process is not None and process.poll() is None and (b"CBUS_WAKE_IDLE" in output or b"CBUS_WAKE_RECEIVED" in output):
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
        if bus_probe:
            result["cleanup"].update(bus_probe.cleanup())
        if owned_socket is not None:
            path, dev, ino = owned_socket
            try:
                st = path.lstat()
                if process.poll() is not None and (st.st_dev, st.st_ino) == (dev, ino):
                    path.unlink()
                    result["forcedOwnedSocketCleanup"] = True
            except FileNotFoundError:
                pass
            result["cleanup"]["nativeSocketRemoved"] = not path.exists()
        result["passed"] = result["passed"] and all(result["cleanup"].values())
        result["timeline"] = timeline
        (root / "provider-requests.json").write_text(json.dumps(requests, indent=2))
        (root / "result.json").write_text(json.dumps(result, indent=2)+"\n")
        print(json.dumps(result, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
