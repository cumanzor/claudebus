#!/usr/bin/env python3
"""The frontend exit check must cover the CLI and nothing it merely started."""
import unittest

from codex_cli_resume_canary import frontend_processes


def row(pid, args):
    return {"pid": pid, "ppid": 1, "comm": "proc", "args": args}


class FrontendProcessesTest(unittest.TestCase):
    def test_keeps_cli_drops_backend_and_git_helpers(self):
        tree = [
            row(1, "node /opt/codex/bin/codex.js --no-alt-screen"),
            row(2, "/opt/codex/vendor/bin/codex --no-alt-screen"),
            row(3, "/opt/codex/bin/codex app-server --listen unix://"),
            row(4, "/usr/bin/git -c safe.bareRepository=explicit -C /work fetch origin"),
            row(5, "/usr/lib/git-core/git remote-https https://example.com/p.git"),
            row(6, "/usr/lib/git-core/git-remote-https https://example.com/p.git"),
        ]
        self.assertEqual([r["pid"] for r in frontend_processes(tree)], [1, 2])

    def test_git_in_a_path_or_argument_is_not_a_helper(self):
        tree = [
            row(1, "/home/me/git/codex/bin/codex --cwd /work/git"),
            row(2, "/opt/bin/github-codex fetch"),
            row(3, ""),
        ]
        self.assertEqual([r["pid"] for r in frontend_processes(tree)], [1, 2, 3])


if __name__ == "__main__":
    unittest.main()
