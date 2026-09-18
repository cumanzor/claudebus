#!/usr/bin/env python3
"""Offline ordinary-CLI interrupt/quit/resume preserves queue pause until a user turn.

The fake provider holds a real CLI turn; Escape interrupts it, /quit closes the
CLI, and exact-UUID resume creates a new native process. All state is temporary.
"""
import argparse
import json
import os
import sys
import uuid

from codex_cli_resume_canary import ResumeCanary
from codex_queue_lifecycle_canary import RPC


class InterruptCanary(ResumeCanary):
    def run(self):
        self.result.update(proofLayer="ordinary CLI interruption then exact-UUID resume; local fake provider",
                           script="scripts/codex_cli_interrupt_canary.py")
        self.prepare()
        seed = "CBUS-INTERRUPT-SEED-" + uuid.uuid4().hex
        hold = "CBUS-HOLD-TURN-" + uuid.uuid4().hex
        pending = "CBUS-PAUSED-INPUT-" + uuid.uuid4().hex
        continuation = "CBUS-USER-CONTINUE-" + uuid.uuid4().hex
        self.start_cli(prompt=seed)
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "initial CLI transcript")
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(e.get("type") == "session_meta" for e in self.entries()), "CLI identity")
        meta = next(e["payload"] for e in self.entries() if e.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"), codexVersion=meta.get("cli_version"))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        self.finish_provider_turn(1, seed)
        initial = self.connect()
        self.observer = RPC(self.codex, self.env, self.root, "interrupt-queue-observer")
        os.write(self.master, hold.encode())
        self.pump(.4)
        os.write(self.master, b"\r")
        self.wait(lambda: len(self.provider_turns()) >= 2, "held real CLI turn")
        self.command(["send", self.target, "--from", "cli-resume-canary/tester", pending])
        self.wait(lambda: self.status()["accepted"] == 1, "durable queue acceptance while busy")
        self.check("busy_turn_keeps_input_queued", len(self.queued()) == 1 and self.count(pending) == 0)
        os.write(self.master, b"\x1b")
        self.wait(lambda: any(e.get("payload", {}).get("type") == "turn_aborted" for e in self.entries()), "actual CLI interruption")
        self.provider.release(self.provider_turns()[1]["number"])
        self.check("interrupt_does_not_drop_queued_input", len(self.queued()) == 1)
        self.quit_cli()
        self.start_cli(resume=True)
        self.check("resume_uses_new_cli_pid", self.clis[0].pid != self.process.pid)
        self.wait(lambda: self.status().get("consumer", {}).get("state") == "online", "resumed native writer")
        self.pump(self.watcher_window)
        self.check("cold_resume_keeps_queue_paused", len(self.queued()) == 1 and self.count(pending) == 0)
        self.check("cold_resume_has_no_unsolicited_recipient_turn", len(self.provider_turns()) == 2)
        os.write(self.master, continuation.encode())
        self.pump(.4)
        os.write(self.master, b"\r")
        self.wait(lambda: len(self.provider_turns()) >= 3, "explicit user continuation")
        turn = self.provider_turns()[2]
        self.check("continuation_preserves_prior_history", all(marker in json.dumps(turn["body"].get("input", [])) for marker in (seed, continuation)))
        self.check("pending_input_stays_queued_until_user_turn_completes", len(self.queued()) == 1 and self.count(pending) == 0)
        self.provider.release(turn["number"])
        self.wait(lambda: len(self.provider_turns()) >= 4, "native queued input after continuation")
        queued_turn = self.provider_turns()[3]
        self.check("resumed_cli_receives_exact_pending_input", pending in json.dumps(queued_turn["body"].get("input", [])))
        self.provider.release(queued_turn["number"])
        self.wait(lambda: self.completed_count() >= 3, "queued turn completion")
        self.check("queue_drained_once", self.queued() == [] and self.count(pending) == 1)
        state = self.reconcile()
        self.check("same_binding_and_historical_receipt", state["id"] == initial["id"] and state["accepted"] == 1 and state["lastAccepted"]["state"] == "received")
        self.pump(self.watcher_window)
        self.check("no_duplicate_or_maintenance_recipient_turn", len(self.provider_turns()) == 4 and self.count(pending) == 1)
        self.check("observer_did_not_load_or_resume_thread", self.observer.call("thread/loaded/list", {})["data"] == [])
        self.result["passed"] = True


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    p.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    p.add_argument("--temp-root", default="/tmp")
    args = p.parse_args()
    args.watcher_window = 11
    c = InterruptCanary(args)
    print(f"Interrupted CLI resume artifacts: {c.root}", flush=True)
    try:
        c.run()
    except Exception as error:
        c.result["error"] = str(error)
    finally:
        c.cleanup()
    print(json.dumps({"passed": c.result["passed"], "checks": c.result["checks"],
                      "error": c.result.get("error"), "result": str(c.root / "result.json")}), flush=True)
    return 0 if c.result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
