#!/usr/bin/env python3
"""Two ordinary Claude sessions, separate channels, one isolated cbus daemon.

Uses only the local fake provider from claude_interactive_wake_canary.py.
Requires --cbus, --cbus-sha256, and --cbus-revision for the exact candidate.
No live bus, installed daemon, account credentials, or global settings are used.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import uuid

from claude_cbus_canary import BusProbe


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--cbus-sha256", required=True)
    parser.add_argument("--cbus-revision", required=True)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="cbus-cc-pair-", dir=os.environ.get("CBUS_TEST_TMPDIR", "/tmp"))).resolve()
    home, work = root / "home", root / "project"
    home.mkdir()
    work.mkdir()
    owner = BusProbe(root, args, str(uuid.uuid4()), "unused-owner-marker")
    env = {"HOME": str(home), "PATH": os.environ["PATH"], "CBUS_DIR": str(owner.bus), "CBUS_UPDATE_CHECK": "0"}
    result = {"root": str(root), "checks": {}, "passed": False, "owner": owner.result,
              "scriptSHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    children, logs = [], []
    try:
        owner.start(env, work)
        fixture = root / "fixture.json"
        descriptor = {"kind": "claude-cbus-isolated-pair", "busRoot": str(owner.bus),
                      "binarySHA256": args.cbus_sha256, "health": owner.health, "ownerPID": os.getpid()}
        with fixture.open("x") as f:
            os.fchmod(f.fileno(), 0o600)
            json.dump(descriptor, f)
        script = Path(__file__).with_name("claude_interactive_wake_canary.py")
        child_env = os.environ.copy()
        child_env["CBUS_TEST_TMPDIR"] = str(root)
        argv = [sys.executable, str(script), "--transport", "cbus", "--cbus", str(owner.binary),
                "--cbus-sha256", args.cbus_sha256, "--cbus-revision", args.cbus_revision,
                "--cbus-shared-fixture", str(fixture)]
        for i in range(2):
            log = (root / f"child-{i}.log").open("wb")
            logs.append(log)
            children.append(subprocess.Popen(argv, stdout=log, stderr=log, env=child_env))
        deadline = time.monotonic() + 90
        for child in children:
            child.wait(timeout=max(1, deadline-time.monotonic()))
        sessions = [json.loads((root / f"child-{i}.log").read_text()) for i in range(2)]
        result["sessions"] = sessions
        result["checks"]["both_round_trips_pass"] = all(s["passed"] for s in sessions)
        result["checks"]["one_shared_daemon"] = {s["cbus"]["daemonPID"] for s in sessions} == {owner.process.pid}
        result["checks"]["distinct_exact_sessions_and_channels"] = len({s["sessionId"] for s in sessions}) == len({s["cbus"]["channel"] for s in sessions}) == len({s["pid"] for s in sessions}) == 2
        ready, sent = [], []
        for i, session in enumerate(sessions):
            peer = sessions[1-i]
            requests = json.loads((Path(session["root"]) / "provider-requests.json").read_text())
            main = [r for r in requests if r.get("mainRequest")]
            ready.append(main[1]["at"])
            sent.append(next(e["at"] for e in session["timeline"] if e["event"] == "external_cbus_send"))
            text = json.dumps([r["body"].get("messages", []) for r in main])
            result["checks"][f"session_{i}_never_received_foreign_marker"] = peer["markers"]["signal"] not in text
            result["checks"][f"session_{i}_received_only_own_ACK"] = all(r["text"] == session["cbus"]["replyMarker"] for r in session["cbus"]["acknowledgments"])
        result["checks"]["both_connected_before_either_send"] = max(ready) < min(sent)
        result["passed"] = all(result["checks"].values())
    except Exception as error:
        result["error"] = repr(error)
    finally:
        for child in children:
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=5)
        for log in logs:
            log.close()
        result["cleanup"] = owner.cleanup()
        result["cleanup"]["childrenExited"] = all(child.poll() is not None for child in children)
        result["passed"] = result["passed"] and all(result["cleanup"].values())
        (root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps({"root": str(root), "passed": result["passed"], "checks": result["checks"], "error": result.get("error"), "cleanup": result["cleanup"]}, indent=2))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
