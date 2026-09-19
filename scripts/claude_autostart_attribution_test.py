#!/usr/bin/env python3
"""Fault-inject verifier startup into a private cbus autostart canary fixture.

Requires the same --cbus, --cbus-sha256 and --cbus-revision as the live canary.
No Claude/model calls: each case starts and stops only its frozen fixture binary.
"""
import argparse
import json
import os
import subprocess
import time
from pathlib import Path
import tempfile
import uuid

from claude_cbus_canary import BusProbe


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--cbus-sha256", required=True)
    parser.add_argument("--cbus-revision", required=True)
    args = parser.parse_args()
    args.cbus_case = "autostart"
    root = Path(tempfile.mkdtemp(prefix="cbus-autostart-attribution-", dir="/tmp")).resolve()
    result = {"root": str(root), "cases": [], "passed": False}
    for phase in ("after-verifier-join", "before-bash-connect-emission", "before-socket-publication"):
        case_root = root / phase
        case_root.mkdir()
        home, work = case_root / "home", case_root / "project"
        home.mkdir()
        work.mkdir()
        probe = BusProbe(case_root, args, str(uuid.uuid4()), "unused")
        env = {"HOME": str(home), "PATH": os.environ["PATH"],
               "CBUS_DIR": str(probe.bus), "CBUS_UPDATE_CHECK": "0"}
        command = probe.command
        def injected_command(argv):
            response = command(argv)
            if argv[0] == "join":
                command(["daemon", "start", "--json"])
            return response
        if phase == "after-verifier-join":
            probe.command = injected_command
        case = {"phase": phase, "probe": probe.result, "rejectedAtRequiredPhase": False}
        initializing = None
        try:
            probe.start(env, work)
            if phase == "before-bash-connect-emission":
                command(["daemon", "start", "--json"])
                probe.before_bash_connect("toolu_cbus_wake")
            if phase == "before-socket-publication":
                # Hold this owned child in journal loading, before it binds its socket.
                connections = probe.bus / ".daemon/connections"
                connections.mkdir(parents=True)
                os.mkfifo(connections / "blocked.json", 0o600)
                initializing = subprocess.Popen([str(probe.binary), "daemon", "serve"], cwd=work,
                                                env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                deadline = time.monotonic() + 5
                while not (probe.bus / ".daemon/lock").exists():
                    if initializing.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError("injected daemon did not begin initialization")
                    time.sleep(.05)
                probe.assert_daemon_absent(phase)
        except Exception as error:
            case["error"] = str(error)
            case["rejectedAtRequiredPhase"] = str(error) == "unexpected fixture daemon at " + phase
        finally:
            case["cleanup"] = probe.cleanup()
            if initializing is not None:
                # This test owns the Popen handle; the canary must not claim it exited.
                case["cleanupRefusedUnverifiedProcess"] = case["cleanup"].get("daemonExited") is False and initializing.poll() is None
                initializing.kill()
                initializing.wait(timeout=5)
                case["ownedInjectedProcessReaped"] = initializing.poll() is not None
        case["noBashAutostartCredit"] = not probe.result.get("autostartObservedAfterBash", False)
        cleanup_passed = (case["cleanupRefusedUnverifiedProcess"] and case["ownedInjectedProcessReaped"]
                          if initializing is not None else all(case["cleanup"].values()))
        case["passed"] = case["rejectedAtRequiredPhase"] and case["noBashAutostartCredit"] and cleanup_passed
        result["cases"].append(case)
    result["passed"] = all(case["passed"] for case in result["cases"])
    (root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"root": str(root), "passed": result["passed"],
                      "cases": [{k: v for k, v in case.items() if k != "probe"} for case in result["cases"]]}, indent=2))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
