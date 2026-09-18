#!/usr/bin/env python3
"""Offline regression: a vscode-origin thread becomes an ordinary CLI bus peer.

A scratch app-server creates and completes the historical seed turn through a
local fake provider. Its real returned source must be vscode; no transcript or
SQLite metadata is rewritten. The seeding server exits before an actual CLI
resumes that exact UUID. A later observer only reads thread/queue state.

Connect is harness-driven with the exact store, not model-selected skill use.
The test proves current CLI admission, exact-thread delivery and offline daemon
restart/resume retention; it does not claim a running desktop client is supported.
HOME, CODEX_HOME, CBUS_DIR, provider, threads and processes are isolated under /tmp.
No paid model calls or user sessions; installed binaries and permissions stay unchanged.
"""
import argparse
import json
import os
import sys
import uuid

from codex_cli_resume_canary import ResumeCanary, process_descendants
from codex_queue_lifecycle_canary import RPC


class OriginCanary(ResumeCanary):
    def seed_vscode_thread(self, marker):
        self.observer = RPC(self.codex, self.env, self.root, "vscode-origin-seed")
        owner = self.observer
        thread = owner.call("thread/start", {"cwd": str(self.work), "model": "cbus-cli-resume-probe",
                            "modelProvider": "cbus_local_mock", "approvalPolicy": "never", "sandbox": "read-only"})["thread"]
        self.thread = thread["id"]
        self.result.update(threadId=self.thread, source=thread["source"], codexVersion=thread["cliVersion"],
                           sourceMethod="actual app-server thread/start; unchanged historical metadata")
        self.check("app_server_created_vscode_origin", thread["source"] == "vscode")
        turn = owner.call("turn/start", {"threadId": self.thread, "input": [{"type": "text", "text": marker}]})["turn"]["id"]
        self.wait(lambda: len(self.provider_turns()) >= 1, "seed local provider request")
        request = self.provider_turns()[0]
        self.check("seed_request_contains_marker", marker in json.dumps(request["body"].get("input", [])))
        self.provider.release(request["number"])
        completed = owner.wait_event("turn/completed", lambda p: p["turn"]["id"] == turn)
        self.check("historical_seed_turn_completed", completed["turn"]["status"] == "completed")
        owner.close()
        self.check("seeding_owner_exited_before_cli", owner.process.poll() is not None)
        self.observer = None
        paths = list(self.home.rglob("rollout-*" + self.thread + ".jsonl"))
        self.check("one_exact_seed_transcript", len(paths) == 1)
        self.rollout = paths[0]
        metas = [row["payload"] for row in self.entries() if row.get("type") == "session_meta"]
        self.check("persisted_origin_is_vscode", bool(metas) and metas[0]["source"] == "vscode")
        self.check("seed_history_persisted_once", self.count(marker) == 1)

    def origin(self):
        thread = self.observer.call("thread/read", {"threadId": self.thread, "includeTurns": False})["thread"]
        self.check("observer_read_exact_thread", thread["id"] == self.thread)
        return thread["source"]

    def reconcile(self):
        # Daemon controls deliberately return busy while a connection lane runs.
        for attempt in range(10):
            run = self.command(["connection", "reconcile", self.target, "--json"], allowed=(0, 1))
            if run.returncode == 0:
                self.result["reconcileBusyRetries"] = attempt
                return json.loads(run.stdout)
            if run.stderr.strip() != "cbus: connection or daemon workers busy; try again":
                raise RuntimeError(f"reconcile failed: {run.stderr.strip()}")
            self.pump(.3)
        raise RuntimeError("reconcile remained busy after 10 attempts")

    def run(self):
        self.prepare()
        self.env["CBUS_UPDATE_CHECK"] = "0"
        self.result.update(script="scripts/codex_cli_origin_canary.py",
                           proofLayer="actual app-server vscode origin resumed by actual CLI; explicit-store harness connect; fake provider")
        seed, first, pending = ("CBUS-ORIGIN-" + label + "-" + uuid.uuid4().hex for label in ("SEED", "FIRST", "OFFLINE"))
        self.result["markers"] = {"seed": seed, "first": first, "offline": pending}
        self.seed_vscode_thread(seed)
        self.start_cli(resume=True)
        self.pump(2)
        self.observer = RPC(self.codex, self.env, self.root, "read-only-origin-observer")
        self.check("resumed_cli_keeps_historical_vscode_source", self.origin() == "vscode")
        initial = self.connect()
        self.check("vscode_origin_admitted_to_exact_cli_thread", initial["threadId"] == self.thread
                   and initial["state"] == "queue-ready")
        self.check("live_cli_writer_observed", initial.get("consumer", {}).get("state") == "online")
        self.command(["send", self.target, "--from", "cli-resume-canary/verifier", first])
        self.finish_provider_turn(2, first)
        self.check("first_delivery_once_in_original_thread", self.count(first) == 1 and self.count(seed) == 1)
        self.check("queue_drained_after_first_delivery", self.queued() == [])
        self.quit_cli()
        self.command(["send", self.target, "--from", "cli-resume-canary/verifier", pending])
        self.wait(lambda: self.status()["accepted"] == 2, "offline native queue acceptance")
        queued = self.queued()
        self.check("offline_message_queued_once", len(queued) == 1 and pending in json.dumps(queued[0]))
        daemon_before = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.command(["daemon", "restart"])
        daemon_after = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.check("daemon_restarted_while_cli_stopped", daemon_before["pid"] != daemon_after["pid"] and self.process is None)
        offline = self.status()
        self.check("offline_restart_retains_connection_epoch", all(offline[key] == initial[key] for key in ("id", "threadId", "dev", "ino")))
        self.pump(self.watcher_window)
        self.check("offline_restart_retains_exact_queue_without_turn", self.queued() == queued
                   and offline["accepted"] == 2 and self.count(pending) == 0 and len(self.provider_turns()) == 2)
        self.start_cli(resume=True)
        self.result["resumedProcessTree"] = process_descendants(self.process.pid)
        self.finish_provider_turn(3, pending)
        self.result["resumedProcessTree"] = process_descendants(self.process.pid)
        reconnected = self.connect()
        self.check("reconnect_keeps_same_registration", all(reconnected[key] == initial[key] for key in ("id", "threadId", "dev", "ino")))
        self.check("second_resume_keeps_vscode_origin", self.origin() == "vscode")
        self.pump(self.watcher_window)
        self.check("both_bus_markers_consumed_exactly_once", all(self.count(marker) == 1 for marker in (seed, first, pending))
                   and self.queued() == [] and self.status()["accepted"] == 2 and len(self.provider_turns()) == 3)
        self.check("resumes_preserve_one_transcript", list(self.home.rglob("rollout-*.jsonl")) == [self.rollout])
        receipt = self.reconcile()
        self.check("offline_message_has_exact_history_receipt", receipt.get("lastAccepted", {}).get("state") == "received"
                   and receipt["lastAccepted"]["attempt"]["clientId"] == queued[0]["clientUserMessageId"])
        self.result["passed"] = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default="/tmp")
    args = parser.parse_args()
    args.watcher_window = 11
    canary = OriginCanary(args)
    print(f"CLI historical-origin artifacts: {canary.root}", flush=True)
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
