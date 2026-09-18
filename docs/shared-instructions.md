# Shared instructions and harness skills

[AGENTS.md](../AGENTS.md) holds repository policy once. Claude Code reads the
small [CLAUDE.md](../CLAUDE.md) import, while harnesses that support AGENTS.md can
read it directly. Keep connection and operational procedures in harness skills;
an instruction file does not provide message transport or configure permissions.

Claude Code 2.1.277 added native AGENTS.md discovery. Its default depends on the
absence of project Claude instruction files in the current directory and its
ancestors, and availability can differ with provider, telemetry and startup
configuration. The explicit import keeps the shared policy usable in those
cases and with older Claude Code versions. No global instruction setting needs
to change. See the [release](https://github.com/anthropics/claude-code/releases/tag/v2.1.277)
and [instruction-loading documentation](https://code.claude.com/docs/en/memory).
