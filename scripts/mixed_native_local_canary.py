#!/usr/bin/env python3
"""Two ordinary native PTYs self-connect and exchange three real cbus messages.

Every model response comes from a local scripted fake provider. Scratch homes,
profiles and a frozen SHA-gated cbus binary are retained under --temp-root.
No observer connect, Monitor, paid inference or installed profile changes occur.
Traffic controls are environment restrictions, not an OS network sandbox.
Historical defaults require Codex 0.154.0 and Claude 2.1.277. For another explicit
pair, add --expected-codex-version 0.155.1 --expected-claude-version 2.1.278
alongside --codex and --claude; installed and rollout versions must still match.
"""
import argparse
import fcntl
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pty
import select
import socket
import shlex
import signal
import struct
import subprocess
import sys
import termios
import threading
import time
import uuid

from claude_cbus_canary import BusProbe
from codex_cli_resume_canary import ResumeCanary
from codex_queue_lifecycle_canary import FakeProvider


def rows(path):
    if not path or not path.exists():
        return []
    return [json.loads(line) for line in path.read_bytes().splitlines(keepends=True) if line.endswith(b"\n")]


def incoming(items, sender, recipient, marker):
    return any(item.get("role") == "user" and
               f"cbus msg from={sender} to={recipient}" in json.dumps(item.get("content", []), ensure_ascii=False) and
               marker in json.dumps(item.get("content", [])) for item in items)


class MixedCanary(ResumeCanary):
    def __init__(self, args):
        super().__init__(args)
        self.args = args
        self.cc_session = str(uuid.uuid4())
        self.seed = "MIXED_NATIVE_SEED_" + uuid.uuid4().hex
        self.markers = {key: key.upper() + "_" + uuid.uuid4().hex for key in ("request", "reply", "ack")}
        self.fixture = BusProbe(self.root, args, self.cc_session, self.markers["request"])
        self.cbus = str(self.fixture.binary)
        self.bus = self.fixture.bus
        self.target = self.fixture.channel + "/codex"
        self.cc_target = self.fixture.target
        self.cc_process = self.cc_master = self.cc_server = None
        self.cc_output, self.cc_requests, self.blocked = bytearray(), [], []
        self.cx_steps, self.cc_steps = [], []
        self.result.update(blockedProxyRequests=self.blocked, script=Path(__file__).name, scriptSHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                           cbus=self.fixture.result, claudeSession=self.cc_session, markers=self.markers,
                           limitations=["Local scripted fake providers; no first-party account or paid model reasoning",
                                        "Environment traffic controls, not OS-enforced networking isolation",
                                        "Local daemon only; relay and OpenCode not exercised"])

    def tool(self, label, argv, number, body):
        self.cx_steps.append(label)
        command = shlex.join(argv)
        names = {tool.get("name") for tool in body.get("tools", [])}
        if "exec_command" in names:
            name, arguments = "exec_command", {"cmd": command, "login": False, "yield_time_ms": 10000, "max_output_tokens": 5000}
        elif "shell_command" in names:
            name, arguments = "shell_command", {"command": command}
        else:
            raise AssertionError("ordinary Codex shell tool unavailable")
        return [{"type": "function_call", "id": f"mixed-item-{number}", "call_id": "mixed-" + label,
                 "name": name, "arguments": json.dumps(arguments)}]

    def codex_response(self, number, body):
        if body.get("client_metadata", {}).get("thread_id") != self.thread:
            return []
        if "connect" not in self.cx_steps:
            return self.tool("connect", [self.cbus, "connect", self.fixture.channel, "codex", "--json"], number, body)
        if "send" not in self.cx_steps:
            return self.tool("send", [self.cbus, "send", self.cc_target, self.markers["request"]], number, body)
        if incoming(body.get("input", []), self.cc_target, self.target, self.markers["reply"]) and "ack" not in self.cx_steps:
            return self.tool("ack", [self.cbus, "send", self.cc_target, self.markers["ack"]], number, body)
        return []

    def claude_response(self, body):
        messages = body.get("messages", [])
        main = self.seed in json.dumps(messages) and any(t.get("name") == "Bash" for t in body.get("tools", []))
        label, command = None, None
        if main and "connect" not in self.cc_steps:
            label, command = "connect", self.fixture.connect_command
        elif main and incoming(messages, self.target, self.cc_target, self.markers["request"]) and "reply" not in self.cc_steps:
            label, command = "reply", shlex.join([self.cbus, "send", self.target, self.markers["reply"]])
        if label:
            self.cc_steps.append(label)
            return [{"type": "tool_use", "id": "toolu_mixed_" + label, "name": "Bash",
                     "input": {"command": command, "description": "Isolated mixed harness " + label}}], "tool_use"
        return [{"type": "text", "text": "MIXED_NATIVE_IDLE"}], "end_turn"

    def prepare(self):
        # Deliberate minimal environment: no real account credentials or session identity.
        self.env = {key: self.env[key] for key in ("PATH", "CODEX_HOME", "CBUS_DIR", "TERM")}
        self.env.update(SHELL="/bin/sh", CBUS_UPDATE_CHECK="0", LANG="en_US.UTF-8")
        super().prepare()
        self.provider.close()
        self.provider = FakeProvider(output=self.codex_response)
        config = (self.home / "config.toml").read_text()
        import re
        config = re.sub(r"http://127\.0\.0\.1:\d+/v1", f"http://127.0.0.1:{self.provider.server.server_port}/v1", config)
        # Only the isolated test CLI may write outside its workspace into scratch bus state.
        config = config.replace('sandbox_mode = "read-only"', 'sandbox_mode = "danger-full-access"')
        (self.home / "config.toml").write_text(config)
        self.cc_home, self.cc_config, self.cc_work = (self.root / p for p in ("cc-home", "cc-config", "cc-work"))
        for path in (self.cc_home, self.cc_config, self.cc_work, self.root / "tmp"):
            path.mkdir()
        owner = self

        class Provider(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_CONNECT(self):
                owner.blocked.append({"method": self.command, "target": self.path, "status": 403})
                self.send_error(403)

            def do_GET(self):
                self.send_error(403)

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
                owner.cc_requests.append({"at": time.time(), "path": self.path, "body": body})
                if "count_tokens" in self.path:
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'{"input_tokens":100}')
                    return
                if not self.path.startswith("/v1/messages"):
                    owner.blocked.append({"method": self.command, "target": self.path, "status": 403})
                    self.send_error(403)
                    return
                content, stop = owner.claude_response(body)
                message = {"id": "msg_" + uuid.uuid4().hex, "type": "message", "role": "assistant",
                           "model": body.get("model"), "content": content, "stop_reason": stop,
                           "stop_sequence": None, "usage": {"input_tokens": 100, "output_tokens": 10}}
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream" if body.get("stream") else "application/json")
                self.end_headers()
                if not body.get("stream"):
                    self.wfile.write(json.dumps(message).encode())
                    return

                def event(kind, data):
                    self.wfile.write(("event: " + kind + "\ndata: " + json.dumps({"type": kind, **data}) + "\n\n").encode())
                    self.wfile.flush()
                event("message_start", {"message": {**message, "content": [], "stop_reason": None}})
                for index, block in enumerate(content):
                    tool = block["type"] == "tool_use"
                    start = {**block, "input": {}} if tool else {"type": "text", "text": ""}
                    delta = {"type": "input_json_delta", "partial_json": json.dumps(block["input"])} if tool else {"type": "text_delta", "text": block["text"]}
                    event("content_block_start", {"index": index, "content_block": start})
                    event("content_block_delta", {"index": index, "delta": delta})
                    event("content_block_stop", {"index": index})
                event("message_delta", {"delta": {"stop_reason": stop, "stop_sequence": None}, "usage": {"output_tokens": 10}})
                event("message_stop", {})

        self.cc_server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
        self.cc_server.daemon_threads = True
        threading.Thread(target=self.cc_server.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{self.cc_server.server_port}"
        key = "sk-ant-mixed-offline-test-not-real"
        conf = {"hasCompletedOnboarding": True, "lastOnboardingVersion": "2.1.277", "theme": "dark",
                "customApiKeyResponses": {"approved": [key[-20:]], "rejected": []},
                "projects": {str(self.cc_work): {"hasTrustDialogAccepted": True, "allowedTools": [], "hasCompletedProjectOnboarding": True}}}
        for path in (self.cc_home / ".claude.json", self.cc_config / ".claude.json"):
            path.write_text(json.dumps(conf))
        reply = shlex.join([self.cbus, "send", self.target, self.markers["reply"]])
        (self.cc_config / "settings.json").write_text(json.dumps({"permissions": {"allow": [f"Bash({c})" for c in (self.fixture.connect_command, reply)], "defaultMode": "default"}, "disableDeepLinkRegistration": "disable"}))
        traffic = dict(HTTP_PROXY=base, HTTPS_PROXY=base, ALL_PROXY=base, NO_PROXY="127.0.0.1,localhost",
                       http_proxy=base, https_proxy=base, all_proxy=base, no_proxy="127.0.0.1,localhost",
                       GIT_SSH_COMMAND="/usr/bin/false", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0")
        self.env.update(traffic)
        self.cc_env = dict(traffic, PATH=self.env["PATH"], HOME=str(self.cc_home), SHELL="/bin/sh", TERM="xterm-256color",
                           TMPDIR=str(self.root / "tmp"), CLAUDE_CONFIG_DIR=str(self.cc_config), ANTHROPIC_API_KEY=key,
                           ANTHROPIC_BASE_URL=base, DISABLE_AUTOUPDATER="1", DISABLE_ERROR_REPORTING="1", DISABLE_TELEMETRY="1",
                           CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL="1",
                           CBUS_DIR=str(self.bus), CBUS_UPDATE_CHECK="0")
        self.fixture.start(self.env, self.work)

    def tick(self):
        if self.master is not None:
            self.pump(.05)
        if self.cc_master is not None and select.select([self.cc_master], [], [], .05)[0]:
            try:
                self.cc_output.extend(os.read(self.cc_master, 65536))
            except OSError:
                pass
        if self.thread:
            for request in self.provider_turns():
                request["release"].set()
        for process in (self.process, self.cc_process):
            if process is not None and process.poll() is not None:
                raise RuntimeError("owned CLI exited before acceptance")

    def until(self, predicate, label, timeout=45):
        deadline = time.monotonic() + timeout
        while not predicate():
            if time.monotonic() >= deadline:
                raise TimeoutError(label)
            self.tick()

    def cc_rows(self):
        paths = list(self.cc_config.glob(f"projects/*/{self.cc_session}.jsonl"))
        if len(paths) > 1:
            raise AssertionError("ambiguous exact Claude transcript")
        return rows(paths[0]) if paths else []

    def states(self):
        return {r["alias"]: r for r in json.loads(self.fixture.command(["connection", "status", "--json"]))}

    def check_codex_identity(self, meta):
        self.result["codexRolloutVersion"] = meta.get("cli_version")
        self.check("ordinary_codex_cli", meta.get("source") == "cli")
        self.check("codex_rollout_version_matches_expected", meta.get("cli_version") == self.args.expected_codex_version)

    def run(self):
        if self.args.require_init_reparented:
            parent = os.getppid()
            grandparent = int(subprocess.run(["ps", "-o", "ppid=", "-p", str(parent)], capture_output=True, text=True).stdout.strip() or 0)
            if 1 not in (parent, grandparent):
                raise RuntimeError("--require-init-reparented: this canary's runner is not reparented to init")
            self.result["initReparented"] = {"parent": parent, "grandparent": grandparent}
        guard_args = (self.args.guard_cbus, self.args.guard_daemon_pid, self.args.guard_channel)
        if any(guard_args) and not all(guard_args):
            raise RuntimeError("--guard-cbus, --guard-daemon-pid and --guard-channel go together")
        self.guard_before = self.guard_snapshot()
        if self.guard_before is not None:
            self.result["guard"] = {"before": self.guard_before}
            if "cbus daemon serve" not in self.guard_before["daemonCommand"] or self.guard_before["rosterExit"] != 0:
                raise RuntimeError("guarded daemon or roster is not readable before the run")
        # Refuse an unintended runtime before starting fake providers or the daemon.
        version_env = {"PATH": self.env["PATH"], "HOME": str(self.root), "CODEX_HOME": str(self.home),
                       "DISABLE_AUTOUPDATER": "1", "DISABLE_TELEMETRY": "1",
                       "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
        self.result["expectedVersions"] = {"codex": self.args.expected_codex_version,
                                           "claude": self.args.expected_claude_version}
        self.result["versions"] = {"codex": subprocess.check_output([self.codex, "--version"], env=version_env, cwd=self.root, timeout=10, text=True).strip(),
                                   "claude": subprocess.check_output([self.args.claude, "--version"], env=version_env, cwd=self.root, timeout=10, text=True).strip()}
        self.check("installed_codex_version_matches_expected", self.result["versions"]["codex"] == "codex-cli " + self.args.expected_codex_version)
        self.check("installed_claude_version_matches_expected", self.result["versions"]["claude"] == self.args.expected_claude_version + " (Claude Code)")
        self.prepare()
        self.cc_master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        os.set_blocking(self.cc_master, False)
        argv = [self.args.claude, "--session-id", self.cc_session, "--model", "claude-sonnet-4-6", "--permission-mode", "default",
                "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--debug-file", str(self.root / "debug.log"), self.seed]
        try:
            self.cc_process = subprocess.Popen(argv, env=self.cc_env, cwd=self.cc_work, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        finally:
            os.close(slave)
        self.until(lambda: b"MIXED_NATIVE_IDLE" in self.cc_output and self.states().get("receiver", {}).get("state") == "socket-ready", "Claude self-connect")
        self.start_cli(prompt=self.seed)
        self.until(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "Codex transcript")
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.until(lambda: any(r.get("type") == "session_meta" for r in self.entries()), "Codex identity")
        meta = next(r["payload"] for r in self.entries() if r.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(codexThread=self.thread, rollout=str(self.rollout), claudePID=self.cc_process.pid)
        self.check_codex_identity(meta)
        self.until(lambda: incoming([r.get("message", {}) for r in self.cc_rows() if r.get("type") == "user"], self.target, self.cc_target, self.markers["ack"]), "three-hop native bus round trip", 70)
        def ack_checkpoint():
            state = self.states()["receiver"]
            receipt = state.get("lastAccepted", {})
            return not state.get("pending") and receipt.get("state") == "received" and any(
                r.get("uuid") == receipt.get("itemId") and self.markers["ack"] in json.dumps(r) for r in self.cc_rows())
        self.until(ack_checkpoint, "exact final Claude acknowledgment receipt checkpoint")
        self.until(lambda: self.completed_count() >= 2, "Codex reply tool turn completion")
        self.result["connections"] = self.states()
        cc, cx = (self.result["connections"][key] for key in ("receiver", "codex"))
        self.check("both_exact_native_bindings", cc["threadId"] == self.cc_session and cc["harness"] == "claude" and cx["threadId"] == self.thread and cx.get("harness", "codex") == "codex")
        self.check("codex_runtime_witness_self_join", cx["config"]["BindingSource"] == "runtime-open-queue" and cx["config"]["RuntimePID"] > 0)
        self.check("claude_original_runtime_bound", cc["claude"]["binding"]["Endpoint"]["PID"] == self.cc_process.pid)
        for marker_key in ("request", "ack"):
            matches = [r for r in self.cc_rows() if r.get("type") == "user" and r.get("sessionId") == self.cc_session and incoming([r.get("message", {})], self.target, self.cc_target, self.markers[marker_key])]
            self.check("claude_exact_receipt_once_" + marker_key, len(matches) == 1)
        self.check("codex_exact_reply_received_once", self.count(self.markers["reply"]) == 1)
        self.check("claude_checkpoint_matches_ack_UUID", any(r.get("uuid") == cc["lastAccepted"].get("itemId") and self.markers["ack"] in json.dumps(r) for r in self.cc_rows()))
        main_cc = [r["body"] for r in self.cc_requests if self.seed in json.dumps(r["body"].get("messages", [])) and r["path"].startswith("/v1/messages")]
        self.check("claude_provider_exact_session", bool(main_cc) and all(json.loads(b.get("metadata", {}).get("user_id", "{}")).get("session_id") == self.cc_session for b in main_cc))
        self.check("claude_model_received_codex_request", any(incoming(b.get("messages", []), self.target, self.cc_target, self.markers["request"]) for b in main_cc))
        self.check("claude_model_received_codex_ack", any(incoming(b.get("messages", []), self.target, self.cc_target, self.markers["ack"]) for b in main_cc))
        self.check("codex_model_received_claude_reply", any(incoming(r["body"].get("input", []), self.cc_target, self.target, self.markers["reply"]) for r in self.provider_turns()))
        self.check("session_tools_only", self.cx_steps == ["connect", "send", "ack"] and self.cc_steps == ["connect", "reply"] and not any(r["args"][0] in ("connect", "send") for r in self.fixture.result["commands"]))
        for label in self.cx_steps:
            outputs = [item for r in self.provider_turns() for item in r["body"].get("input", []) if item.get("type") == "function_call_output" and item.get("call_id") == "mixed-" + label]
            self.check("codex_tool_success_" + label, bool(outputs) and all("Process exited with code 0" in str(o.get("output")) or '"exit_code":0' in str(o.get("output")).replace(" ", "") for o in outputs))
        for label in self.cc_steps:
            outputs = [block for body in main_cc for message in body.get("messages", [])
                       for block in message.get("content", []) if isinstance(block, dict)
                       and block.get("type") == "tool_result" and block.get("tool_use_id") == "toolu_mixed_" + label]
            self.check("claude_tool_success_" + label, bool(outputs) and all(not block.get("is_error") for block in outputs))
        for alias, sender, recipient, marker in (("receiver", self.target, self.cc_target, self.markers["request"]),
                                                 ("codex", self.cc_target, self.target, self.markers["reply"]),
                                                 ("receiver", self.target, self.cc_target, self.markers["ack"])):
            self.check("bus_envelope_exact_" + marker.split("_")[0].lower(), len([row for row in rows(self.bus / self.fixture.channel / alias / "inbox.jsonl")
                       if row.get("from") == sender and row.get("to") == recipient and row.get("text") == marker]) == 1)
        self.check("same_daemon_and_both_original_CLIs", self.fixture.health == json.loads(self.fixture.command(["daemon", "status", "--json"])) and self.cc_process.poll() is None and self.process.poll() is None)
        self.check("all_observed_nonlocal_proxy_requests_denied", all(r["status"] == 403 for r in self.blocked))
        self.check_list_consumers(cc, cx)
        self.check_disconnect_fence()
        self.close_codex_peer()
        self.check_guard("end")
        self.result["passed"] = True

    def daemon_http(self, method, path, body=None):
        # Raw control-socket request: the CLI has no way to send a fenced disconnect.
        payload = json.dumps(body).encode() if body is not None else b""
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
            conn.settimeout(10)
            conn.connect(str(self.bus / ".daemon/control.sock"))
            conn.sendall(f"{method} {path} HTTP/1.1\r\nHost: cbus\r\nContent-Type: application/json\r\nContent-Length: {len(payload)}\r\nConnection: close\r\n\r\n".encode() + payload)
            data = b""
            while chunk := conn.recv(65536):
                data += chunk
        head, _, rest = data.partition(b"\r\n\r\n")
        code = int(head.split(b" ", 2)[1])
        if b"chunked" in head.lower():
            decoded, rest = b"", rest
            while rest:
                size, _, rest = rest.partition(b"\r\n")
                n = int(size, 16)
                if n == 0:
                    break
                decoded, rest = decoded + rest[:n], rest[n + 2:]
            rest = decoded
        return code, rest.decode(errors="replace")

    @staticmethod
    def start_token(pid):
        run = subprocess.run(["ps", "-o", "lstart=", "-p", str(pid)], capture_output=True, text=True)
        return run.stdout.strip() if run.returncode == 0 else ""

    def owned_descendant(self, root_pid, pid):
        # Independent of the daemon: pid must sit in the process tree the canary started.
        table = subprocess.run(["ps", "-A", "-o", "pid=,ppid="], capture_output=True, text=True, check=True).stdout
        children = {}
        for line in table.splitlines():
            child, parent = (int(field) for field in line.split())
            children.setdefault(parent, []).append(child)
        tree, frontier = {root_pid}, [root_pid]
        while frontier:
            for child in children.get(frontier.pop(), []):
                if child not in tree:
                    tree.add(child)
                    frontier.append(child)
        return pid in tree

    def codex_witness(self):
        consumer = self.states()["codex"].get("consumer", {}).get("pid")
        if not consumer or not self.owned_descendant(self.process.pid, consumer):
            raise RuntimeError(f"Codex consumer pid {consumer} is not in the process tree this canary started; refusing to act on it")
        token = self.start_token(consumer)
        if not token:
            raise RuntimeError(f"cannot pin the start time of Codex consumer pid {consumer}")
        return consumer, token

    def guard_snapshot(self):
        if not self.args.guard_cbus:
            return None
        pid = self.args.guard_daemon_pid
        command = subprocess.run(["ps", "-o", "command=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()
        env = {k: v for k, v in os.environ.items() if k != "CBUS_DIR"}
        roster = subprocess.run([self.args.guard_cbus, "list", self.args.guard_channel], capture_output=True, text=True, env=env, timeout=15)
        return {"daemonCommand": command, "roster": roster.stdout, "rosterExit": roster.returncode}

    def check_guard(self, phase):
        if not self.args.guard_cbus:
            return
        now = self.guard_snapshot()
        self.result.setdefault("guard", {})[phase] = now
        if now != self.guard_before or "cbus daemon serve" not in now["daemonCommand"]:
            raise RuntimeError(f"guarded daemon or roster changed at {phase}; aborting")

    def check_list_consumers(self, cc, cx):
        listing = json.loads(self.fixture.command(["list", self.fixture.channel, "--json"]))
        peers = {p["alias"]: p for channel in listing["channels"] for p in channel["peers"]}
        text = self.fixture.command(["list", self.fixture.channel])
        daemon_pid = self.fixture.health["pid"]
        codex_consumer, _ = self.codex_witness()
        self.result["listConsumers"] = {"json": peers, "text": text, "daemonPID": daemon_pid, "codexConsumerPID": codex_consumer}
        def row(alias):
            return next((line for line in text.splitlines() if f"/{alias} " in line), "")
        self.check("list_claude_row_shows_consumer_not_daemon",
                   peers["receiver"].get("consumerPid") == self.cc_process.pid and peers["receiver"].get("consumerState") == "online"
                   and peers["receiver"].get("listenerPid") == daemon_pid and f"pid={self.cc_process.pid} " in row("receiver"))
        self.check("list_codex_row_shows_consumer_not_daemon",
                   bool(codex_consumer) and codex_consumer != daemon_pid and peers["codex"].get("consumerPid") == codex_consumer
                   and peers["codex"].get("consumerState") == "online" and peers["codex"].get("listenerPid") == daemon_pid
                   and f"pid={codex_consumer} " in row("codex"))

    def check_disconnect_fence(self):
        code, body = self.daemon_http("GET", "/health")
        self.result["health"] = body
        self.check("health_advertises_fenced_disconnect", code == 200 and json.loads(body).get("fencedDisconnect") is True)
        before = self.states()["receiver"]
        code, body = self.daemon_http("POST", "/disconnect", {"target": self.cc_target, "connectionId": "not-this-registration"})
        after = self.states()["receiver"]
        self.result["wrongFenceDisconnect"] = {"status": code, "body": body}
        self.check("wrong_fence_disconnect_refused", code == 400 and "different registration" in body)
        self.check("wrong_fence_leaves_registration_unchanged",
                   after.get("state") == "socket-ready" and all(after.get(k) == before.get(k) for k in ("id", "threadId", "offset", "accepted")))

    def close_codex_peer(self):
        # Stubs stand in for tmux and osascript so close can never reach a real terminal.
        stubs = self.root / "close-stubs"
        stubs.mkdir()
        for tool in ("tmux", "osascript"):
            stub = stubs / tool
            stub.write_text(f"#!/bin/sh\necho \"{tool} $*\" >> {shlex.quote(str(self.root / 'close-stub-calls.log'))}\nexit 1\n")
            stub.chmod(0o755)
        consumer, token = self.codex_witness()
        inbox = self.bus / self.fixture.channel / "codex" / "inbox.jsonl"
        inbox_before = inbox.read_bytes()
        env = {**self.fixture.env, "PATH": str(stubs) + os.pathsep + self.fixture.env.get("PATH", "")}
        self.check_guard("before-close")
        if self.start_token(consumer) != token or not self.owned_descendant(self.process.pid, consumer):
            raise RuntimeError("Codex consumer changed between the witness and the close; aborting without closing")
        self.result["codexCloseWitness"] = {"pid": consumer, "startToken": token, "ownedRoot": self.process.pid}
        run = subprocess.run([str(self.fixture.binary), "close", self.target], cwd=self.fixture.work, env=env, capture_output=True, text=True, timeout=30)
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            try:
                os.kill(consumer, 0)
            except ProcessLookupError:
                break
            self.tick_quiet()
        after = self.states()
        self.result["codexClose"] = {"exitCode": run.returncode, "stdout": run.stdout, "stderr": run.stderr, "consumerPID": consumer,
                                     "stubCalls": (self.root / "close-stub-calls.log").read_text() if (self.root / "close-stub-calls.log").exists() else ""}
        surfaces = ("surface already closed", "surface not swept (its terminal no longer exists)", "tty busy, surface left alone",
                    "surface left open (could not confirm idle)", "surface unknown (no tty)")
        self.check("codex_close_reports_ended_and_disconnected", run.returncode == 0
                   and run.stdout.startswith(self.target + ": process ended; connection disconnected, inbox retained; ")
                   and any(run.stdout.strip().endswith(surface) for surface in surfaces))
        try:
            os.kill(consumer, 0)
            gone = False
        except ProcessLookupError:
            gone = True
        self.check("codex_close_ended_the_journaled_consumer", gone)
        self.check("codex_close_disconnected_exact_registration", after["codex"].get("state") == "disconnected" and after["codex"].get("id") == self.result["connections"]["codex"]["id"])
        self.check("codex_close_kept_inbox", inbox.read_bytes() == inbox_before)
        self.check("codex_close_left_claude_and_daemon", after["receiver"].get("state") == "socket-ready" and self.cc_process.poll() is None
                   and self.fixture.health == json.loads(self.fixture.command(["daemon", "status", "--json"])))
        self.check("codex_close_never_reached_a_real_terminal", all(line.split()[0] in ("tmux", "osascript") for line in self.result["codexClose"]["stubCalls"].splitlines()))
        self.check_guard("after-close")
        self.check_offline_rows()

    def check_offline_rows(self):
        def offline(alias):
            listing = json.loads(self.fixture.command(["list", self.fixture.channel, "--json"]))
            peer = next(p for channel in listing["channels"] for p in channel["peers"] if p["alias"] == alias)
            row = next((line for line in self.fixture.command(["list", self.fixture.channel]).splitlines() if f"/{alias} " in line), "")
            return peer, row
        peer, row = offline("codex")
        self.result.setdefault("offlineRows", {})["codex"] = {"json": peer, "text": row}
        self.check("list_closed_codex_row_has_no_consumer_pid", "consumerPid" not in peer and "pid=? " in row)
        self.fixture.command(["connection", "disconnect", self.cc_target])
        peer, row = offline("receiver")
        self.result["offlineRows"]["receiver"] = {"json": peer, "text": row}
        self.check("list_disconnected_claude_row_has_no_consumer_pid", "consumerPid" not in peer and "pid=? " in row)

    def tick_quiet(self):
        # The closed Codex CLI exits on purpose here; keep draining the Claude PTY only.
        if self.cc_master is not None and select.select([self.cc_master], [], [], .1)[0]:
            try:
                self.cc_output.extend(os.read(self.cc_master, 65536))
            except OSError:
                pass

    def cleanup(self):
        errors, cleanup = [], {}
        for name, process in (("codex", self.process), ("claude", self.cc_process)):
            if process is not None:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=8)
                except ProcessLookupError:
                    pass
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=5)
                cleanup[name + "Exited"] = process.poll() is not None
        for master in (self.master, self.cc_master):
            if master is not None:
                os.close(master)
        for name, callback in (("codexProvider", self.provider.close if self.provider else None),
                               ("claudeProvider", self.cc_server.shutdown if self.cc_server else None)):
            if callback:
                try:
                    callback()
                    cleanup[name + "Stopped"] = True
                except Exception as error:
                    errors.append(name + ": " + str(error))
        cleanup.update(self.fixture.cleanup())
        if "connections" in self.result:
            native_pid = self.result["connections"]["codex"]["config"]["RuntimePID"]
            try:
                os.kill(native_pid, 0)
                cleanup["boundNativeCodexExited"] = False
            except ProcessLookupError:
                cleanup["boundNativeCodexExited"] = True
        self.result.update(cleanup=cleanup, cleanupErrors=errors, claudeProviderRequests=self.cc_requests,
                           providerRequests=[{k: v for k, v in r.items() if k != "release"} for r in self.provider.requests] if self.provider else [])
        (self.root / "terminal.log").write_bytes(self.cc_output)
        (self.root / "codex-terminal.log").write_bytes(self.output)
        (self.root / "provider-requests.json").write_text(json.dumps(self.cc_requests, indent=2))
        if "connections" in self.result:
            self.fixture.connection = self.result["connections"]["receiver"]
            self.fixture.result["mixedResult"] = self.result.copy()
            self.fixture.result["mixedResult"].pop("cbus")
            self.result["checks"]["native_secret_absent_from_evidence"] = self.fixture.token_absent_from_evidence()
            self.fixture.result.pop("mixedResult")
        self.result["passed"] = self.result["passed"] and not errors and all(cleanup.values()) and all(self.result["checks"].values())
        (self.root / "result.json").write_text(json.dumps(self.result, indent=2) + "\n")


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--cbus-sha256", required=True)
    parser.add_argument("--cbus-revision", required=True)
    parser.add_argument("--codex", required=True)
    parser.add_argument("--claude", required=True)
    parser.add_argument("--expected-codex-version", default="0.154.0", help="Exact installed and rollout version (default: %(default)s)")
    parser.add_argument("--expected-claude-version", default="2.1.277", help="Exact installed version (default: %(default)s)")
    parser.add_argument("--temp-root", default="/tmp")
    parser.add_argument("--require-init-reparented", action="store_true", help="Refuse to run unless this canary's parent was reparented to init")
    parser.add_argument("--guard-cbus", help="Installed cbus binary used only to read the guarded roster")
    parser.add_argument("--guard-daemon-pid", type=int, help="A live daemon that must be unchanged before and after")
    parser.add_argument("--guard-channel", help="A live channel whose roster must be unchanged before and after")
    return parser.parse_args(argv)


def main():
    args = parse_args()
    args.watcher_window, args.resume_selector = 11, "uuid"
    canary = MixedCanary(args)
    print("Mixed native artifacts: " + str(canary.root), flush=True)
    try:
        canary.run()
    except Exception as error:
        canary.result["error"] = str(error)
    finally:
        canary.cleanup()
    print(json.dumps({"passed": canary.result["passed"], "error": canary.result.get("error"), "checks": canary.result["checks"],
                      "expectedVersions": canary.result.get("expectedVersions"), "versions": canary.result.get("versions"),
                      "codexRolloutVersion": canary.result.get("codexRolloutVersion"), "result": str(canary.root / "result.json")}), flush=True)
    return 0 if canary.result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
