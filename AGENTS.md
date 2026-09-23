# Repository instructions

These rules are shared across coding harnesses. Harness-specific connection,
permission and collaboration procedures remain in their skills and commands.

## Delivery milestones

- Plan substantial work as small, independently testable and revertible changes.
  Define each milestone's outcome, dependencies, acceptance checks and rollback.
- Aim for 200–500 substantive changed lines per PR. Approaching 1,000 is a reason
  to split or replan. Report generated files, fixtures and lockfile churn separately.
- Commit coherent checked steps. Use one PR per milestone and GitHub milestones
  to group the effort. Name dependencies explicitly for stacked PRs; do not later
  combine reviewed milestones into one oversized commit.
- Keep the internal effort record in the tracker when working in the maintained project:
  one epic with direct milestone children, each with Now, Decisions, Findings,
  Open and Pointers. Update at milestone boundaries and before compaction. Use
  dated one-line notes for events; attach longer evidence. Keep internal IDs out
  of public PR prose. In worktrees, pass the project explicitly.
- Human review/merge and release/install are separate gates. A feature flag must
  name its owner, checks, enablement criteria and removal milestone; it does not
  excuse a large PR. Preserve unrelated work in dirty checkouts.

## Integration boundaries

- Target Claude Code, Codex CLI and OpenCode. Desktop harness clients are v2.
- Keep delivery terminal agnostic. iTerm2 and tmux are terminal backends, not
  harness identities; a future terminal host must not change the bus protocol.
- Bind to exact session/runtime identities. Never substitute a recent session,
  working directory, terminal focus or process name for the recipient identity.
- Distinguish submission, persisted receipt, completed work and current presence.
  Preserve uncertain attempts and avoid blind retries that could duplicate input.
- Daemons own idle waiting. Do not add periodic model turns, Monitor re-arming
  or model-driven liveness polling. Preserve each receiving session's permissions.
- Run checks appropriate to the change. Separate fixture tests, source tracing,
  actual harness acceptance and field evidence in reports; do not claim one
  proves another.
