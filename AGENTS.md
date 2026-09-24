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

## Public content

This repository is public. Code, comments, tests, fixtures, docs, changelogs,
commit messages, tags, release notes and recordings are all written for any
reader, not for one operator's setup.

- Use the generic names in examples, fixtures and prose: hosts `laptop`,
  `server` and `winbox`, CCS profiles `alpha` and `beta`, channels such as
  `demo`. Never a real hostname, username, home path, profile or account name.
- Do not hardcode deployment details. Hosts, relay URLs, credentials and paths
  come from arguments, environment or config, never from a built-in default
  that points at one person's machines. The relay has no built-in hosts (each
  resolves through `CBUS_SITE_<HOST>_URL`) and `cbus auth status` requires its
  host argument; new code follows the same pattern.
- Name the operator's own tooling by its role: "the tracker", "a password
  manager", "a private network", "a Linux container runtime", "a dotfiles
  directory". Products cbus integrates with (Claude Code, Codex CLI, CCS,
  iTerm2, tmux) keep their names.
- New changelog entries, comments and commit messages say what changed and why.
  Leave out tracker and finding ids, local branch and worktree names, scratch paths,
  machine names, and account, plan or billing details.
- Security docs describe what the code does and how to configure it safely.
  They do not describe the exposure of a particular live deployment.
- Record demos and screenshots in a scratch environment: `CBUS_HOST` set, a
  scratch store and working directory, a minimal environment. Check every frame
  for names, paths and hostnames before committing, with a check that is
  independent of the tool that edited the frames.
- Before committing, search the diff for your own machine, account and tool
  names. Keep that list outside the repository.
