"""Scratch-only cbus fixture for the ordinary Claude PTY capability canary."""

import hashlib
import ctypes
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
import time
import uuid


def process_environment_keys(pid):
    """Read only an owned fixture process; return executable and names, never values."""
    if sys.platform != "darwin":
        executable = Path(f"/proc/{pid}/exe").resolve()
        environment = Path(f"/proc/{pid}/environ").read_bytes()
    else:
        libc = ctypes.CDLL(None, use_errno=True)
        mib, size = (ctypes.c_int * 3)(1, 49, pid), ctypes.c_size_t()
        if libc.sysctl(mib, 3, None, ctypes.byref(size), None, 0):
            raise OSError(ctypes.get_errno())
        buf = ctypes.create_string_buffer(size.value)
        if libc.sysctl(mib, 3, buf, ctypes.byref(size), None, 0):
            raise OSError(ctypes.get_errno())
        raw = buf.raw[:size.value]
        argc = int.from_bytes(raw[:4], sys.byteorder)
        end = raw.index(b"\0", 4)
        executable = Path(raw[4:end].decode())
        pos = end + 1
        while raw[pos] == 0:
            pos += 1
        for _ in range(argc):
            pos = raw.index(b"\0", pos) + 1
        environment = raw[pos:]
    parts = environment.lstrip(b"\0").split(b"\0")
    keys = set()
    for part in parts:
        if not part:
            break  # Darwin's following Apple vector is not the environment.
        if b"=" in part:
            keys.add(part.split(b"=", 1)[0].decode())
    return executable.resolve(), sorted(keys)


def matching_processes(binary):
    """Find only the frozen fixture executable; never inspect command arguments."""
    if sys.platform == "darwin":
        rows = subprocess.check_output(["ps", "-axo", "pid=,comm="], text=True)
        matches = []
        for row in rows.splitlines():
            parts = row.strip().split(None, 1)
            if len(parts) == 2 and parts[1] == str(binary):
                matches.append(int(parts[0]))
        return matches
    matches = []
    for path in Path("/proc").glob("[0-9]*/exe"):
        try:
            if path.resolve(strict=True) == binary:
                matches.append(int(path.parent.name))
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            pass
    return matches


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
        self.clear_connect_command = shlex.join([str(self.binary), "connect", self.channel, "receiver-new", "--json"])
        self.extra_connections = []
        self.reply_command = shlex.join([str(self.binary), "send", self.sender, "--force", self.ack])
        self.second_marker = "CBUS_SECOND_" + uuid.uuid4().hex
        self.second_ack = "CBUS_ACK_SECOND_" + uuid.uuid4().hex
        self.second_reply_command = shlex.join([str(self.binary), "send", self.sender, "--force", self.second_ack])
        self.inbox = self.bus / self.channel / "verifier" / "inbox.jsonl"
        self.process = None
        self.log = None
        self.autostart = getattr(args, "cbus_case", "") == "autostart"
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
        if self.autostart:
            self.assert_daemon_absent("before-verifier-join")
            (self.root / "daemon.log").write_text("CLI-autostarted daemon; see bus/.daemon/daemon.log.\n")
            self.command(["join", self.channel, "verifier", "--session-id", str(uuid.uuid4())])
            self.assert_daemon_absent("after-verifier-join")
            return
        elif self.shared:
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
        if self.autostart and not self.result.get("autostartObservedAfterBash"):
            raise RuntimeError("autostart was not bound to the completed Bash connect")
        states = json.loads(self.command(["connection", "status", self.target, "--json"]))
        if len(states) != 1:
            raise RuntimeError("expected one exact connection")
        return states[0]

    def assert_daemon_absent(self, phase):
        # The lock detects initialization before the control socket is published.
        paths = [self.bus / ".daemon" / name for name in ("control.sock", "lock")]
        present = [path.name for path in paths if os.path.lexists(path)]
        pids = matching_processes(self.binary.resolve())
        absent = not present and not pids
        self.result.setdefault("autostartAbsence", []).append(
            {"phase": phase, "at": time.time(), "absent": absent, "paths": present, "pids": pids})
        if not absent:
            raise RuntimeError("unexpected fixture daemon at " + phase)

    def before_bash_connect(self, tool_id):
        self.assert_daemon_absent("before-bash-connect-emission")
        self.result["autostartConnect"] = {"toolUseId": tool_id, "emittedAt": time.time()}

    def after_bash_connect(self, body):
        proof = self.result.get("autostartConnect")
        if not proof:
            raise RuntimeError("autostart connect was not emitted")
        blocks = [block for message in body.get("messages", [])
                  for block in message.get("content", []) if isinstance(block, dict)]
        replies = [block for block in blocks if block.get("type") == "tool_result"
                   and block.get("tool_use_id") == proof["toolUseId"]]
        if len(replies) != 1 or replies[0].get("is_error"):
            raise RuntimeError("expected successful exact Bash connect tool result")
        self.capture_autostart()
        proof.update(observedAt=time.time(), health=self.health,
                     socketDevice=self.autostart_socket[0], socketInode=self.autostart_socket[1],
                     executable=str(self.binary.resolve()))
        self.result["autostartObservedAfterBash"] = True

    def capture_autostart(self):
        socket = self.bus / ".daemon/control.sock"
        before = socket.lstat()
        health = json.loads(self.command(["daemon", "status", "--json"]))
        executable, keys = process_environment_keys(health["pid"])
        after = socket.lstat()
        if (not stat.S_ISSOCK(before.st_mode) or before.st_uid != os.geteuid()
                or (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino)
                or executable != self.binary.resolve()
                or health != json.loads(self.command(["daemon", "status", "--json"]))):
            raise RuntimeError("autostart daemon executable, socket or runtime identity changed")
        self.health = health
        self.autostart_socket = (before.st_dev, before.st_ino)
        self.result["daemonPID"] = health["pid"]
        self.result["daemonEnvironmentKeys"] = keys

    def acknowledgments(self):
        return [json.loads(line) for line in self.inbox.read_text().splitlines()
                if line and json.loads(line).get("text") == self.ack]

    def receipt_ready(self, minimum_accepted=1):
        if not hasattr(self, "connection"):
            return False
        path = self.bus / ".daemon/connections" / (self.connection["id"] + ".json")
        saved = json.loads(path.read_text())
        return saved.get("accepted", 0) >= minimum_accepted and saved.get("lastAccepted", {}).get("state") == "received"

    def restart(self):
        if self.shared or self.process is None:
            raise RuntimeError("restart requires an owned isolated daemon")
        before = json.loads(self.command(["daemon", "status", "--json"]))
        if before != self.health or before["pid"] != self.process.pid:
            raise RuntimeError("daemon restart identity fence changed")
        self.command(["daemon", "stop", "--json"])
        self.process.wait(timeout=10)
        if (self.bus / ".daemon/control.sock").exists():
            raise RuntimeError("old daemon socket remained after stop")
        self.process = subprocess.Popen([str(self.binary), "daemon", "serve"], cwd=self.work,
                                        env=self.env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 10
        while not (self.bus / ".daemon/control.sock").exists():
            if self.process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("restarted isolated daemon did not start")
            time.sleep(.05)
        self.health = json.loads(self.command(["daemon", "status", "--json"]))
        if self.health["pid"] != self.process.pid or self.health["pid"] == before["pid"]:
            raise RuntimeError("daemon restart did not produce expected fresh process")
        self.result["daemonRestart"] = {"before": before, "after": self.health}

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
        if (self.bus / ".daemon/daemon.log").exists():
            paths.append(self.bus / ".daemon/daemon.log")
        return bool(secret) and secret not in json.dumps(self.result).encode() and all(secret not in p.read_bytes() for p in paths)

    def cleanup(self):
        clean = {}
        if self.autostart:
            try:
                socket = self.bus / ".daemon/control.sock"
                observed = {pid for row in self.result.get("autostartAbsence", []) for pid in row["pids"]}
                observed.update(matching_processes(self.binary.resolve()))
                deadline = time.monotonic() + 10
                while not socket.exists() and observed:
                    for pid in list(observed):
                        try:
                            os.kill(pid, 0)
                        except ProcessLookupError:
                            observed.remove(pid)
                    if observed and time.monotonic() >= deadline:
                        raise RuntimeError("fixture process remains without a verifiable daemon socket")
                    if observed:
                        time.sleep(.05)
                if socket.exists():
                    if not hasattr(self, "health"):
                        self.capture_autostart()
                    st = socket.lstat()
                    if (st.st_dev, st.st_ino) != self.autostart_socket or json.loads(self.command(["daemon", "status", "--json"])) != self.health:
                        raise RuntimeError("autostart daemon cleanup identity fence changed")
                    if hasattr(self, "connection"):
                        self.command(["connection", "disconnect", self.target, "--json"])
                    self.command(["daemon", "stop", "--json"])
                    deadline = time.monotonic() + 10
                    while time.monotonic() < deadline:
                        try:
                            os.kill(self.health["pid"], 0)
                        except ProcessLookupError:
                            break
                        time.sleep(.05)
                    else:
                        raise RuntimeError("autostart daemon did not exit after stop")
                clean.update(daemonExited=True, daemonSocketRemoved=not socket.exists())
            except Exception as error:
                self.result["cleanupError"] = str(error)
                clean["daemonExited"] = False
            return clean
        if self.shared:
            try:
                if hasattr(self, "connection"):
                    for target in [*self.extra_connections, self.target]:
                        self.command(["connection", "disconnect", target, "--json"])
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
                        for target in [*self.extra_connections, self.target]:
                            self.command(["connection", "disconnect", target, "--json"])
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
