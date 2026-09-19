"""Scratch-only cbus fixture for the ordinary Claude PTY capability canary."""

import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import time
import uuid


class BusProbe:
    def __init__(self, root, args, session, marker):
        self.root, self.session, self.marker = root, session, marker
        source = Path(args.cbus).resolve(strict=True)
        self.binary = root / "cbus"
        shutil.copy2(source, self.binary)
        actual = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        if actual != args.cbus_sha256:
            raise ValueError("cbus candidate does not match the supplied SHA-256")
        self.bus = root / "bus"
        self.shared = None
        if getattr(args, "cbus_shared_fixture", None):
            fixture = Path(args.cbus_shared_fixture).resolve(strict=True)
            info = fixture.stat()
            self.shared = json.loads(fixture.read_text())
            self.bus = Path(self.shared["busRoot"])
            if (info.st_uid != os.geteuid() or info.st_mode & 0o077
                    or self.shared.get("kind") != "claude-cbus-isolated-pair"
                    or self.shared.get("binarySHA256") != actual
                    or self.bus.resolve().parent != fixture.parent):
                raise ValueError("invalid private shared cbus fixture")
            os.kill(self.shared["ownerPID"], 0)
        else:
            self.bus.mkdir(mode=0o700)
        self.channel = "cc-canary-" + uuid.uuid4().hex[:12]
        self.target, self.sender = self.channel + "/receiver", self.channel + "/verifier"
        self.ack = "CBUS_ACK_" + uuid.uuid4().hex
        self.connect_command = shlex.join([str(self.binary), "connect", self.channel, "receiver", "--json"])
        self.reply_command = shlex.join([str(self.binary), "send", self.sender, "--force", self.ack])
        self.inbox = self.bus / self.channel / "verifier" / "inbox.jsonl"
        self.process = None
        self.log = None
        self.result = {"sourceBinary": str(source), "frozenBinary": str(self.binary),
                       "sha256": actual, "declaredSourceRevision": args.cbus_revision,
                       "channel": self.channel, "target": self.target, "sender": self.sender,
                       "replyMarker": self.ack, "commands": []}

    def command(self, args):
        run = subprocess.run([str(self.binary), *args], cwd=self.work, env=self.env,
                             capture_output=True, text=True, timeout=15)
        self.result["commands"].append({"args": args, "exitCode": run.returncode,
                                         "stdout": run.stdout, "stderr": run.stderr})
        if run.returncode:
            raise RuntimeError("cbus command failed: " + run.stderr.strip())
        return run.stdout

    def start(self, env, work):
        self.env, self.work = env.copy(), work
        self.result["version"] = self.command(["--version"]).strip()
        if self.shared:
            self.health = json.loads(self.command(["daemon", "status", "--json"]))
            if self.health != self.shared["health"]:
                raise RuntimeError("shared fixture daemon identity changed")
            (self.root / "daemon.log").write_text("Shared isolated fixture daemon; see parent result.\n")
        else:
            self.log = (self.root / "daemon.log").open("wb")
            self.process = subprocess.Popen([str(self.binary), "daemon", "serve"], cwd=work,
                                            env=env, stdout=self.log, stderr=self.log)
            deadline = time.monotonic() + 10
            while not (self.bus / ".daemon/control.sock").exists():
                if self.process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("isolated daemon did not start")
                time.sleep(.05)
            self.health = json.loads(self.command(["daemon", "status", "--json"]))
            if self.health["pid"] != self.process.pid:
                raise RuntimeError("isolated daemon PID did not match launched process")
        self.result["daemonPID"] = self.health["pid"]
        self.command(["join", self.channel, "verifier", "--session-id", str(uuid.uuid4())])

    def status(self):
        states = json.loads(self.command(["connection", "status", self.target, "--json"]))
        if len(states) != 1:
            raise RuntimeError("expected one exact connection")
        return states[0]

    def acknowledgments(self):
        return [json.loads(line) for line in self.inbox.read_text().splitlines()
                if line and json.loads(line).get("text") == self.ack]

    def receipt_ready(self):
        if not hasattr(self, "connection"):
            return False
        path = self.bus / ".daemon/connections" / (self.connection["id"] + ".json")
        saved = json.loads(path.read_text())
        return saved.get("lastAccepted", {}).get("state") == "received"

    def check_receipt(self, records, pid):
        current = self.status()
        self.result["received"] = current
        receipt = current.get("lastAccepted", {})
        receipt_uuid = receipt.get("itemId")
        rows = [r for r in records if r.get("type") == "user" and r.get("sessionId") == self.session
                and r.get("uuid") == receipt_uuid and self.marker in json.dumps(r)]
        acks = self.acknowledgments()
        self.result["acknowledgments"] = acks
        if rows:
            (self.root / "cbus-receipt-user-row.json").write_text(json.dumps(rows[0], indent=2) + "\n")
        binding = current.get("claude", {}).get("binding", {})
        return {
            "cbus_harness_and_exact_session": current.get("harness") == "claude" and current.get("threadId") == self.session and binding.get("SessionID") == self.session,
            "cbus_bound_original_runtime": binding.get("Endpoint", {}).get("PID") == pid,
            "cbus_socket_ready": current.get("state") == "socket-ready",
            "cbus_received_requires_exact_UUID_transcript": receipt.get("state") == "received" and len(rows) == 1,
            "cbus_round_trip_ack_exact_sender": len(acks) == 1 and acks[0].get("from") == self.target and acks[0].get("to") == self.sender,
            "cbus_no_observer_connect": not any(r["args"][0] == "connect" for r in self.result["commands"]),
        }

    def token_absent_from_evidence(self):
        reference = self.connection["claude"]["credentialRef"]
        if not re.fullmatch(r"[0-9a-f-]{36}\.token", reference):
            return False
        secret_path = self.bus / ".daemon/claude-credentials" / reference
        secret = secret_path.read_bytes()
        paths = [self.root / name for name in ("terminal.log", "debug.log", "provider-requests.json", "daemon.log")]
        paths += list((self.bus / ".daemon/connections").glob("*.json"))
        return bool(secret) and secret not in json.dumps(self.result).encode() and all(secret not in p.read_bytes() for p in paths)

    def cleanup(self):
        clean = {}
        if self.shared:
            try:
                if hasattr(self, "connection"):
                    self.command(["connection", "disconnect", self.target, "--json"])
                clean["sharedDaemonPreserved"] = json.loads(self.command(["daemon", "status", "--json"])) == self.health
            except Exception as error:
                self.result["cleanupError"] = str(error)
                clean["sharedDaemonPreserved"] = False
            return clean
        if self.process is not None:
            try:
                if self.process.poll() is None:
                    health = json.loads(self.command(["daemon", "status", "--json"]))
                    if health != self.health or health["pid"] != self.process.pid:
                        raise RuntimeError("daemon health fence changed")
                    if hasattr(self, "connection"):
                        self.command(["connection", "disconnect", self.target, "--json"])
                    self.command(["daemon", "stop", "--json"])
                    self.process.wait(timeout=10)
            except Exception as error:
                self.result["cleanupError"] = str(error)
                if self.process.poll() is None:
                    self.process.terminate()
                    try:
                        self.process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        self.process.kill()
                        self.process.wait(timeout=5)
            clean["daemonExited"] = self.process.poll() is not None
            clean["daemonSocketRemoved"] = not (self.bus / ".daemon/control.sock").exists()
        if self.log is not None:
            self.log.close()
        return clean
