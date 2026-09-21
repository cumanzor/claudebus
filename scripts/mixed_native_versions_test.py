#!/usr/bin/env python3
"""Exercise explicit mixed-canary version pins without starting any fixture."""
from pathlib import Path
import unittest
from unittest.mock import Mock, patch

from mixed_native_local_canary import MixedCanary, parse_args


class PreparedBoundary(Exception):
    pass


class MixedVersionTest(unittest.TestCase):
    def canary(self, *flags):
        args = parse_args(["--cbus", "/unused/cbus", "--cbus-sha256", "unused",
                           "--cbus-revision", "unused", "--codex", "/unused/codex",
                           "--claude", "/unused/claude", *flags])
        canary = MixedCanary.__new__(MixedCanary)
        canary.args, canary.codex = args, args.codex
        canary.root, canary.home = Path("/unused/root"), Path("/unused/root/codex")
        canary.env = {"PATH": "/unused/bin"}
        canary.result = {"checks": {}}
        canary.prepare = Mock(side_effect=PreparedBoundary)
        return canary

    def run_versions(self, canary, codex="0.155.1", claude="2.1.278"):
        with patch("mixed_native_local_canary.subprocess.check_output", side_effect=[
                f"codex-cli {codex}\n", f"{claude} (Claude Code)\n"]):
            canary.run()

    def test_historical_defaults_refuse_new_runtime_before_prepare(self):
        canary = self.canary()
        with self.assertRaisesRegex(AssertionError, "installed_codex_version_matches_expected"):
            self.run_versions(canary)
        canary.prepare.assert_not_called()
        self.assertEqual(canary.result["expectedVersions"], {"codex": "0.154.0", "claude": "2.1.277"})
        self.assertEqual(canary.result["versions"]["codex"], "codex-cli 0.155.1")

    def test_historical_defaults_still_accept_historical_versions(self):
        canary = self.canary()
        with self.assertRaises(PreparedBoundary):
            self.run_versions(canary, "0.154.0", "2.1.277")
        canary.prepare.assert_called_once_with()

    def test_explicit_versions_parse_and_reach_prepare(self):
        canary = self.canary("--expected-codex-version", "0.155.1", "--expected-claude-version", "2.1.278")
        with self.assertRaises(PreparedBoundary):
            self.run_versions(canary)
        canary.prepare.assert_called_once_with()
        self.assertEqual(canary.result["expectedVersions"], {"codex": "0.155.1", "claude": "2.1.278"})
        self.assertTrue(all(canary.result["checks"].values()))

    def test_each_wrong_explicit_version_refuses_before_prepare(self):
        for codex, claude, failed in (("0.155.2", "2.1.278", "codex"), ("0.155.1", "2.1.279", "claude")):
            with self.subTest(failed=failed):
                canary = self.canary("--expected-codex-version", codex, "--expected-claude-version", claude)
                with self.assertRaisesRegex(AssertionError, f"installed_{failed}_version_matches_expected"):
                    self.run_versions(canary)
                canary.prepare.assert_not_called()

    def test_rollout_requires_same_configured_pin_and_cli_source(self):
        canary = self.canary("--expected-codex-version", "0.155.1")
        with self.assertRaisesRegex(AssertionError, "codex_rollout_version_matches_expected"):
            canary.check_codex_identity({"source": "cli", "cli_version": "0.154.0"})
        with self.assertRaisesRegex(AssertionError, "ordinary_codex_cli"):
            canary.check_codex_identity({"source": "exec", "cli_version": "0.155.1"})
        canary.check_codex_identity({"source": "cli", "cli_version": "0.155.1"})
        self.assertEqual(canary.result["codexRolloutVersion"], "0.155.1")
        self.assertTrue(all(canary.result["checks"].values()))


if __name__ == "__main__":
    unittest.main()
