#!/usr/bin/env python3
"""Actual automatic CLI connect across two homes/profile/SQLite configurations.

Only scratch HOME/CODEX_HOME/rules and localhost fake providers are used. Each
ordinary CLI runs cbus connect itself, with its injected thread ID and no explicit
SQLite-home fallback. An exact scratch connect rule permits process inspection
and local socket bootstrap. A shared daemon must retain each caller's real open
queue store, including a per-launch -c override, across daemon restart.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import shlex
import struct
import subprocess
import sys
import termios
import time
import uuid

from codex_cli_resume_canary import ResumeCanary, process_descendants
from codex_queue_lifecycle_canary import FakeProvider


class Peer(ResumeCanary):
    def __init__(self, args, label, bus=None):
        super().__init__(args)
        self.label = label
        self.target = "multi-home/" + label
        self.sqlite = self.root / "runtime-sqlite"
        self.sqlite.mkdir()
        self.config_sqlite = self.root / "unselected-sqlite"
        self.config_sqlite.mkdir(exist_ok=True)
        self.requested = False
        if bus is not None:
            self.bus = bus
            self.env["CBUS_DIR"] = str(bus)
        self.result.update(proofLayer="ordinary CLI automatic shell connect with runtime FD store binding; local fake provider",
                           script="scripts/codex_multihome_canary.py", label=label)

    def prepare(self):
        super().prepare()
        self.provider.close()
        self.provider = FakeProvider(output=self.provider_output)
        config = (self.home / "config.toml").read_text()
        config = re.sub(r'http://127\.0\.0\.1:\d+/v1', f'http://127.0.0.1:{self.provider.server.server_port}/v1', config)
        config = config.replace('approval_policy = "never"', 'approval_policy = "on-request"')
        config = config.replace('sandbox_mode = "read-only"', 'sandbox_mode = "workspace-write"')
        config = 'sqlite_home = ' + json.dumps(str(self.config_sqlite)) + '\n' + config
        (self.home / "config.toml").write_text(config)
        profile = config.replace('model = "cbus-cli-resume-probe"', 'model = "cbus-selected-profile-' + self.label + '"')
        if self.label == "profile":
            profile = profile.replace(str(self.config_sqlite), str(self.sqlite))
        (self.home / "isolated.config.toml").write_text(profile)
        rules = self.home / "rules"
        rules.mkdir()
        (rules / "bootstrap.rules").write_text('prefix_rule(pattern = ' + json.dumps([self.cbus, "connect", "multi-home", self.label, "--json"]) + ', decision = "allow", justification = "Isolated automatic connect acceptance")\n')
        self.result["bootstrapRule"] = (rules / "bootstrap.rules").read_text()

    def provider_output(self, number, body):
        if self.requested or body.get("client_metadata", {}).get("thread_id") != self.thread:
            return []
        self.requested = True
        command = shlex.join([self.cbus, "connect", "multi-home", self.label, "--json"])
        names = {tool.get("name") for tool in body.get("tools", [])}
        if "exec_command" in names:
            name, arguments = "exec_command", {"cmd": command, "yield_time_ms": 1000, "max_output_tokens": 3000}
        elif "shell_command" in names:
            name, arguments = "shell_command", {"command": command}
        else:
            raise AssertionError("ordinary CLI shell tool unavailable")
        self.result["requestedTool"] = {"name": name, "arguments": arguments}
        return [{"type": "function_call", "id": f"connect-item-{number}", "call_id": f"connect-call-{number}", "name": name, "arguments": json.dumps(arguments)}]

    def start(self):
        self.prepare()
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        os.set_blocking(self.master, False)
        args = [self.codex, "--no-alt-screen", "-C", str(self.work), "--profile", "isolated"]
        if self.label == "override":
            args.extend(["-c", "sqlite_home=" + json.dumps(str(self.sqlite))])
        args.append("Local automatic cbus runtime binding acceptance " + self.label)
        try:
            self.process = subprocess.Popen(args, stdin=slave, stdout=slave, stderr=slave, env=self.env, cwd=self.work, start_new_session=True)
        finally:
            os.close(slave)
        self.clis.append(self.process)
        self.result["cliLaunches"] = [{"args": args, "pid": self.process.pid}]
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "initial transcript")
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(row.get("type") == "session_meta" for row in self.entries()), "initial identity")
        meta = next(row["payload"] for row in self.entries() if row.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"), codexVersion=meta.get("cli_version"), rollout=str(self.rollout))
        self.check("ordinary_cli_source", meta.get("source") == "cli")

    def release(self):
        with self.provider.condition:
            for request in self.provider.requests:
                request["release"].set()

    def connected(self):
        if self.completed_count() < 1:
            return False
        path = self.bus / "multi-home" / self.label / "meta.json"
        return path.exists() and json.loads(path.read_text()).get("sessionId") == self.thread

    def verify_binding(self):
        state = self.status()
        config = state["config"]
        tree = process_descendants(self.process.pid)
        self.result["resumedProcessTree"] = tree
        self.check("runtime_ancestor_proved_automatic_binding", config["BindingSource"] == "runtime-open-queue"
                   and config["RuntimePID"] in [row["pid"] for row in tree] and bool(config["RuntimeStartToken"]))
        self.check("per_session_homes_preserved", config["Home"] == str(self.home) and config["UserHome"] == self.env["HOME"] and config["Cwd"] == str(self.work))
        self.check("real_sqlite_override_preserved", config["SQLiteHome"] == str(self.sqlite) and (self.sqlite / "queue_1.sqlite").exists())
        self.check("unselected_config_store_is_not_used", not (self.config_sqlite / "queue_1.sqlite").exists())
        self.check("no_explicit_store_fallback_in_tool", "--codex-sqlite-home" not in json.dumps(self.result["requestedTool"]))
        contexts = [row["payload"] for row in self.entries() if row.get("type") == "turn_context"]
        self.check("selected_profile_applies_to_actual_cli", bool(contexts) and all(row.get("model") == "cbus-selected-profile-" + self.label for row in contexts))
        self.check("bootstrap_keeps_approval_and_sandbox_policy", all(row.get("approval_policy") == "on-request" and row.get("sandbox_policy", {}).get("type") == "workspace-write" for row in contexts))
        self.result["binding"] = state
        return state


def pump(peers, seconds):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        for peer in peers:
            peer.release()
            peer.pump(.05)


def wait(peers, predicate, description, timeout=40):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if predicate():
            return
        if any(peer.process.poll() is not None for peer in peers):
            raise RuntimeError("CLI exited during " + description)
        pump(peers, .15)
    raise TimeoutError(description)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default="/tmp")
    args = parser.parse_args()
    args.watcher_window = 11
    peers = [Peer(args, "profile")]
    peers.append(Peer(args, "override", peers[0].bus))
    result = {"passed": False, "roots": [str(peer.root) for peer in peers], "checks": {}}
    def check(name, condition):
        result["checks"][name] = bool(condition)
        if not condition:
            raise AssertionError(name)
    print("Multi-home artifacts: " + str(peers[0].root), flush=True)
    try:
        for peer in peers:
            peer.start()
            wait(peers[:peers.index(peer)+1], peer.connected, peer.label + " automatic shell connect")
            peer.verify_binding()
        daemon = json.loads(peers[0].command(["daemon", "status", "--json"]).stdout)
        check("two_distinct_threads_and_runtime_pids", peers[0].thread != peers[1].thread and peers[0].result["binding"]["config"]["RuntimePID"] != peers[1].result["binding"]["config"]["RuntimePID"])
        check("both_registrations_share_one_daemon", json.loads(peers[1].command(["daemon", "status", "--json"]).stdout)["pid"] == daemon["pid"])
        markers = []
        for phase in ("before-restart", "after-restart"):
            if phase == "after-restart":
                restarted = json.loads(peers[0].command(["daemon", "restart", "--json"]).stdout)
                check("listeners_ready_when_restart_returns", all(json.loads((peer.bus / "multi-home" / peer.label / "meta.json").read_text()).get("listenerPid") == restarted["pid"] for peer in peers))
            for peer in peers:
                marker = "CBUS-MULTI-" + phase + "-" + peer.label + "-" + uuid.uuid4().hex
                markers.append((peer, marker))
                peers[0].command(["send", peer.target, "--from", "multi-home/verifier", marker])
            wait(peers, lambda: all(peer.count(marker) == 1 for peer, marker in markers), phase + " exact recipient delivery")
        pump(peers, 12)
        check("each_marker_received_once_only_by_exact_cli", all(peer.count(marker) == 1 and all(other.count(marker) == 0 for other in peers if other is not peer) for peer, marker in markers))
        for peer in peers:
            peer.verify_binding()
            peer.result["passed"] = True
        check("distinct_homes_and_sqlite_roots", len({peer.result["binding"]["config"]["Home"] for peer in peers}) == 2 and len({peer.result["binding"]["config"]["SQLiteHome"] for peer in peers}) == 2)
        result.update(markers=[{"target": peer.target, "text": marker} for peer, marker in markers], passed=True)
    except Exception as error:
        result["error"] = str(error)
    finally:
        for peer in reversed(peers):
            peer.cleanup()
        result["passed"] = result["passed"] and all(peer.result["passed"] for peer in peers)
        result["peerResults"] = [str(peer.root / "result.json") for peer in peers]
        (peers[0].root / "multi-home-result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
