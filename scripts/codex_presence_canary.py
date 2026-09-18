#!/usr/bin/env python3
"""Opt-in isolated ordinary-CLI presence/ownership acceptance with a fake provider.

CBUS_TEST_BINARY=/tmp/cbus-candidate python3 scripts/codex_presence_canary.py
All configuration, bus state and artifacts are temporary. No paid inference,
permission edits, terminal automation, or default user state is used. Verifies
exact rollout-writer ownership, actual CLI exit/resume, event-only native turns,
durable offline mail, explicit disconnect and daemon restart without duplicates.
"""

import argparse
import json
import os
from pathlib import Path
import signal
import sys
import time
import uuid

from codex_cli_resume_canary import ResumeCanary, process_descendants, process_exists


class PresenceCanary(ResumeCanary):
    def peer_events(self):
        if not self.peer_inbox.exists():
            return []
        records = []
        for line in self.peer_inbox.read_bytes().splitlines(keepends=True):
            if not line.endswith(b"\n"):
                break
            value = json.loads(line)
            if value.get("from") == self.target and value.get("kind") == "presence":
                records.append(value)
        return records

    def consumer(self):
        return self.status().get("consumer", {})

    def check_events(self, events):
        self.wait(lambda: len(self.peer_events()) >= len(events), "peer presence events")
        observed = self.peer_events()
        self.check("presence_sequence_" + "_".join(events),
                   [e.get("event") for e in observed] == events)
        ids = [e.get("eventId") for e in observed]
        self.check(f"unique_durable_event_ids_{len(events)}", all(ids) and len(set(ids)) == len(ids))

    def run(self):
        self.prepare()
        self.result.update(script="scripts/codex_presence_canary.py",
                           proofLayer="ordinary CLI, local fake provider, exact rollout writer presence")
        seed = "CBUS-PRESENCE-SEED-" + uuid.uuid4().hex
        pending = "CBUS-PRESENCE-OFFLINE-" + uuid.uuid4().hex
        self.result["markers"] = {"seed": seed, "pending": pending}
        self.start_cli(prompt=seed)
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "CLI rollout", 20)
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(e.get("type") == "session_meta" for e in self.entries()), "exact CLI UUID")
        meta = next(e["payload"] for e in self.entries() if e.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"),
                           codexVersion=meta.get("cli_version"), rollout=str(self.rollout))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        self.finish_provider_turn(1, seed)
        self.command(["join", "cli-resume-canary", "observer", "--session-id", str(uuid.uuid4())])
        self.peer_inbox = self.bus / "cli-resume-canary" / "observer" / "inbox.jsonl"
        connected = self.connect()
        self.wait(lambda: self.consumer().get("state") == "online", "exact writer ownership")
        initial = self.status()
        original_tree = process_descendants(self.process.pid)
        original_native = {r["pid"] for r in original_tree if Path(r["comm"]).name == "codex"}
        self.check("owner_is_actual_native_cli", self.consumer().get("pid") in original_native)
        self.check("explicit_store_did_not_claim_parent_pid", connected["config"].get("RuntimePID", 0) == 0)
        self.check_events(["join"])
        self.connect()
        self.pump(6)
        self.check("repeat_connect_and_idle_no_events_or_turns", len(self.peer_events()) == 1 and len(self.provider_turns()) == 1)
        self.stop_daemon()
        self.command(["daemon", "start"])
        self.wait(lambda: self.consumer().get("state") == "online", "consumer after daemon restart")
        self.pump(6)
        self.check("daemon_restart_no_duplicate_join_or_turn", len(self.peer_events()) == 1 and len(self.provider_turns()) == 1)

        self.command(["join", "cli-resume-canary", "visitor", "--session-id", str(uuid.uuid4())])
        self.finish_provider_turn(2, "joined cli-resume-canary as visitor")
        request = self.provider_turns()[1]
        visitor_inputs = [part.get("text", "")
                          for item in request["body"].get("input", []) if item.get("role") == "user"
                          for part in item.get("content", [])
                          if "joined cli-resume-canary as visitor" in part.get("text", "")]
        self.check("real_presence_join_is_model_visible_with_user_notice_and_no_bus_reply",
                   len(visitor_inputs) == 1 and all(text in visitor_inputs[0] for text in (
                       "from=cli-resume-canary/visitor", "kind=presence", 'event="join"',
                       "Briefly tell the user which peer joined, left, departed or was renamed",
                       "Do not send a bus reply or acknowledgment solely for this event.")))
        accepted_before = self.status()["accepted"]
        daemon_before = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.quit_cli()
        self.wait(lambda: self.consumer().get("state") == "exited", "natural CLI exit observed")
        self.check_events(["join", "departed"])
        self.command(["send", self.target, "--from", "cli-resume-canary/tester", pending])
        self.wait(lambda: self.status()["accepted"] == accepted_before + 1, "offline native queue acceptance")
        down = self.reconcile()
        self.check("offline_mail_durable_and_consumer_separate", down["lastAccepted"]["state"] == "queued"
                   and down["consumer"]["state"] == "exited")
        self.pump(6)
        self.check("offline_no_model_maintenance", len(self.provider_turns()) == 2 and self.count(pending) == 0)
        self.start_cli(resume=True)
        self.wait(lambda: self.consumer().get("state") == "online", "automatic exact-thread resume discovery")
        resumed_tree = process_descendants(self.process.pid)
        resumed_native = {r["pid"] for r in resumed_tree if Path(r["comm"]).name == "codex"}
        self.check("resume_owner_is_new_native_cli", self.consumer().get("pid") in resumed_native
                   and not resumed_native & original_native)
        self.check_events(["join", "departed", "join"])
        self.finish_provider_turn(3, pending)
        self.check("resume_preserves_epoch_and_prior_input", self.status()["id"] == initial["id"]
                   and seed in json.dumps(self.provider_turns()[2]["body"].get("input", [])))
        self.pump(self.watcher_window)
        self.check("no_duplicate_presence_or_unprompted_turn", len(self.peer_events()) == 3
                   and len(self.provider_turns()) == 3 and self.count(pending) == 1)
        current = self.status()
        self.command(["connection", "disconnect", self.target])
        self.check_events(["join", "departed", "join", "leave"])
        self.pump(6)
        self.check("explicit_disconnect_not_undone_by_live_writer", self.status()["state"] == "disconnected"
                   and self.consumer().get("state") == "disconnected" and len(self.peer_events()) == 4)
        reconnected = self.connect()
        self.check_events(["join", "departed", "join", "leave", "join"])
        self.check("reconnect_keeps_journal_and_inbox", reconnected["id"] == current["id"]
                   and reconnected["offset"] == current["offset"] and reconnected["accepted"] == current["accepted"])
        os.killpg(self.process.pid, signal.SIGKILL)
        self.process.wait(timeout=5)
        os.close(self.master)
        self.master = None
        self.process = None
        deadline = time.monotonic() + 5
        while any(process_exists(p) for p in resumed_native) and time.monotonic() < deadline:
            time.sleep(.05)
        self.wait(lambda: self.consumer().get("state") == "exited", "abrupt CLI exit observed")
        self.check_events(["join", "departed", "join", "leave", "join", "departed"])
        self.pump(6)
        self.check("abrupt_exit_no_duplicate_or_model_maintenance", len(self.peer_events()) == 6
                   and len(self.provider_turns()) == 3)
        daemon_after = json.loads(self.command(["daemon", "status", "--json"]).stdout)
        self.check("daemon_survives_both_cli_exits", daemon_before["pid"] == daemon_after["pid"])
        self.result.update(initialConnection=initial, afterResume=current, finalConnection=self.status(),
                           resumedProcessTree=resumed_tree, presenceEvents=self.peer_events(),
                           daemonBefore=daemon_before, daemonAfter=daemon_after, passed=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default=os.getenv("CBUS_TEST_TMPDIR", "/tmp"))
    parser.add_argument("--watcher-window", type=float, default=11)
    args = parser.parse_args()
    if args.watcher_window <= 10:
        parser.error("--watcher-window must exceed the native 10-second queue poll")
    canary = PresenceCanary(args)
    print(f"Presence artifacts: {canary.root}", flush=True)
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
