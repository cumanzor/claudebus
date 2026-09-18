#!/usr/bin/env python3
"""Opt-in actual Codex --remote wrapper acceptance using only a local fake provider.

All HOME, CODEX_HOME, bus and workspace state is temporary and retained. Exercises
fresh launch, UUID/name/--last/picker resume, actual shell reply without --from,
and graceful/SIGTERM/SIGHUP teardown. No paid inference or user settings are used.
The scratch workspace-write config permits /tmp; this is an identity/lifecycle
check, not proof of unattended permission rules outside writable paths.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import shlex
import signal
import struct
import subprocess
import sys
import termios
import time
import uuid

from codex_cli_resume_canary import ResumeCanary, process_descendants, process_exists
from codex_queue_lifecycle_canary import FakeProvider


class WrapperCanary(ResumeCanary):
    def __init__(self, args):
        super().__init__(args)
        self.target = "legacy-canary/advisor"
        self.marker = "CBUS-WRAPPER-REPLY-" + uuid.uuid4().hex
        self.tool_requested = False
        self.trees = []
        self.result.update(proofLayer="actual --remote CLI wrapper, selector resume and shell identity; local fake provider",
                           script="scripts/codex_legacy_wrapper_canary.py")

    def prepare(self):
        super().prepare()
        self.provider.close()
        self.provider = FakeProvider(output=self.provider_output)
        config = (self.home / "config.toml").read_text()
        config = re.sub(r'http://127\.0\.0\.1:\d+/v1', f'http://127.0.0.1:{self.provider.server.server_port}/v1', config)
        config = config.replace('sandbox_mode = "read-only"', 'sandbox_mode = "workspace-write"')
        (self.home / "config.toml").write_text(config)
        for name in ("CLAUDE_CODE_SESSION_ID", "CBUS_SESSION_ID", "GROK_SESSION_ID", "CODEX_THREAD_ID"):
            self.env[name] = "stale-launcher-identity"
        self.command(["join", "legacy-canary", "launcher", "--session-id", "stale-launcher-identity"])
        self.command(["join", "legacy-canary", "verifier", "--session-id", "isolated-verifier"])

    def provider_output(self, number, body):
        if self.tool_requested or body.get("client_metadata", {}).get("thread_id") != self.thread or self.marker not in json.dumps(body.get("input", [])):
            return []
        self.tool_requested = True
        tools = {tool.get("name") for tool in body.get("tools", [])}
        command = shlex.join([self.cbus, "send", "legacy-canary/verifier", self.marker])
        if "exec_command" in tools:
            name, arguments = "exec_command", {"cmd": command, "yield_time_ms": 1000, "max_output_tokens": 2000}
        elif "shell_command" in tools:
            name, arguments = "shell_command", {"command": command}
        else:
            raise AssertionError("expected shell tool")
        self.result["requestedTool"] = {"name": name, "arguments": arguments}
        return [{"type": "function_call", "id": f"wrapper-item-{number}", "call_id": f"wrapper-call-{number}",
                 "name": name, "arguments": json.dumps(arguments)}]

    def pump(self, seconds):
        if self.provider is not None:
            with self.provider.condition:
                for request in self.provider.requests:
                    request["release"].set()
        super().pump(seconds)

    def launch(self, label, arguments):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        os.set_blocking(self.master, False)
        args = [self.cbus, "codex", "--channel", "legacy-canary", "--alias", "advisor", "--no-alt-screen", *arguments]
        try:
            self.process = subprocess.Popen(args, stdin=slave, stdout=slave, stderr=slave, env=self.env,
                                            cwd=self.work, start_new_session=True)
        finally:
            os.close(slave)
        self.clis.append(self.process)
        self.result.setdefault("cliLaunches", []).append({"label": label, "args": args, "pid": self.process.pid})
        if label == "picker":
            self.pump(2)
            os.write(self.master, b"\r")
        meta_path = self.bus / "legacy-canary/advisor/meta.json"
        def joined():
            if not meta_path.exists():
                return False
            meta = json.loads(meta_path.read_text())
            return meta.get("listenerPid") == self.process.pid
        self.wait(joined, label + " live wrapper registration", 55)
        meta = json.loads(meta_path.read_text())
        if self.thread is None:
            self.thread = meta["sessionId"]
            self.wait(lambda: any(self.thread in str(path) for path in self.home.rglob("rollout-*.jsonl")), "wrapper transcript")
            self.rollout = next(path for path in self.home.rglob("rollout-*.jsonl") if self.thread in str(path))
            self.result.update(threadId=self.thread, rollout=str(self.rollout))
        self.check(label + "_exact_thread_registered", meta["sessionId"] == self.thread)
        self.pump(1)
        self.check(label + "_wrapper_still_alive", self.process.poll() is None)
        tree = process_descendants(self.process.pid)
        self.trees.extend(tree)
        self.result.setdefault("processTrees", []).append({"label": label, "tree": tree})
        return tree

    def finish(self, label, sig=None):
        tree = process_descendants(self.process.pid)
        self.trees.extend(tree)
        process = self.process
        if sig is None:
            os.write(self.master, b"/quit")
            self.pump(.4)
            os.write(self.master, b"\r")
        else:
            os.kill(process.pid, sig)
        deadline = time.monotonic() + 12
        while process.poll() is None and time.monotonic() < deadline:
            self.pump(.1)
        self.check(label + "_wrapper_exited", process.poll() is not None and (process.returncode == 0 if sig is None else process.returncode != 0))
        deadline = time.monotonic() + 5
        while any(process_exists(row["pid"]) for row in tree) and time.monotonic() < deadline:
            self.pump(.1)
        alive = [row for row in tree if process_exists(row["pid"])]
        if alive:
            self.result.setdefault("teardownSurvivors", []).append({"label": label, "processes": alive, "ps": subprocess.run(["ps", "-p", ",".join(str(row["pid"]) for row in alive), "-o", "pid=,ppid=,pgid=,stat=,args="], capture_output=True, text=True).stdout})
        self.check(label + "_all_children_exited", not alive)
        self.check(label + "_socket_removed", not list((self.bus / ".sock").glob("*.sock")))
        os.close(self.master)
        self.master, self.process = None, None

    def run(self):
        self.prepare()
        self.launch("fresh", ["CBUS wrapper initial seed"])
        self.wait(lambda: self.completed_count() >= 1, "fresh seed complete")
        before_reply = self.completed_count()
        self.command(["send", self.target, "--from", "legacy-canary/verifier", self.marker])
        reply_path = self.bus / "legacy-canary/verifier/inbox.jsonl"
        def replies():
            return [json.loads(line) for line in reply_path.read_text().splitlines() if json.loads(line).get("text") == self.marker]
        self.wait(lambda: len(replies()) == 1, "actual model shell reply without from")
        self.check("actual_shell_reply_uses_wrapper_identity", replies()[0]["from"] == self.target)
        self.check("reply_does_not_spoof_launcher", replies()[0]["from"] != "legacy-canary/launcher")
        self.wait(lambda: self.completed_count() > before_reply, "reply turn completion")
        name = "cbus-wrapper-selector-" + uuid.uuid4().hex[:8]
        os.write(self.master, ("/rename " + name).encode())
        self.pump(.4)
        os.write(self.master, b"\r")
        self.pump(1)
        self.result["resumeName"] = name
        self.finish("fresh")
        for label, selector in (("uuid", [self.thread]), ("last", ["--last"]), ("name", [name]), ("picker", [])):
            self.launch(label, ["resume", *selector])
            marker = "CBUS-WRAPPER-" + label.upper() + "-" + uuid.uuid4().hex
            before = self.completed_count()
            self.command(["send", self.target, "--from", "legacy-canary/verifier", marker])
            self.wait(lambda: self.count(marker) == 1 and self.completed_count() > before, label + " inbound completion")
            self.check(label + "_one_inbound_same_transcript", self.count(marker) == 1)
            self.finish(label, signal.SIGTERM if label == "name" else signal.SIGHUP if label == "picker" else None)
        self.check("one_rollout_across_resume_selectors", list(self.home.rglob("rollout-*.jsonl")) == [self.rollout])
        contexts = [record["payload"] for record in self.entries() if record.get("type") == "turn_context"]
        self.check("caller_policy_preserved", bool(contexts) and all(record.get("approval_policy") == "never"
                   and record.get("sandbox_policy", {}).get("type") == "workspace-write" for record in contexts))
        self.check("exactly_one_reply_in_verifier_inbox", len(replies()) == 1)
        self.result["passed"] = True

    def cleanup(self):
        super().cleanup()
        deadline = time.monotonic() + 5
        while any(process_exists(row["pid"]) for row in self.trees) and time.monotonic() < deadline:
            time.sleep(.1)
        alive = [row for row in self.trees if process_exists(row["pid"])]
        self.result["cleanup"]["allWrapperAndServerProcessesExited"] = not alive
        self.result["cleanup"]["allWrapperSocketsRemoved"] = not list((self.bus / ".sock").glob("*.sock"))
        self.result["passed"] = self.result["passed"] and all(self.result["cleanup"].values())
        if alive:
            self.result["cleanupRemainingProcesses"] = alive
        (self.root / "result.json").write_text(json.dumps(self.result, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default="/tmp")
    args = parser.parse_args()
    args.watcher_window = 11
    canary = WrapperCanary(args)
    print(f"Legacy wrapper artifacts: {canary.root}", flush=True)
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
