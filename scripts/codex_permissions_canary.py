#!/usr/bin/env python3
"""Opt-in fresh exact-path reply-permission acceptance with an ordinary CLI.

All homes, rules, bus messages, provider requests and logs are isolated under /tmp.
A local fake Responses provider requests one real shell tool execution; no paid
inference is used. The ordinary CLI retains workspace-write/on-request settings.
The explicit cbus reply rule alone permits a send outside the writable workspace.
Observer-side connect is separate: this does not prove model selection of a skill.
"""
import argparse
import json
import os
import shlex
import sys
import uuid

from codex_cli_resume_canary import ResumeCanary
from codex_queue_lifecycle_canary import FakeProvider


class PermissionsCanary(ResumeCanary):
    def __init__(self, args):
        super().__init__(args)
        self.target = "cli-permissions/advisor"
        self.marker = "CBUS-PERMISSIONS-" + uuid.uuid4().hex
        self.reply_requested = False
        self.without_rule = args.without_rule
        self.result.update(proofLayer="ordinary CLI actual shell tool with fresh exact-path reply rule; local fake provider",
                           script="scripts/codex_permissions_canary.py")

    def prepare(self):
        super().prepare()
        self.provider.close()
        self.provider = FakeProvider(output=self.provider_output)
        config = (self.home / "config.toml").read_text()
        import re
        config = re.sub(r'http://127\.0\.0\.1:\d+/v1', f'http://127.0.0.1:{self.provider.server.server_port}/v1', config)
        config = config.replace('approval_policy = "never"', 'approval_policy = "on-request"')
        config = config.replace('sandbox_mode = "read-only"', 'sandbox_mode = "workspace-write"')
        config = config.replace('[model_providers.cbus_local_mock]',
                                '[sandbox_workspace_write]\nexclude_slash_tmp = true\nexclude_tmpdir_env_var = true\n'
                                '[model_providers.cbus_local_mock]')
        (self.home / "config.toml").write_text(config)
        # Default bus placement is outside the scratch workspace.
        self.bus = self.root / "user-home" / ".claude-bus"
        self.env.pop("CBUS_DIR", None)
        self.command(["install-codex-skills"])
        self.check("fresh_skill_installed", (self.home / "skills" / "cbus-connect" / "SKILL.md").is_file())
        if not self.without_rule:
            self.command(["codex-permissions", "--binary", self.cbus, "--install"])
            rules = self.home / "rules" / "cbus.rules"
            self.check("only_explicit_reply_rule_installed", list((self.home / "rules").glob("*.rules")) == [rules])
        else:
            self.check("negative_control_has_no_rules", not (self.home / "rules").exists())
        self.check("general_config_unchanged_by_install", (self.home / "config.toml").read_text() == config)

    def provider_output(self, number, body):
        if body.get("client_metadata", {}).get("thread_id") != self.thread or self.reply_requested:
            return []
        self.reply_requested = True
        names = {tool.get("name"): tool for tool in body.get("tools", [])}
        command = shlex.join([self.cbus, "send", "cli-permissions/verifier", "--force", "--from", self.target, self.marker])
        if "exec_command" in names:
            name, arguments = "exec_command", {"cmd": command, "yield_time_ms": 1000, "max_output_tokens": 2000}
        elif "shell_command" in names:
            name, arguments = "shell_command", {"command": command}
        else:
            raise AssertionError("expected ordinary CLI shell tool; got " + repr(sorted(str(k) for k in names)))
        self.result["requestedTool"] = {"name": name, "arguments": arguments}
        return [{"type": "function_call", "id": f"cbus-item-{number}", "call_id": f"cbus-call-{number}",
                 "name": name, "arguments": json.dumps(arguments)}]

    def run(self):
        self.prepare()
        self.command(["join", "cli-permissions", "verifier", "--session-id", "isolated-permissions-verifier"])
        self.start_cli(prompt="Local cbus permission acceptance probe")
        self.wait(lambda: bool(list(self.home.rglob("rollout-*.jsonl"))), "initial CLI transcript", 20)
        self.rollout = next(self.home.rglob("rollout-*.jsonl"))
        self.wait(lambda: any(record.get("type") == "session_meta" for record in self.entries()), "CLI identity")
        meta = next(record["payload"] for record in self.entries() if record.get("type") == "session_meta")
        self.thread = meta["id"]
        self.result.update(threadId=self.thread, source=meta.get("source"), codexVersion=meta.get("cli_version"))
        self.check("ordinary_cli_source", meta.get("source") == "cli")
        state = json.loads(self.command(["connect", "cli-permissions", "advisor", "--codex-sqlite-home", str(self.home), "--json"], recipient=True).stdout)
        self.check("exact_thread_connected", state["threadId"] == self.thread)
        self.wait(lambda: len(self.provider_turns()) >= 1, "initial fake provider request")
        self.provider.release(self.provider_turns()[0]["number"])
        self.wait(lambda: len(self.provider_turns()) >= 2, "shell tool result follow-up", 30)
        followup = self.provider_turns()[1]
        outputs = [item for item in followup["body"].get("input", []) if item.get("type") == "function_call_output"]
        if self.without_rule:
            self.check("no_rule_tool_denied_by_filesystem", any(any(reason in str(item.get("output", "")).lower()
                       for reason in ("operation not permitted", "permission denied")) for item in outputs))
            self.check("no_rule_tool_did_not_claim_success", not any("sent to cli-permissions/verifier" in str(item.get("output", "")) for item in outputs))
        else:
            self.check("actual_cli_tool_returned_success", any("sent to cli-permissions/verifier" in str(item.get("output", "")) for item in outputs))
        self.provider.release(followup["number"])
        self.wait(lambda: self.completed_count() >= 1, "reply turn completion")
        inbox = self.bus / "cli-permissions" / "verifier" / "inbox.jsonl"
        messages = [json.loads(line) for line in inbox.read_text().splitlines()]
        replies = [message for message in messages if message.get("text") == self.marker]
        if self.without_rule:
            self.check("no_rule_recipient_received_nothing", not replies)
        else:
            self.check("recipient_inbox_has_exactly_one_reply", len(replies) == 1 and replies[0]["from"] == self.target)
        contexts = [record["payload"] for record in self.entries() if record.get("type") == "turn_context"]
        self.check("workspace_write_approval_policy_preserved", bool(contexts) and all(
            item.get("approval_policy") == "on-request" and item.get("sandbox_policy", {}).get("type") == "workspace-write" for item in contexts))
        self.check("temporary_bus_is_outside_writable_policy", bool(contexts) and all(
            item.get("sandbox_policy", {}).get("exclude_slash_tmp") is True and
            item.get("sandbox_policy", {}).get("exclude_tmpdir_env_var") is True and
            not any(str(self.bus).startswith(root.rstrip("/") + "/") for root in item.get("sandbox_policy", {}).get("writable_roots", []))
            for item in contexts))
        self.check("tool_invocation_recorded_in_exact_thread", any(record.get("type") == "response_item" and record.get("payload", {}).get("type") == "function_call" and self.marker in record["payload"].get("arguments", "") for record in self.entries()))
        self.pump(.5)
        self.provider_turns()
        self.result["passed"] = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cbus", default=os.getenv("CBUS_TEST_BINARY"), required=not os.getenv("CBUS_TEST_BINARY"))
    parser.add_argument("--codex", default=os.getenv("CODEX_TEST_BINARY", "codex"))
    parser.add_argument("--temp-root", default="/tmp")
    parser.add_argument("--without-rule", action="store_true", help="negative control: expect actual shell permission denial and no reply")
    args = parser.parse_args()
    args.watcher_window = 11
    canary = PermissionsCanary(args)
    print(f"Permission canary artifacts: {canary.root}", flush=True)
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
