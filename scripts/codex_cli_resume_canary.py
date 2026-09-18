#!/usr/bin/env python3
"""Opt-in offline acceptance check for an ordinary Codex CLI restart/resume.

Run with CBUS_TEST_BINARY=/path/to/cbus python3 scripts/codex_cli_resume_canary.py.
CODEX_TEST_BINARY selects Codex; CBUS_TEST_TMPDIR selects the artifact parent.
The test requires PTY, Unix-socket, localhost-bind, and process-inspection access.

All Codex, cbus, HOME, and workspace state is temporary and retained as evidence.
Only a local fake Responses provider is configured; no paid inference is used.
The actual CLI exits with /quit and a new CLI resumes its exact UUID. A read-only
app-server observer lists queued/loaded threads; it never starts or resumes one.
Use --resume-selector uuid|last|name|picker to choose the actual CLI resume path.
This proves clean CLI resume and retained input, not a model-driven skill/reply,
interrupted resume, or long-idle behavior. Explicit cbus reconciliation must
observe the pending message as queued, then received, without another submission
or provider request.
"""

import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import struct
import subprocess
import sys
import termios
import time
import uuid

from codex_native_queue_canary import Canary
from codex_queue_lifecycle_canary import FakeProvider, RPC


def process_descendants(pid):
    rows = {}
    output = subprocess.check_output(
        ["ps", "-axo", "pid=,ppid=,comm="], text=True, timeout=5)
    for line in output.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) == 3:
            rows[int(parts[0])] = {"pid": int(parts[0]), "ppid": int(parts[1]), "comm": parts[2]}
    selected = {pid}
    while True:
        more = {key for key, row in rows.items() if row["ppid"] in selected}
        if more <= selected:
            break
        selected.update(more)
    return [rows[key] for key in sorted(selected) if key in rows]


def process_exists(pid):
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


class ResumeCanary(Canary):
    def __init__(self, args):
        super().__init__(args)
        self.provider = None
        self.observer = None
        self.clis = []
        self.watcher_window = args.watcher_window
        self.resume_selector = getattr(args, "resume_selector", "uuid")
        self.resume_name = "cbus-resume-" + uuid.uuid4().hex[:12]
        self.target = "cli-resume-canary/advisor"
        self.result.update(proofLayer=f"ordinary CLI quit and {self.resume_selector} CLI resume; exact-thread history check; local fake provider",
                           resumeSelector=self.resume_selector,
                           script="scripts/codex_cli_resume_canary.py")

    def prepare(self):
        user_home = self.root / "user-home"
        user_home.mkdir()
        self.env["HOME"] = str(user_home)
        for key in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
            self.env.pop(key, None)
        self.env["NO_PROXY"] = "127.0.0.1,localhost"
        self.provider = FakeProvider()
        (self.home / "config.toml").write_text(
            'model = "cbus-cli-resume-probe"\nmodel_provider = "cbus_local_mock"\n'
            'approval_policy = "never"\nsandbox_mode = "read-only"\n'
            'check_for_update_on_startup = false\n'
            '[model_providers.cbus_local_mock]\nname = "Local fake Responses provider"\n'
            f'base_url = "http://127.0.0.1:{self.provider.server.server_port}/v1"\n'
            'wire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false\n'
            'request_max_retries = 0\nstream_max_retries = 0\n'
            '[analytics]\nenabled = false\n[features]\nunbounded_connection_retries = false\n'
            'enable_request_compression = false\nshell_snapshot = false\n'
            f'[projects.{json.dumps(str(self.work))}]\ntrust_level = "trusted"\n')

    def pump(self, seconds):
        if self.master is None:
            time.sleep(seconds)
        else:
            super().pump(seconds)

    def wait(self, predicate, description, timeout=35):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if predicate():
                return
            if self.process is not None and self.process.poll() is not None:
                raise RuntimeError(f"CLI exited while waiting for {description}")
            self.pump(.15)
        raise TimeoutError(f"timed out waiting for {description}")

    def start_cli(self, resume=False, prompt=None):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        os.set_blocking(self.master, False)
        args = [self.codex, "--no-alt-screen", "-C", str(self.work)]
        if resume:
            selectors = {"uuid": [self.thread], "last": ["--last"], "name": [self.resume_name], "picker": []}
            args.extend(["resume", *selectors[self.resume_selector]])
        else:
            args.append(prompt)
        try:
            self.process = subprocess.Popen(args, stdin=slave, stdout=slave, stderr=slave,
                                            env=self.env, cwd=self.work, start_new_session=True)
        finally:
            os.close(slave)
        self.clis.append(self.process)
        self.result.setdefault("cliLaunches", []).append({"args": args, "pid": self.process.pid})
        if resume and self.resume_selector == "picker":
            self.pump(2)
            os.write(self.master, b"\r")

    def quit_cli(self):
        self.pump(.5)
        original = self.process
        tree = process_descendants(original.pid)
        self.result["originalProcessTree"] = tree
        self.check("ordinary_native_cli_process_observed", any(Path(row["comm"]).name == "codex" for row in tree))
        os.write(self.master, b"/quit")
        self.pump(.4)  # Let the TUI's paste-burst detector settle before Enter.
        os.write(self.master, b"\r")
        deadline = time.monotonic() + 10
        while original.poll() is None and time.monotonic() < deadline:
            self.pump(.1)
        self.check("original_cli_exited_cleanly", original.poll() == 0)
        deadline = time.monotonic() + 5
        while any(process_exists(row["pid"]) for row in tree) and time.monotonic() < deadline:
            time.sleep(.05)
        self.check("original_cli_process_tree_exited", not any(process_exists(row["pid"]) for row in tree))
        os.close(self.master)
        self.master = None
        self.process = None

    def connect(self):
        return json.loads(self.command(["connect", "cli-resume-canary", "advisor", "--codex-sqlite-home", str(self.home), "--json"], recipient=True).stdout)

    def entries(self):
        if self.rollout is None:
            return []
        records = []
        for number, line in enumerate(self.rollout.read_bytes().splitlines(keepends=True), 1):
            if not line.endswith(b"\n"):
                break  # Only the unfinished physical tail may still be in flight.
            try:
                records.append(json.loads(line))
            except (json.JSONDecodeError, UnicodeDecodeError) as error:
                raise RuntimeError(f"malformed complete rollout record {number}: {error}") from error
        return records

    def completed_count(self):
        return sum(record.get("payload", {}).get("type") == "task_complete" for record in self.entries())

    def provider_turns(self):
        with self.provider.condition:
            requests = list(self.provider.requests)
        turns = []
        for request in requests:
            body = request["body"]
            if body.get("client_metadata", {}).get("thread_id") == self.thread:
                turns.append(request)
                continue
            # Ordinary CLI startup can create a separate ephemeral title request.
            # It is local fake inference, but must not count as recipient activity.
            schema = body.get("text", {}).get("format", {}).get("schema", {})
            metadata = json.loads(body.get("client_metadata", {}).get("x-codex-turn-metadata", "{}"))
            if set(schema.get("properties", {})) != {"title"} or metadata.get("thread_source") != "system":
                raise AssertionError("unexpected auxiliary provider request")
            request["classification"] = "automatic-cli-title"
            request["release"].set()
        return turns

    def finish_provider_turn(self, number, marker):
        self.wait(lambda: len(self.provider_turns()) >= number, f"recipient provider request {number}")
        request = self.provider_turns()[number - 1]
        self.check(f"provider_request_{number}_contains_expected_input", marker in json.dumps(request["body"].get("input", [])))
        self.provider.release(request["number"])
        self.wait(lambda: self.completed_count() >= number, f"turn {number} completion")

    def queued(self):
        return self.observer.call("thread/queue/list", {"threadId": self.thread})["data"]

    def reconcile(self):
        return json.loads(self.command(["connection", "reconcile", self.target, "--json"]).stdout)

    def run(self):
        self.prepare()
        seed = "CBUS-RESUME-SEED-" + uuid.uuid4().hex
        before = "CBUS-BEFORE-CLI-EXIT-" + uuid.uuid4().hex
        pending = "CBUS-WHILE-CLI-DOWN-" + uuid.uuid4().hex
        self.result["markers"] = {"seed": seed, "beforeExit": before, "pending": pending}
        self.start_cli(prompt=seed)
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "initial CLI transcript", 20)
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(record.get("type") == "session_meta" for record in self.entries()), "CLI identity")
        meta = next(record["payload"] for record in self.entries() if record.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"),
                           codexVersion=meta.get("cli_version"), rollout=str(self.rollout))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        self.finish_provider_turn(1, seed)
        initial_connection = self.connect()
        daemon_before = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.command(["send", self.target, "--from", "cli-resume-canary/tester", before])
        self.finish_provider_turn(2, before)
        self.check("initial_history_present", self.count(seed) == 1 and self.count(before) == 1)
        self.observer = RPC(self.codex, self.env, self.root, "queue-observer")
        self.check("queue_empty_before_cli_exit", self.queued() == [])
        if self.resume_selector == "name":
            os.write(self.master, ("/rename " + self.resume_name).encode())
            self.pump(.4)
            os.write(self.master, b"\r")
            index = self.home / "session_index.jsonl"
            def exact_name_saved():
                if not index.exists():
                    return False
                rows = [json.loads(line) for line in index.read_bytes().splitlines(keepends=True) if line.endswith(b"\n")]
                return any(row.get("id") == self.thread and row.get("thread_name") == self.resume_name for row in rows)
            self.wait(exact_name_saved, "exact thread name persisted")
            self.result["resumeName"] = self.resume_name
            self.check("resume_name_resolves_exact_saved_thread", exact_name_saved())
        self.quit_cli()
        print("Original CLI exited cleanly; checking pending mail while it is down.", flush=True)

        self.command(["send", self.target, "--from", "cli-resume-canary/tester", pending])
        self.wait(lambda: self.status()["accepted"] == 2, "daemon acceptance while CLI absent")
        queued_before = self.queued()
        self.check("queued_message_preserved_without_cli", len(queued_before) == 1
                   and pending in json.dumps(queued_before[0]))
        requests_before = self.provider.count()
        reconciled_down = self.reconcile()
        down_observation = reconciled_down.get("lastAccepted", {})
        self.check("reconcile_observes_queued_while_cli_down", down_observation.get("state") == "queued"
                   and down_observation.get("attempt", {}).get("clientId") == queued_before[0]["clientUserMessageId"])
        self.check("queued_reconcile_preserves_acceptance_and_queue", reconciled_down["accepted"] == 2
                   and self.queued() == queued_before and self.provider.count() == requests_before)
        self.pump(self.watcher_window)
        self.check("absent_cli_does_not_consume_queue", self.queued() == queued_before
                   and self.count(pending) == 0 and len(self.provider_turns()) == 2)
        daemon_during = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.check("cbus_daemon_survives_cli_exit", daemon_during["pid"] == daemon_before["pid"])
        self.result["downtime"] = {"secondsObserved": self.watcher_window, "queue": queued_before,
                                   "connection": self.status(), "daemon": daemon_during,
                                   "reconciled": reconciled_down}

        self.start_cli(resume=True)
        self.check("resume_uses_new_cli_pid", self.clis[0].pid != self.process.pid)
        print(f"Resuming CLI with {self.resume_selector}; requiring exact prior thread {self.thread}.", flush=True)
        self.finish_provider_turn(3, pending)
        request = self.provider_turns()[2]
        self.check("resumed_provider_has_prior_history", all(marker in json.dumps(request["body"].get("input", []))
                   for marker in (seed, before)))
        self.check("resume_preserves_exact_transcript", list(self.home.rglob("rollout-*.jsonl")) == [self.rollout]
                   and all(record["payload"]["id"] == self.thread for record in self.entries()
                           if record.get("type") == "session_meta"))
        self.check("native_queue_drained_after_cli_resume", self.queued() == [])
        self.pump(.5)
        self.provider_turns()  # Release any concurrent local automatic-title response.
        requests_before = self.provider.count()
        reconciled_up = self.reconcile()
        up_observation = reconciled_up.get("lastAccepted", {})
        self.check("reconcile_observes_received_after_resume", up_observation.get("state") == "received"
                   and up_observation.get("attempt", {}).get("clientId") == queued_before[0]["clientUserMessageId"]
                   and bool(up_observation.get("itemId")))
        self.check("received_reconcile_preserves_acceptance_and_queue", reconciled_up["accepted"] == 2
                   and self.queued() == [] and self.provider.count() == requests_before)
        reconnected = self.connect()
        self.check("reconnect_preserves_binding_and_acceptance", reconnected["id"] == initial_connection["id"]
                   and reconnected["accepted"] == 2 and reconnected["threadId"] == self.thread)
        self.pump(self.watcher_window)
        self.check("each_input_received_exactly_once", all(self.count(marker) == 1 for marker in (seed, before, pending)))
        self.check("exactly_three_recipient_provider_requests", len(self.provider_turns()) == 3)
        self.check("observer_never_loaded_thread", self.observer.call("thread/loaded/list", {})["data"] == [])
        daemon_after = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.check("same_cbus_daemon_after_cli_resume", daemon_after["pid"] == daemon_before["pid"])
        resumed_tree = process_descendants(self.process.pid)
        original_native = {row["pid"] for row in self.result["originalProcessTree"] if Path(row["comm"]).name == "codex"}
        resumed_native = {row["pid"] for row in resumed_tree if Path(row["comm"]).name == "codex"}
        self.check("resumed_native_cli_pid_changed", bool(resumed_native) and not resumed_native & original_native)
        self.result.update(initialConnection=initial_connection, afterResume=self.status(),
                           daemonBefore=daemon_before, daemonAfter=daemon_after,
                           resumedProcessTree=resumed_tree, reconciledAfterResume=reconciled_up,
                           auxiliaryTitleRequests=self.provider.count() - len(self.provider_turns()), passed=True)

    def cleanup(self):
        errors = []
        for label, resource in (("observer", self.observer), ("provider", self.provider)):
            if resource is not None:
                try:
                    resource.close()
                except Exception as error:
                    errors.append(f"{label}: {error}")
        try:
            super().cleanup()
        except Exception as error:
            errors.append(f"CLI/daemon cleanup: {error}")
        known_pids = [row["pid"] for key in ("originalProcessTree", "resumedProcessTree")
                      for row in self.result.get(key, [])]
        deadline = time.monotonic() + 3
        while any(process_exists(pid) for pid in known_pids) and time.monotonic() < deadline:
            time.sleep(.05)
        self.result["cleanup"] = {
            "allCliLaunchesExited": all(process.poll() is not None for process in self.clis),
            "allObservedCliProcessesExited": not any(process_exists(pid) for pid in known_pids),
            "observerExited": self.observer is None or self.observer.process.poll() is not None,
            "providerExited": self.provider is None or not self.provider.thread.is_alive(),
            "cbusSocketRemoved": not (self.bus / ".daemon" / "control.sock").exists(),
        }
        try:
            complete_jsonl = self.rollout is not None and bool(self.entries()) and self.rollout.read_bytes().endswith(b"\n")
        except Exception as error:
            complete_jsonl = False
            errors.append(f"final transcript: {error}")
        self.result["checks"]["final_complete_transcript_parses_strictly"] = complete_jsonl
        if errors:
            self.result["cleanupErrors"] = errors
        self.result["passed"] = self.result["passed"] and complete_jsonl and not errors and all(self.result["cleanup"].values())
        if self.provider is not None:
            self.result["providerRequests"] = [{key: value for key, value in record.items()
                                                if key != "release"} for record in self.provider.requests]
        (self.root / "result.json").write_text(json.dumps(self.result, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), help="built cbus candidate (required)")
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"), help="installed Codex CLI")
    parser.add_argument("--temp-root", default=os.getenv("CBUS_TEST_TMPDIR", "/tmp"))
    parser.add_argument("--watcher-window", type=float, default=11, help="seconds, greater than the 10-second native poll")
    parser.add_argument("--resume-selector", choices=("uuid", "last", "name", "picker"), default="uuid")
    args = parser.parse_args()
    if not args.cbus:
        parser.error("set CBUS_TEST_BINARY or --cbus")
    if args.watcher_window <= 10:
        parser.error("--watcher-window must exceed the 10-second native queue poll")
    canary = ResumeCanary(args)
    print(f"CLI resume artifacts: {canary.root}", flush=True)
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
