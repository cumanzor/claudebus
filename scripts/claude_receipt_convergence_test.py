#!/usr/bin/env python3
"""Regression fixtures for a persisted receipt ahead of authoritative CLI status."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from claude_cbus_canary import BusProbe


class ReceiptConvergenceTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cbus-receipt-convergence-")
        self.addCleanup(self.temporary.cleanup)
        self.probe = BusProbe.__new__(BusProbe)
        self.probe.root = Path(self.temporary.name)
        self.probe.bus = self.probe.root / "bus"
        self.probe.session, self.probe.marker = "session-two", "unique-message-two"
        self.probe.target, self.probe.sender = "fixture/receiver", "fixture/verifier"
        self.probe.ack = "unique-ack-two"
        self.probe.connection = {"id": "connection-one"}
        self.probe.result = {"commands": []}
        self.ready = {"id": "connection-one", "harness": "claude", "threadId": "session-two",
                      "state": "socket-ready", "accepted": 2,
                      "lastAccepted": {"state": "received", "itemId": "uuid-two"},
                      "claude": {"binding": {"SessionID": "session-two", "Endpoint": {"PID": 123}}}}
        checkpoint = self.probe.bus / ".daemon/connections/connection-one.json"
        checkpoint.parent.mkdir(parents=True)
        checkpoint.write_text(json.dumps(self.ready))
        self.rows = [{"type": "user", "sessionId": "session-two", "uuid": "uuid-two",
                      "message": {"content": "unique-message-two"}}]
        self.current = copy.deepcopy(self.ready)
        self.samples = 0
        def status():
            self.samples += 1
            return self.current
        self.probe.status = status
        self.probe.acknowledgments = lambda: [{"from": self.probe.target, "to": self.probe.sender, "text": self.probe.ack}]

    def test_ready_checkpoint_cannot_bypass_stale_cli_status(self):
        self.current.update(state="uncertain", accepted=1, pending={"itemId": "uuid-two"})
        self.current["lastAccepted"]["itemId"] = "uuid-one"
        self.assertFalse(self.probe.receipt_ready(self.rows, 2))
        self.assertIsNone(self.probe.receipt_snapshot)
        with self.assertRaisesRegex(RuntimeError, "no converged CLI receipt snapshot"):
            self.probe.check_receipt(self.rows, 123)
        self.assertEqual(self.samples, 1)

    def test_all_receipt_conditions_must_converge(self):
        for delta in ({"state": "uncertain"}, {"accepted": 1}, {"threadId": "session-one"},
                      {"lastAccepted": {"state": "submitted", "itemId": "uuid-two"}},
                      {"lastAccepted": {"state": "received", "itemId": "uuid-one"}}):
            self.current = {**copy.deepcopy(self.ready), **delta}
            self.assertFalse(self.probe.receipt_ready(self.rows, 2), delta)
        self.current = copy.deepcopy(self.ready)
        for rows in ([], self.rows * 2, [{**self.rows[0], "sessionId": "session-one"}],
                     [{**self.rows[0], "message": {"content": "different-message"}}]):
            self.assertFalse(self.probe.receipt_ready(rows, 2), rows)
        self.assertTrue(self.probe.receipt_ready(self.rows, 2))
        evidence = self.probe.result["receiptConvergence"]
        self.assertEqual(len(evidence), 1)
        self.assertEqual(evidence[0]["samples"], 10)
        self.assertGreaterEqual(evidence[0]["elapsedSeconds"], 0)

    def test_final_checks_use_same_immutable_snapshot(self):
        self.assertTrue(self.probe.receipt_ready(self.rows, 2))
        self.current.update(state="uncertain", accepted=1)
        self.current["lastAccepted"]["itemId"] = "uuid-one"
        self.assertTrue(all(self.probe.check_receipt(self.rows, 123).values()))
        self.assertEqual(self.samples, 1, "final evidence must not resample status")
        self.assertEqual(self.probe.result["received"]["lastAccepted"]["itemId"], "uuid-two")
        self.probe.marker = "next-message"
        with self.assertRaisesRegex(RuntimeError, "no converged CLI receipt snapshot"):
            self.probe.check_receipt(self.rows, 123)


if __name__ == "__main__":
    unittest.main()
