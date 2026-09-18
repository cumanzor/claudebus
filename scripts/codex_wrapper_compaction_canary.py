#!/usr/bin/env python3
"""Real wrapper fresh/resumed /compact and local completed-notice acceptance.

Temporary homes and a local fake model provider only. Verifies recipient inboxes
and persisted completions; a late independent app-server connection is not an
item subscriber and therefore is not used as a protocol observer.
"""
import argparse
import json
import os

from codex_cli_resume_canary import ResumeCanary
from codex_legacy_wrapper_canary import WrapperCanary


class CompactionWrapperCanary(WrapperCanary):
    def __init__(self, args):
        super().__init__(args)
        self.hold_provider = False
        self.result.update(script="scripts/codex_wrapper_compaction_canary.py",
                           proofLayer="actual fresh/resumed wrapper compaction and exact local peer receipt")

    def pump(self, seconds):
        if self.hold_provider:
            ResumeCanary.pump(self, seconds)
        else:
            super().pump(seconds)

    def notices(self):
        path = self.bus / "legacy-canary/verifier/inbox.jsonl"
        return [row for row in map(json.loads, path.read_text().splitlines())
                if row.get("event") == "compact-post"]

    def run(self):
        self.prepare()
        for index, label in enumerate(("fresh", "resumed")):
            args = ["Local wrapper compaction acceptance"] if index == 0 else ["resume", self.thread]
            self.launch(label, args)
            if index == 0:
                self.wait(lambda: self.completed_count() >= 1, "seed completion")
            self.check(label + "_attachment_does_not_replay", len(self.notices()) == index)
            requests = len(self.provider_turns())
            self.hold_provider = True
            os.write(self.master, b"/compact")
            self.pump(.4)
            os.write(self.master, b"\r")
            self.wait(lambda: len(self.provider_turns()) > requests, label + " compaction request")
            self.check(label + "_no_notice_before_completion", len(self.notices()) == index)
            self.hold_provider = False
            self.wait(lambda: sum(row.get("type") == "compacted" for row in self.entries()) == index + 1,
                      label + " durable compaction record")
            self.wait(lambda: len(self.notices()) == index + 1, label + " peer completion notice")
            self.pump(2)
            self.check(label + "_one_fixed_local_notice", len(self.notices()) == index + 1 and
                       self.notices()[-1]["from"] == self.target and
                       self.notices()[-1]["text"] == "Codex context compacted; in-context state was reset (observed completion)")
            self.check(label + "_only_compaction_turn", len(self.provider_turns()) == requests + 1)
            self.finish(label)
        self.result["passed"] = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default="/tmp")
    args = parser.parse_args()
    args.watcher_window = 11
    canary = CompactionWrapperCanary(args)
    print(f"Wrapper compaction artifacts: {canary.root}", flush=True)
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
    raise SystemExit(main())
