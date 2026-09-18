#!/usr/bin/env python3
"""Offline ordinary-CLI /compact -> exactly one durable local peer notice.

Uses an isolated local fake provider and scratch homes only. No paid inference,
global hooks or user sessions are involved. It tests completed notifications,
not pre-compaction hooks or a deadline for peers to checkpoint.
"""
import argparse
import json
import os
import sys

from codex_cli_resume_canary import ResumeCanary


class CompactionCanary(ResumeCanary):
    def __init__(self, args):
        super().__init__(args)
        self.target = "cli-compaction/advisor"
        self.result.update(proofLayer="ordinary CLI compaction and local peer presence; local fake provider",
                           script="scripts/codex_compaction_canary.py")

    def notices(self):
        inbox = self.bus / "cli-compaction" / "verifier" / "inbox.jsonl"
        return [message for message in map(json.loads, inbox.read_text().splitlines())
                if message.get("event") == "compact-post"]

    def run(self):
        self.prepare()
        self.command(["join", "cli-compaction", "verifier", "--session-id", "isolated-compaction-verifier"])
        self.start_cli(prompt="Local compaction observation acceptance")
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "initial rollout")
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(e.get("type") == "session_meta" for e in self.entries()), "metadata")
        meta = next(e["payload"] for e in self.entries() if e.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"), codexVersion=meta.get("cli_version"))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        self.finish_provider_turn(1, "Local compaction observation acceptance")
        state = json.loads(self.command(["connect", "cli-compaction", "advisor", "--codex-sqlite-home", str(self.home), "--json"], recipient=True).stdout)
        self.check("baseline_is_initialized", state.get("compaction", {}).get("offset", 0) > 0)
        self.check("attachment_does_not_emit_compaction", self.notices() == [])
        os.write(self.master, b"/compact")
        self.pump(.4)
        os.write(self.master, b"\r")
        self.wait(lambda: len(self.provider_turns()) >= 2, "compaction provider request")
        self.check("no_notice_before_completion", self.notices() == [])
        self.provider.release(self.provider_turns()[1]["number"])
        self.wait(lambda: any(e.get("type") == "compacted" for e in self.entries()), "durable compaction record")
        self.wait(lambda: len(self.notices()) == 1, "recipient completed-compaction notice")
        first = self.notices()[0]
        self.check("completed_notice_has_exact_source_and_stable_id", first["from"] == self.target and bool(first.get("eventId")))
        self.check("notice_has_only_fixed_metadata", first["text"] == "Codex context compacted; in-context state was reset (observed completion)")
        self.check("saved_completed_count_is_one", self.status().get("compaction", {}).get("completed") == 1)
        self.command(["daemon", "restart", "--json"])
        self.pump(6)
        self.provider_turns()
        self.check("daemon_restart_does_not_replay_notice", self.notices() == [first])
        self.check("only_seed_and_compaction_provider_turns", len(self.provider_turns()) == 2)
        self.result.update(notice=first, passed=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default="/tmp")
    args = parser.parse_args()
    args.watcher_window = 11
    c = CompactionCanary(args)
    print(f"Compaction canary artifacts: {c.root}", flush=True)
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
