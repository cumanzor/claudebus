#!/usr/bin/env python3
"""Two ordinary native PTYs self-connect and exchange three real cbus messages.

Every model response comes from a local scripted fake provider. Scratch homes,
profiles and a frozen SHA-gated cbus binary are retained under --temp-root.
No observer connect, Monitor, paid inference or installed profile changes occur.
Traffic controls are environment restrictions, not an OS network sandbox.
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

    def run(self):
        self.prepare()
        self.result["versions"] = {"codex": subprocess.check_output([self.codex, "--version"], env=self.env, text=True).strip(),
                                   "claude": subprocess.check_output([self.args.claude, "--version"], env=self.cc_env, text=True).strip()}
        self.check("pinned_codex_0154", self.result["versions"]["codex"] == "codex-cli 0.154.0")
        self.check("pinned_claude_21277", self.result["versions"]["claude"].startswith("2.1.277 "))
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
        self.check("ordinary_codex_cli", meta.get("source") == "cli" and meta.get("cli_version") == "0.154.0")
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
        self.result["passed"] = True

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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--cbus-sha256", required=True)
    parser.add_argument("--cbus-revision", required=True)
    parser.add_argument("--codex", required=True)
    parser.add_argument("--claude", required=True)
    parser.add_argument("--temp-root", default="/tmp")
    args = parser.parse_args()
    args.watcher_window, args.resume_selector = 11, "uuid"
    canary = MixedCanary(args)
    print("Mixed native artifacts: " + str(canary.root), flush=True)
    try:
        canary.run()
    except Exception as error:
        canary.result["error"] = str(error)
    finally:
        canary.cleanup()
    print(json.dumps({"passed": canary.result["passed"], "error": canary.result.get("error"), "checks": canary.result["checks"], "result": str(canary.root / "result.json")}), flush=True)
    return 0 if canary.result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
