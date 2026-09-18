#!/usr/bin/env python3
"""Opt-in ordinary Codex CLI + real durable relay + deterministic local provider.

Build cbus and cbus-relay, then pass --cbus and --relay. All state, fake tokens,
HOME, CODEX_HOME and PATH helpers stay under one retained temporary directory.
No paid provider or existing user sessions/credentials are used. The fake model
issues actual Codex tool calls for connect and replies; this proves the tool and
transport path, not real-model understanding or normal sandbox authorization.
"""

import argparse
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import shlex
import signal
import socket
import struct
import subprocess
import termios
import threading
import time
import urllib.parse
import urllib.request
import uuid

from codex_native_queue_canary import Canary, executable
from codex_queue_lifecycle_canary import FakeProvider


class Tester:
    def __init__(self, port, token, artifact):
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=5)
        self.sock.settimeout(None)
        self.reader = self.sock.makefile("rb")
        self.lock = threading.Lock()
        self.messages, self.errors = [], []
        self.pings = 0
        self.closed = False
        key = base64.b64encode(os.urandom(16)).decode()
        path = "/tail/durable-v1?" + urllib.parse.urlencode(
            {"channel": "relay-native", "alias": "tester", "consumer": "isolated-tester"})
        self.sock.sendall((f"GET {path} HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n"
                           "Upgrade: websocket\r\nConnection: Upgrade\r\n"
                           f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
                           f"Sec-WebSocket-Protocol: bearer.cbus.{token}\r\n\r\n").encode())
        status = self.reader.readline().decode().strip()
        headers = {}
        while True:
            line = self.reader.readline().decode().strip()
            if not line:
                break
            name, value = line.split(":", 1)
            headers[name.lower()] = value.strip()
        accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
        if " 101 " not in status or headers.get("sec-websocket-accept") != accept:
            raise RuntimeError(f"tester WebSocket handshake failed: {status}")
        op, raw = self.read_frame()
        if op != 1 or json.loads(raw) != {"type": "ready", "protocol": "cbus-relay-durable/v1"}:
            raise RuntimeError("tester did not receive durable ready handshake")
        self.artifact = artifact.open("a")
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()

    def exact(self, n):
        data = self.reader.read(n)
        if len(data) != n:
            raise EOFError("tester socket ended")
        return data

    def read_frame(self):
        head = self.exact(2)
        if head[0] & 0x80 == 0 or head[1] & 0x80:
            raise RuntimeError("invalid server frame")
        count = head[1] & 0x7f
        if count == 126:
            count = struct.unpack("!H", self.exact(2))[0]
        elif count == 127:
            count = struct.unpack("!Q", self.exact(8))[0]
        if count > 2 << 20:
            raise RuntimeError("oversized server frame")
        return head[0] & 15, self.exact(count)

    def send(self, op, body):
        mask = os.urandom(4)
        size = len(body)
        header = bytes([0x80 | op])
        if size < 126:
            header += bytes([0x80 | size])
        elif size < 65536:
            header += bytes([0x80 | 126]) + struct.pack("!H", size)
        else:
            header += bytes([0x80 | 127]) + struct.pack("!Q", size)
        self.sock.sendall(header + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(body)))

    def run(self):
        seen = {}
        try:
            while True:
                op, raw = self.read_frame()
                if op == 9:
                    self.pings += 1
                    self.send(10, raw)
                    continue
                if op == 8:
                    return
                if op != 1:
                    continue
                frame = json.loads(raw)
                if frame.get("type") != "message":
                    raise RuntimeError(f"unexpected tester frame: {frame}")
                identity = frame["spoolId"]
                if identity in seen and seen[identity] != frame:
                    raise RuntimeError("relay reused spool ID with different bytes")
                if identity not in seen:
                    self.artifact.write(json.dumps(frame) + "\n")
                    self.artifact.flush()
                    os.fsync(self.artifact.fileno())
                    seen[identity] = frame
                    with self.lock:
                        self.messages.append(frame)
                self.send(1, json.dumps({"type": "ack", "spoolId": identity}).encode())
        except Exception as error:
            if not self.closed:
                self.errors.append(str(error))

    def count(self, *, text=None, event=None):
        with self.lock:
            return sum(1 for frame in self.messages if
                       (text is None or frame["message"].get("text") == text) and
                       (event is None or frame["message"].get("event") == event))

    def close(self):
        self.closed = True
        try:
            self.sock.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        self.sock.close()
        self.thread.join(timeout=3)
        self.reader.close()
        self.artifact.close()


class RelayCanary(Canary):
    def __init__(self, args):
        super().__init__(args)
        self.relay_binary = executable(args.relay)
        self.target = "relay-native@offline/advisor"
        self.nonces = [uuid.uuid4().hex, uuid.uuid4().hex]
        self.pings = ["CBUS_RELAY_PING_" + n for n in self.nonces]
        self.pongs = ["CBUS_RELAY_PONG_" + n for n in self.nonces]
        self.bootstrap_emitted = False
        self.replies_emitted = set()
        self.tool_commands = []
        self.provider = None
        self.relay = None
        self.tester = None
        self.release_stop = threading.Event()
        self.result.update(relayBinary=self.relay_binary,
                           relaySha256=hashlib.sha256(Path(self.relay_binary).read_bytes()).hexdigest(),
                           proofScope="ordinary CLI and actual tool execution; deterministic local fake model; isolated full-access scratch session")

    def model_output(self, number, body):
        command = None
        if not self.bootstrap_emitted:
            self.bootstrap_emitted = True
            command = [self.cbus, "connect", "relay-native@offline", "advisor", "--json"]
        else:
            user_text = "\n".join(part.get("text", "") for item in body.get("input", [])
                                  if item.get("type") == "message" and item.get("role") == "user"
                                  for part in item.get("content", []) if isinstance(part, dict))
            for i, marker in enumerate(self.pings):
                if marker in user_text and i not in self.replies_emitted:
                    self.replies_emitted.add(i)
                    # Omitting --from verifies the real session's remote marker.
                    command = [self.cbus, "send", "relay-native@offline/tester", self.pongs[i]]
                    break
        if command is None:
            return [{"type": "message", "id": f"msg-{number}", "role": "assistant", "status": "completed",
                     "content": [{"type": "output_text", "text": "OFFLINE_CANARY_READY", "annotations": []}]}]
        tools = {tool.get("name"): tool for tool in body.get("tools", []) if tool.get("type") == "function"}
        name = next((name for name in ("exec_command", "shell_command", "shell") if name in tools), None)
        if name is None:
            raise RuntimeError(f"no supported shell tool: {list(tools)}")
        properties = tools[name].get("parameters", {}).get("properties", {})
        key = "cmd" if "cmd" in properties else "command"
        value = shlex.join(command)
        if properties.get(key, {}).get("type") == "array":
            value = ["/bin/sh", "-c", value]
        arguments = {key: value}
        for key, value in (("workdir", str(self.work)), ("login", False), ("yield_time_ms", 30000), ("timeout_ms", 30000), ("max_output_tokens", 2000)):
            if key in properties:
                arguments[key] = value
        self.tool_commands.append({"number": number, "name": name, "arguments": arguments})
        return [{"type": "function_call", "id": f"fc-{number}", "call_id": f"call-{number}",
                 "name": name, "arguments": json.dumps(arguments)}]

    def prepare(self):
        user_home = self.root / "user-home"
        user_home.mkdir()
        self.env["HOME"] = str(user_home)
        self.env["XDG_CONFIG_HOME"] = str(user_home / ".config")
        for name in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
            self.env.pop(name, None)
        self.env["NO_PROXY"] = "127.0.0.1,localhost"
        token = "isolated-relay-token"
        creds = user_home / ".config" / "cbus" / "offline"
        creds.mkdir(parents=True)
        (creds / "token").write_text(token)
        (creds / "token").chmod(0o600)
        # macOS credential lookup is replaced only in this scratch PATH. The
        # real Keychain is neither read nor changed.
        security = self.root / "bin" / "security"
        security.write_text("#!/bin/sh\n" +
                            "if [ \"$*\" = \"find-generic-password -s cbus-relay-offline -a token -w\" ]; then\n" +
                            f"  printf '%s\\n' '{token}'\nelse\n  exit 1\nfi\n")
        security.chmod(0o700)
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        self.env.update(CBUS_RELAY_LOCAL_URL=base, CBUS_SITE_OFFLINE_URL=base)
        relay_env = self.env.copy()
        relay_env["CBUS_RELAY_TOKEN"] = token
        self.relay_log = (self.root / "relay.stderr").open("w")
        self.relay = subprocess.Popen([self.relay_binary, "-listen", f"127.0.0.1:{port}",
                                       "-spool", str(self.root / "relay-spool"), "-token-file", str(self.root / "unused")],
                                      env=relay_env, cwd=self.work, stdout=subprocess.DEVNULL,
                                      stderr=self.relay_log, start_new_session=True)
        deadline = time.monotonic() + 10
        while True:
            try:
                with urllib.request.urlopen(base + "/healthz", timeout=.3) as reply:
                    if reply.read().strip() == b"ok":
                        break
            except Exception:
                if time.monotonic() > deadline:
                    raise TimeoutError("isolated relay did not start")
                time.sleep(.05)
        self.tester = Tester(port, token, self.root / "tester-inbox.jsonl")
        self.provider = FakeProvider(output=self.model_output)
        def release_requests():
            while not self.release_stop.wait(.02):
                with self.provider.condition:
                    for request in self.provider.requests:
                        request["release"].set()
        self.release_thread = threading.Thread(target=release_requests, daemon=True)
        self.release_thread.start()
        (self.home / "config.toml").write_text(
            'model = "cbus-relay-cli-probe"\nmodel_provider = "cbus_local_mock"\n'
            'approval_policy = "never"\nsandbox_mode = "danger-full-access"\n'
            'check_for_update_on_startup = false\n'
            '[model_providers.cbus_local_mock]\nname = "Local deterministic test provider"\n'
            f'base_url = "http://127.0.0.1:{self.provider.server.server_port}/v1"\n'
            'wire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false\n'
            'request_max_retries = 0\nstream_max_retries = 0\n'
            '[analytics]\nenabled = false\n[features]\nunbounded_connection_retries = false\n'
            'enable_request_compression = false\nshell_snapshot = false\n'
            f'[projects.{json.dumps(str(self.work))}]\ntrust_level = "trusted"\n')

    def run(self):
        self.prepare()
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        os.set_blocking(self.master, False)
        try:
            self.process = subprocess.Popen([self.codex, "--no-alt-screen", "-C", str(self.work),
                                             "Run the isolated relay canary bootstrap tool, then wait."],
                                            stdin=slave, stdout=slave, stderr=slave, env=self.env,
                                            cwd=self.work, start_new_session=True)
        finally:
            os.close(slave)
        self.result["tuiPid"] = self.process.pid
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "CLI transcript")
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(e.get("type") == "session_meta" for e in self.entries()), "CLI metadata")
        meta = next(e["payload"] for e in self.entries() if e.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"), codexVersion=meta.get("cli_version"),
                           rollout=str(self.rollout))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        self.wait(lambda: self.tester.count(event="join") == 1, "actual CLI bootstrap + remote consumer join")
        self.wait(lambda: any(e.get("payload", {}).get("type") == "task_complete" for e in self.entries()), "bootstrap completion")
        first = self.status()
        self.check("bootstrap_from_real_cli_tool", bool(self.tool_commands) and first["threadId"] == self.thread)
        self.check("actual_consumer_online", first.get("consumer", {}).get("state") == "online")
        self.command(["send", self.target, "--from", "relay-native@offline/tester", self.pings[0]])
        self.wait(lambda: self.tester.count(text=self.pongs[0]) == 1, "first model-tool PONG")
        self.wait(lambda: self.count(self.pings[0]) == 1, "first queue input in same CLI")
        self.pump(2)
        idle_requests = self.provider.count()
        self.pump(32)
        self.check("idle_keepalive_no_model_requests", self.provider.count() == idle_requests and self.tester.pings >= 1)
        before = self.status()
        daemon_before = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.stop_daemon()
        self.command(["send", self.target, "--from", "relay-native@offline/tester", self.pings[1]])
        self.pump(5)
        self.check("offline_message_stays_on_relay", self.count(self.pings[1]) == 0 and self.provider.count() == idle_requests)
        self.command(["daemon", "start", "--json"])
        self.wait(lambda: self.tester.count(text=self.pongs[1]) == 1, "PONG after daemon restart")
        self.wait(lambda: self.count(self.pings[1]) == 1, "second queue input in same CLI")
        self.pump(12)
        after = self.status()
        self.check("same_epoch_after_restart", after["id"] == first["id"] and after["accepted"] == 2 and after["offset"] > before["offset"])
        self.check("no_duplicate_consumption", all(self.count(marker) == 1 for marker in self.pings))
        self.check("no_duplicate_reply", all(self.tester.count(text=marker) == 1 for marker in self.pongs))
        self.check("transport_reconnect_no_false_consumer_join", self.tester.count(event="join") == 1)
        replies = [frame["message"] for frame in self.tester.messages if frame["message"].get("text") in self.pongs]
        self.check("default_remote_reply_identity", all(reply.get("from") == self.target for reply in replies))
        self.command(["connection", "disconnect", self.target, "--json"])
        self.wait(lambda: self.tester.count(event="leave") == 1, "durable explicit departure")
        self.wait(lambda: not self.status().get("presenceOutbox"), "departure ACK journal")
        self.check("original_cli_process_preserved", self.process.poll() is None)
        self.check("tester_no_protocol_error", not self.tester.errors)
        self.result.update(initial=first, beforeRestart=before, afterRestart=after,
                           daemonBefore=daemon_before, daemonAfter=json.loads(self.command(["daemon", "status", "--json"]).stdout),
                           pings=self.pings, pongs=self.pongs, providerRequests=self.provider.count(),
                           testerPings=self.tester.pings, passed=True)

    def cleanup(self):
        super().cleanup()
        self.release_stop.set()
        if self.provider:
            self.provider.close()
            records = [{key: value for key, value in request.items() if key != "release"}
                       for request in self.provider.requests]
            (self.root / "provider-requests.json").write_text(json.dumps(records, indent=2) + "\n")
        if self.tester:
            self.tester.close()
        if self.relay:
            self.relay.terminate()
            try:
                self.relay.wait(timeout=3)
            except subprocess.TimeoutExpired:
                self.relay.kill()
                self.relay.wait(timeout=3)
            self.relay_log.close()
        self.result["toolCommands"] = self.tool_commands
        self.result["scratchProcessesStopped"] = ((self.process is None or self.process.poll() is not None)
                                                   and (self.relay is None or self.relay.poll() is not None))
        self.result["cleanup"] = {
            "cliExited": self.process is None or self.process.poll() is not None,
            "relayExited": self.relay is None or self.relay.poll() is not None,
            "providerExited": self.provider is None or not self.provider.thread.is_alive(),
            "testerExited": self.tester is None or not self.tester.thread.is_alive(),
            "cbusSocketRemoved": not (self.bus / ".daemon" / "control.sock").exists(),
        }
        self.result["passed"] = self.result["passed"] and all(self.result["cleanup"].values())
        (self.root / "result.json").write_text(json.dumps(self.result, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--relay", required=True)
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default=os.getenv("CBUS_TEST_TMPDIR", "/tmp"))
    canary = RelayCanary(parser.parse_args())
    print(f"Canary artifacts: {canary.root}", flush=True)
    try:
        canary.run()
    except Exception as error:
        canary.result["error"] = str(error)
    finally:
        canary.cleanup()
    print(json.dumps(canary.result, indent=2), flush=True)
    return 0 if canary.result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
