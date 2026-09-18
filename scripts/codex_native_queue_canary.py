#!/usr/bin/env python3
"""Opt-in, offline integration check for the ordinary Codex CLI connection path.

Build cbus first, then run on macOS/Linux with local Unix-socket access:
    go build -o /tmp/cbus-native-pilot ./cmd/cbus
    CBUS_TEST_BINARY=/tmp/cbus-native-pilot python3 scripts/codex_native_queue_canary.py

CODEX_TEST_BINARY may select a specific Codex executable; otherwise PATH is used.
CBUS_TEST_TMPDIR / --temp-root selects the parent for a fresh, retained artifact
directory. This script never uses the user's Codex or cbus state directories.

The provider is an intentionally unavailable localhost endpoint. No paid model
request or agent reply is expected. The script simulates the skill's shell call
using the exact UUID read from the newly launched CLI's transcript. Success means
the original CLI consumed the bus frames without restarting, including across a
cbus daemon restart; it does not establish that a model executed the skill.
"""

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import uuid


def executable(value):
    found = shutil.which(value) if value else None
    if not found:
        raise ValueError(f"executable not found: {value!r}")
    return str(Path(found).resolve())


class Canary:
    def __init__(self, args):
        self.cbus = executable(args.cbus)
        self.codex = executable(args.codex)
        self.root = Path(tempfile.mkdtemp(prefix="cbus-cli-canary-", dir=args.temp_root)).resolve()
        self.home, self.bus, self.work = (self.root / p for p in ("codex", "bus", "work"))
        self.home.mkdir()
        self.work.mkdir()
        bindir = self.root / "bin"
        bindir.mkdir()
        # cbus identity discovery must select the same executable as this TUI.
        (bindir / "codex").symlink_to(self.codex)
        self.env = os.environ.copy()
        for name in (
            "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN",
            "CODEX_EXEC_SERVER_URL", "CODEX_THREAD_ID", "CODEX_SESSION_ID",
            "CBUS_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "GROK_SESSION_ID",
            "CBUS_CHANNEL", "CBUS_ALIAS",
        ):
            self.env.pop(name, None)
        self.env.update(
            CODEX_HOME=str(self.home), CBUS_DIR=str(self.bus), TERM="xterm-256color",
            PATH=str(bindir) + os.pathsep + self.env.get("PATH", ""),
        )
        (self.home / "config.toml").write_text(
            'model = "cbus-offline-probe"\nmodel_provider = "cbus_offline"\n'
            '[model_providers.cbus_offline]\nname = "CBUS Offline Probe"\n'
            'base_url = "http://127.0.0.1:9/v1"\nwire_api = "responses"\n'
            'requires_openai_auth = false\nrequest_max_retries = 0\nstream_max_retries = 0\n'
            '[analytics]\nenabled = false\n[features]\n'
            'unbounded_connection_retries = false\nshell_snapshot = false\n'
            f'[projects.{json.dumps(str(self.work))}]\ntrust_level = "trusted"\n'
        )
        self.result = {"root": str(self.root), "cbusBinary": self.cbus, "codexBinary": self.codex,
                       "cbusSha256": hashlib.sha256(Path(self.cbus).read_bytes()).hexdigest(),
                       "checks": {}, "commands": [], "passed": False}
        self.output = bytearray()
        self.process = None
        self.master = None
        self.rollout = None
        self.thread = None
        self.target = "native-canary/advisor"

    def command(self, args, recipient=False, allowed=(0,)):
        env = self.env.copy()
        if recipient:
            env["CODEX_THREAD_ID"] = self.thread
        run = subprocess.run([self.cbus, *args], env=env, cwd=self.work,
                             capture_output=True, text=True, timeout=30)
        record = {"args": args, "code": run.returncode, "stdout": run.stdout, "stderr": run.stderr}
        self.result["commands"].append(record)
        if run.returncode not in allowed:
            raise RuntimeError(f"cbus {' '.join(args)} failed: {run.stderr.strip()}")
        return run

    def pump(self, seconds):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            ready, _, _ = select.select([self.master], [], [], min(.1, max(0, deadline - time.monotonic())))
            if not ready:
                continue
            try:
                data = os.read(self.master, 65536)
            except OSError:
                return
            if not data:
                return
            self.output.extend(data)
            # Supply the terminal queries Codex emits during TUI initialization.
            for query, answer in ((b"\x1b[6n", b"\x1b[1;1R"),
                                  (b"\x1b[c", b"\x1b[?1;2c"),
                                  (b"\x1b[?u", b"\x1b[?0u")):
                if query in data:
                    os.write(self.master, answer)

    def wait(self, predicate, description, timeout=35):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if predicate():
                return
            if self.process.poll() is not None:
                raise RuntimeError(f"original CLI exited while waiting for {description}")
            self.pump(.2)
        raise TimeoutError(f"timed out waiting for {description}")

    def entries(self):
        if self.rollout is None:
            return []
        entries = []
        for line in self.rollout.read_text().splitlines():
            try:
                entries.append(json.loads(line))
            except json.JSONDecodeError:
                pass  # A trailing record can still be in flight.
        return entries

    def count(self, marker):
        # Count canonical input messages, not every event/transcript rendering.
        return sum(1 for record in self.entries()
                   if record.get("type") == "response_item"
                   and record.get("payload", {}).get("type") == "message"
                   and record["payload"].get("role") == "user"
                   and any(marker in item.get("text", "")
                           for item in record["payload"].get("content", [])))

    def status(self):
        states = json.loads(self.command(["connection", "status", self.target, "--json"]).stdout)
        if len(states) != 1:
            raise AssertionError(f"expected one connection, got {len(states)}")
        return states[0]

    def connect(self):
        return json.loads(self.command(["connect", "native-canary", "advisor", "--codex-sqlite-home", str(self.home), "--json"], recipient=True).stdout)

    def stop_daemon(self):
        socket = self.bus / ".daemon" / "control.sock"
        if not socket.exists():
            return
        self.command(["daemon", "stop"])
        deadline = time.monotonic() + 5
        while socket.exists() and time.monotonic() < deadline:
            time.sleep(.05)
        if socket.exists():
            raise RuntimeError("scratch daemon socket remained after stop")

    def check(self, name, condition):
        self.result["checks"][name] = bool(condition)
        if not condition:
            raise AssertionError(name)

    def run(self):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        os.set_blocking(self.master, False)
        try:
            self.process = subprocess.Popen(
                [self.codex, "--no-alt-screen", "-C", str(self.work),
                 "CBUS offline initial turn to persist CLI thread"],
                stdin=slave, stdout=slave, stderr=slave, env=self.env,
                cwd=self.work, start_new_session=True,
            )
        finally:
            os.close(slave)
        self.result["tuiPid"] = self.process.pid
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "initial CLI transcript", 15)
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(e.get("type") == "session_meta" for e in self.entries()), "CLI identity")
        meta = next(e["payload"] for e in self.entries() if e.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"),
                           codexVersion=meta.get("cli_version"), rollout=str(self.rollout))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        self.wait(lambda: any(e.get("payload", {}).get("type") == "task_complete"
                              for e in self.entries()), "offline initial turn completion")

        first = self.connect()
        daemon_before = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        marker_a = "CBUS-NATIVE-A-" + uuid.uuid4().hex
        marker_b = "CBUS-NATIVE-B-" + uuid.uuid4().hex
        self.result["markers"] = [marker_a, marker_b]
        self.command(["send", self.target, "--from", "native-canary/tester", marker_a])
        self.wait(lambda: self.count(marker_a) == 1, "first frame in original CLI")
        baseline = self.status()
        self.check("first_frame_accepted", baseline["accepted"] == 1 and baseline["offset"] > 0)
        repeat = self.connect()
        self.check("repeat_connect_preserves_id_and_offset",
                   repeat["id"] == first["id"] and repeat["offset"] == baseline["offset"])

        self.stop_daemon()
        checkpoint = json.loads((self.bus / ".daemon" / "connections" / (first["id"] + ".json")).read_text())
        self.check("stop_preserves_checkpoint", checkpoint["offset"] == baseline["offset"])
        self.command(["send", self.target, "--from", "native-canary/tester", "--force", marker_b])
        self.pump(11)
        self.check("downtime_frame_waits_for_daemon", self.count(marker_b) == 0)
        checkpoint_after = json.loads((self.bus / ".daemon" / "connections" / (first["id"] + ".json")).read_text())
        self.check("downtime_preserves_id_offset_and_count",
                   checkpoint_after["id"] == first["id"]
                   and checkpoint_after["offset"] == baseline["offset"]
                   and checkpoint_after["accepted"] == 1)

        daemon_after = json.loads(self.command(["daemon", "start", "--json"]).stdout)
        self.check("daemon_process_restarted", daemon_before["pid"] != daemon_after["pid"])
        resumed = self.connect()
        self.check("restart_reconnect_preserves_registration",
                   resumed["id"] == first["id"] and resumed["offset"] >= baseline["offset"])
        self.wait(lambda: self.count(marker_b) == 1, "downtime frame in original CLI after daemon restart")
        delivered = self.status()
        self.check("restart_delivers_one_pending_frame",
                   delivered["accepted"] == 2 and delivered["offset"] > baseline["offset"])
        for _ in range(2):
            again = self.connect()
            self.check("repeated_reconnect_preserves_delivered_offset",
                       again["id"] == first["id"] and again["offset"] == delivered["offset"])
        self.pump(12)
        final = self.status()
        self.result["messageCounts"] = {marker_a: self.count(marker_a), marker_b: self.count(marker_b)}
        self.check("no_duplicate_messages", all(n == 1 for n in self.result["messageCounts"].values()))
        self.check("no_duplicate_enqueue", final["accepted"] == 2 and final["offset"] == delivered["offset"])
        self.check("original_cli_process_preserved", self.process.poll() is None)
        self.result.update(initialConnection=first, beforeRestart=baseline, afterRestart=final,
                           daemonBefore=daemon_before, daemonAfter=daemon_after, passed=True)

    def cleanup(self):
        try:
            self.stop_daemon()
        except Exception as error:
            self.result["cleanupError"] = str(error)
            self.result["passed"] = False
        if self.process is not None:
            try:
                os.killpg(self.process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                self.process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=3)
        if self.master is not None:
            os.close(self.master)
        (self.root / "tui-output.txt").write_bytes(self.output)
        (self.root / "result.json").write_text(json.dumps(self.result, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), help="built cbus candidate (required)")
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"), help="installed Codex CLI")
    parser.add_argument("--temp-root", default=os.getenv("CBUS_TEST_TMPDIR", "/tmp"), help="parent for retained artifacts")
    args = parser.parse_args()
    if not args.cbus:
        parser.error("set CBUS_TEST_BINARY or --cbus to the built candidate")
    canary = Canary(args)
    print(f"Canary artifacts: {canary.root}", flush=True)
    try:
        canary.run()
    except Exception as error:
        canary.result["error"] = str(error)
    finally:
        canary.cleanup()
    print(json.dumps({"passed": canary.result["passed"], "checks": canary.result["checks"],
                      "error": canary.result.get("error"), "result": str(canary.root / "result.json")}), flush=True)
    return 0 if canary.result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
