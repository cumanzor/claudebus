#!/usr/bin/env python3
"""Filesystem-denial portability fixtures; no runtime, account or model calls."""
import json
import unittest

from codex_permissions_canary import filesystem_denial


def tool_output(code, output):
    return f"Chunk ID: fixture\nWall time: 0.0000 seconds\nProcess exited with code {code}\nOriginal token count: 57\nOutput:\n{output}\n"


class FilesystemDenialTest(unittest.TestCase):
    def test_completed_os_denials(self):
        for reason in ("operation not permitted", "permission denied", "read-only file system"):
            with self.subTest(reason=reason):
                self.assertTrue(filesystem_denial(tool_output(1, "cbus: open peer lock: " + reason)))

    def test_structured_shell_denial(self):
        for payload in ({"output": "cbus: read-only file system", "metadata": {"exit_code": 1}},
                        {"output": "cbus: permission denied", "exit_code": 1}):
            self.assertTrue(filesystem_denial(json.dumps(payload)))

    def test_success_cannot_pass_by_echoing_denial_or_exit_words(self):
        for diagnostic in ("cbus: read-only file system", "permission denied\nProcess exited with code 1"):
            self.assertFalse(filesystem_denial(tool_output(0, diagnostic)))
            self.assertFalse(filesystem_denial(json.dumps({"output": diagnostic, "metadata": {"exit_code": 0}})))
        self.assertFalse(filesystem_denial(tool_output(1, "sent to cli-permissions/verifier\npermission denied")))

    def test_unrelated_error_or_unfinished_tool_does_not_prove_denial(self):
        for text in (tool_output(1, "cbus: no such file or directory"),
                     tool_output(127, "command not found"), "permission denied",
                     "Process running with session ID 123\nOutput:\npermission denied\n",
                     json.dumps({"output": "permission denied"}),
                     json.dumps({"output": "permission denied", "metadata": {"exit_code": True}})):
            with self.subTest(text=text):
                self.assertFalse(filesystem_denial(text))


if __name__ == "__main__":
    unittest.main()
