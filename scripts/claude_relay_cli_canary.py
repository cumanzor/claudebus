#!/usr/bin/env python3
"""Ordinary Claude PTY, local fake provider and real isolated durable relay.

Reuses the committed native Claude receive harness and relay protocol observer.
The model emits real Bash connect/send calls; no observer-side connect, paid
provider, public relay, installed service or existing credentials are used.
Default case verifies daemon restart and a second exact-UUID relay round trip.
This does not simulate network loss or mail sent during daemon downtime.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import sys
import time
import urllib.request
import uuid

import claude_interactive_wake_canary as native
from claude_cbus_canary import BusProbe
from codex_relay_cli_canary import Tester


class RelayBusProbe(BusProbe):
    options = None
    instances = []

    def __init__(self, root, args, session, marker):
        super().__init__(root, args, session, marker)
        self.instances.append(self)
        self.channel = "relay-native@offline"
        self.target, self.sender = self.channel + "/receiver", self.channel + "/tester"
        self.connect_command = shlex.join([str(self.binary), "connect", self.channel, "receiver", "--json"])
        self.reply_command = shlex.join([str(self.binary), "send", self.sender, self.ack])
        self.second_reply_command = shlex.join([str(self.binary), "send", self.sender, self.second_ack])
        self.inbox = root / "verifier-messages.jsonl"
        self.inbox.write_text("")
        self.relay_binary = root / "cbus-relay"
        shutil.copy2(Path(self.options.relay).resolve(strict=True), self.relay_binary)
        relay_hash = hashlib.sha256(self.relay_binary.read_bytes()).hexdigest()
        if relay_hash != self.options.relay_sha256:
            raise ValueError("relay binary does not match declared SHA-256")
        self.relay = self.relay_log = self.tester = None
        self.result.update(channel=self.channel, target=self.target, sender=self.sender,
                           relayBinary=str(self.relay_binary), relaySHA256=relay_hash,
                           relaySourceRevision=self.options.cbus_revision)

    def start(self, env, work):
        # Main passes this same environment to the actual Claude child. All
        # credential resolution is scoped to its scratch HOME and scratch PATH.
        token = "isolated-relay-" + uuid.uuid4().hex
        credential_dir = Path(env["HOME"]) / ".config" / "cbus" / "offline"
        credential_dir.mkdir(mode=0o700, parents=True)
        credential = credential_dir / "token"
        credential.write_text(token)
        credential.chmod(0o600)
        shim_dir = self.root / "relay-bin"
        shim_dir.mkdir(mode=0o700)
        security = shim_dir / "security"
        security.write_text("#!/bin/sh\n" +
                            'if [ "$*" = "find-generic-password -s cbus-relay-offline -a token -w" ]; then\n' +
                            "  printf '%s\\n' " + shlex.quote(token) + "\nelse\n  exit 1\nfi\n")
        security.chmod(0o700)
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env.update(PATH=str(shim_dir) + os.pathsep + env["PATH"],
                   XDG_CONFIG_HOME=str(Path(env["HOME"]) / ".config"),
                   CBUS_RELAY_LOCAL_URL=base, CBUS_SITE_OFFLINE_URL=base)
        self.env, self.work = env.copy(), work
        relay_env = env.copy()
        relay_env["CBUS_RELAY_TOKEN"] = token
        self.relay_log = (self.root / "relay.log").open("wb")
        self.relay = subprocess.Popen([str(self.relay_binary), "-listen", f"127.0.0.1:{port}",
                                       "-spool", str(self.root / "relay-spool"),
                                       "-token-file", str(self.root / "unused-token-file")],
                                      cwd=work, env=relay_env, stdout=self.relay_log,
                                      stderr=self.relay_log, start_new_session=True)
        deadline = time.monotonic() + 10
        while True:
            try:
                with urllib.request.urlopen(base + "/healthz", timeout=.3) as response:
                    if response.read().strip() == b"ok":
                        break
            except Exception:
                pass
            if self.relay.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("isolated relay did not become healthy")
            time.sleep(.05)
        self.tester = Tester(port, token, self.root / "relay-verifier-frames.jsonl")
        self.result["relayPID"] = self.relay.pid
        self.result["relayBase"] = base
        self.result["version"] = self.command(["--version"]).strip()
        self.log = (self.root / "daemon.log").open("wb")
        self.process = subprocess.Popen([str(self.binary), "daemon", "serve"], cwd=work,
                                        env=env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 10
        while not (self.bus / ".daemon/control.sock").exists():
            if self.process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("isolated daemon did not start")
            time.sleep(.05)
        self.health = json.loads(self.command(["daemon", "status", "--json"]))
        if self.health["pid"] != self.process.pid:
            raise RuntimeError("isolated daemon identity mismatch")
        self.result["daemonPID"] = self.process.pid

    def acknowledgments(self):
        if self.tester is None:
            return []
        with self.tester.lock:
            messages = [frame["message"] for frame in self.tester.messages]
        # Native harness expects a plain-message verifier artifact. Keep the
        # original fsynced durable frames separately as transport evidence.
        self.inbox.write_text("".join(json.dumps(message) + "\n" for message in messages))
        return [message for message in messages if message.get("text") == self.ack]

    def check_receipt(self, records, pid):
        checks = super().check_receipt(records, pid)
        current = self.result["received"]
        acks = self.acknowledgments()
        # The relay's recipient is wire-local channel/alias; the sender keeps
        # its originating @host address so a remote peer can reply correctly.
        checks["cbus_round_trip_ack_exact_sender"] = (len(acks) == 1 and
            acks[0].get("from") == self.target and acks[0].get("to") == "relay-native/tester")
        self.result["testerPings"] = self.tester.pings
        checks.update({
            "relay_subscription_identity": current.get("relay", {}).get("host") == "offline",
            "relay_verified_round_trip": self.tester.count(text=self.ack) == 1,
            "relay_reconnect_does_not_fake_consumer_join": self.tester.count(event="join") == 1,
            "relay_observer_protocol_valid": not self.tester.errors,
        })
        return checks

    def cleanup(self):
        clean = super().cleanup()
        if self.tester is not None:
            self.acknowledgments()
            self.result["testerPings"] = self.tester.pings
            self.result["testerErrors"] = self.tester.errors
            self.tester.close()
            clean["relayObserverStopped"] = not self.tester.thread.is_alive()
            clean["relayObserverProtocolValid"] = not self.tester.errors
        if self.relay is not None:
            if self.relay.poll() is None:
                self.relay.terminate()
                try:
                    self.relay.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    self.relay.kill()
                    self.relay.wait(timeout=5)
            clean["relayExited"] = self.relay.poll() is not None
        if self.relay_log is not None:
            self.relay_log.close()
        return clean


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", required=True)
    parser.add_argument("--cbus-sha256", required=True)
    parser.add_argument("--cbus-revision", required=True)
    parser.add_argument("--relay", required=True)
    parser.add_argument("--relay-sha256", required=True)
    parser.add_argument("--idle-seconds", type=float, default=32)
    parser.add_argument("--case", choices=("accepted", "restart-received"), default="restart-received")
    args = parser.parse_args()
    RelayBusProbe.options = args
    native.BusProbe = RelayBusProbe
    sys.argv = [__file__, "--transport", "cbus", "--cbus", args.cbus,
                "--cbus-sha256", args.cbus_sha256, "--cbus-revision", args.cbus_revision,
                "--cbus-case", args.case, "--idle-seconds", str(args.idle_seconds)]
    result_code = native.main()
    if not RelayBusProbe.instances:
        return result_code
    probe = RelayBusProbe.instances[-1]
    path = probe.root / "result.json"
    result = json.loads(path.read_text())
    result["relayCanarySHA256"] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    result["reusedSupportSHA256"] = {
        name: hashlib.sha256(Path(sys.modules[name].__file__).read_bytes()).hexdigest()
        for name in ("claude_cbus_canary", "codex_relay_cli_canary")
    }
    result["limitations"] = [
        "Real native CLI and relay with deterministic local fake provider; not paid-model or real-account field proof.",
        "Same-process daemon restart plus subsequent relay roundtrip; no network-loss or mail-during-downtime simulation.",
        "Native sockets, transcript receipt and full bus reply are checked; model quality is not assessed.",
    ]
    result["checks"]["relay_keepalive_observed"] = probe.result.get("testerPings", 0) >= 1
    result["passed"] = result["passed"] and all(result["checks"].values())
    path.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"result": str(path), "passed": result["passed"]}), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
