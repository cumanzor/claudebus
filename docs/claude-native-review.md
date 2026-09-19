# Native Claude receive: review and acceptance

This work is prepared for review in the [Claude Code native receive milestone](https://github.com/cumanzor/claudebus/milestone/1).
It is not included in v0.12.2. Review, merging, release and installation are separate steps.

## Review order

Keep each component independently reviewable. Grouping these PRs supplies context;
it does not mean combining them into one large merge.

| Group | PRs in suggested order | Review focus |
| --- | --- | --- |
| Independent maintenance | [12](https://github.com/cumanzor/claudebus/pull/12), [16](https://github.com/cumanzor/claudebus/pull/16) | Codex canary/help corrections; shared AGENTS policy with a Claude compatibility import. |
| Inactive foundations | [13](https://github.com/cumanzor/claudebus/pull/13), [14](https://github.com/cumanzor/claudebus/pull/14), [15](https://github.com/cumanzor/claudebus/pull/15), [18](https://github.com/cumanzor/claudebus/pull/18), [19](https://github.com/cumanzor/claudebus/pull/19) | Harness boundary, process/socket ownership, receipt primitives, private credentials and exact caller identity. |
| Delivery and admission | [20](https://github.com/cumanzor/claudebus/pull/20) → [21](https://github.com/cumanzor/claudebus/pull/21) → [22](https://github.com/cumanzor/claudebus/pull/22) → [23](https://github.com/cumanzor/claudebus/pull/23) | Exact durable receipts, unresolved attempts without replay, native admission and reconnect. |
| User flow and lifecycle | [24](https://github.com/cumanzor/claudebus/pull/24), [25](https://github.com/cumanzor/claudebus/pull/25) including [37](https://github.com/cumanzor/claudebus/pull/37), [28](https://github.com/cumanzor/claudebus/pull/28) → [29](https://github.com/cumanzor/claudebus/pull/29) → [30](https://github.com/cumanzor/claudebus/pull/30) → [38](https://github.com/cumanzor/claudebus/pull/38) → [31](https://github.com/cumanzor/claudebus/pull/31) | In-session connect, reservations, current-session fencing, formations, prompts, commands, legacy migration and isolated child launch environments. |
| Reproducible native acceptance | [17](https://github.com/cumanzor/claudebus/pull/17) → [27](https://github.com/cumanzor/claudebus/pull/27) → [32](https://github.com/cumanzor/claudebus/pull/32) → [33](https://github.com/cumanzor/claudebus/pull/33), then [34](https://github.com/cumanzor/claudebus/pull/34) and [36](https://github.com/cumanzor/claudebus/pull/36) | Ordinary CLI receipt/reply, isolation, lifecycle, relay and mixed Claude/Codex self-join. |
| Optional Monitor workaround, last | Comparison [26](https://github.com/cumanzor/claudebus/pull/26) → evidence [40](https://github.com/cumanzor/claudebus/pull/40) → helper [35](https://github.com/cumanzor/claudebus/pull/35) | Reversible pinned-version helper, measured mechanism and explicit field limits. Native receive does not depend on it. |

Most components remain below 500 changed lines. Review fixes bring the transcript
trust milestone #19 to 571 lines and the operational documentation #31 to 697;
both retain small coherent commits. The optional stopgap package is about 520
lines. Public Monitor snapshots are separate in #40: 52
lines of explanation and 762 lines of generated JSON evidence/manifest. The guide
links immutable evidence directly; temporary run paths are provenance only.

Source prerequisites remain explicit: #24 depends on #22, #25 on #21, #28 on
#23, #38 on #30, and #34/#36 share the #33 acceptance helpers. The small #37
migration-message correction belongs with #25 so reservation integration retains
the actionable legacy-alias refusal from #22.

The native canaries use an unchanged wake-body extraction in #27. Monitor
configuration and comparison logic stays in #26, which lands after the native
stack. This ordering changes no final canary behavior; historical acceptance
remains attributed to the script and binary hashes that actually ran.

After prerequisites land, rebase/retarget the dependent component onto `main` and
check that its diff contains only that component. A squash-merged parent must not
appear again as a duplicate change. Recheck affected behavior after conflicts.

**Do not merge aggregate staging branches.** These frozen references supply
review bases where several component dependencies meet:

| Reference | Frozen source | Purpose |
| --- | --- | --- |
| `staging/claude-native-foundations` | `a06fd45` | Foundation assembly used by #20. |
| `staging/claude-native-adapters` | `accb4be` | Runtime/CLI/reservation/formation assembly used by #30 and #37. |
| `staging/claude-native-runtime` | `3e83348` | Earlier combined runtime and documentation snapshot, not a release or merge shortcut. |

## Runtime acceptance

The following uses actual ordinary CLI processes with isolated profiles, narrow
normal tool permissions and local scripted fake model providers. It measures the
transport and lifecycle, not paid-model reasoning or signed-in-account field use.

| Path | Result |
| --- | --- |
| Claude 2.1.277, macOS | Session-side Bash join, exact UUID receipt and real bus reply; busy delivery; hold/refuse preserve uncertainty. |
| Two Claude sessions | One daemon, different channels, both joined before sending; each received only its own message and replied. |
| Daemon restart and actual `--resume` | Received state/cursors survive; unresolved attempts are retained without replay or silent runtime takeover. |
| Same-process `/clear` | PID/socket remain, session changes; old binding stops, new session can join a fresh alias and reply. |
| First-connect daemon autostart | Actual Claude Bash starts an absent daemon; its environment keeps isolated configuration and excludes native credentials/session identity. |
| Claude 2.1.276, Linux | server session-side join, receipt and reply: 17 assertions and six cleanup checks passed. |
| Codex 0.154.0 regression | Presence/resume/reconnect/exit canary: 34 assertions and five cleanup checks passed. |
| Native Claude + Codex | Both self-joined through their own tools; request/reply/ACK in exact threads: 26 assertions and seven cleanup checks passed. |
| Claude through isolated relay | Two exact receipts/replies across daemon restart: 36 assertions and nine cleanup checks; 32 seconds idle with no extra model/auxiliary requests. |
| Native socket idle | 2,100 seconds with no extra main/auxiliary request, followed by exact-session wake and receipt. |
| Integrated cbus idle | 2,100.049 seconds with no extra main/auxiliary request, then an actual bus message woke the same Claude session, persisted the exact UUID and elicited a verified reply: 17 assertions and six cleanup checks passed. |

Mixed-runtime acceptance recorded five denied nonlocal proxy attempts. Inference
used only local fake providers; the test does not claim no outbound attempts.

The principal later native candidate was source `0456bed`:

- macOS SHA-256: `6dbcb7ff9008d3559043eb6d20b07ff0305f7727d37e495db11b79b3ef250875`
- Linux SHA-256: `fb5c4d0d98adf74febba147b7534242a036beec30e1ea0bd8138ef50d207c39f`

The native-only idle probe used committed canary `1ef9ba9`. The integrated idle
probe uses earlier runtime source `3d4eb1e`, macOS SHA-256
`f391116bf1c05dd96233a4740ad9f91d64f85fa957c767e767a27a1ee409e2a4`.
These are immutable test candidates, not release assets. Later source changes do
not inherit an earlier binary's exact-byte acceptance claim.

Independent landing review subsequently found a transcript trust gap in #19.
Repair `92f7856` rejects foreign ownership, group/other write permission and hard
links during capture and every bound reopen. The new filesystem regressions fail
on the original head and pass on the repair. Foreign-owner metadata is exercised;
changing actual file ownership is skipped when tests run without root.

After that repair, native runtime assembly `428eddf` passed a fresh ordinary
Claude 2.1.277 self-connect, exact receipt and reply canary: 17 assertions and six
cleanup checks. Its macOS binary SHA-256 is
`37b8079cc8149e7c7f9cf29cece3a149ebb9fb05c7c8fbf69694b4de8ec097b2`.
This used the Monitor-free runner and a local fake provider. It adds no new
signed-in-account, relay, reconnect or long-idle evidence.

The canaries record source/script/binary hashes, exact runtime/session evidence,
assertions and owned-process cleanup. Use their `--help` with explicitly supplied
candidate paths. Never point isolated probes at an existing user's bus store.

Independent review also found that the original autostart check could not exclude
the verifier starting the daemon before Claude's Bash command. Repair `521413c`
checks absence after verifier setup and before emitting that exact tool call,
then records the daemon identity after its successful tool result. The fault
fixture rejects early verifier startup and refuses to claim cleanup when a
process remains alive before its socket appears. Earlier green autostart results
alone do not establish this stronger attribution.

Relay landing review reproduced a different test race: the persisted checkpoint
could show a receipt before CLI status converged, producing an intermittent
failure. Repair `5b2d99a` waits on the same exact CLI receipt snapshot used by the
final assertions and records convergence time. Its 15-second timeout remains a
failure; this changes test observation, not delivery or retry behavior. A passing
run is evidence for that path, not a claim that timing failures are impossible.

The combined source `c301e0f` passed `go test ./...`, focused native/lifecycle/
launcher race checks and `go vet ./...`. Independent review found no additional
direct lifecycle or launcher blocker. These checks include the final launch
correction; earlier runtime canaries remain attributed to their own candidates.

The final launcher correction is covered by generated fresh/fork commands for
iTerm and tmux, default and explicit profiles, relative roots and inherited
identity removal. Those fixtures execute harmless shells; they do not claim new
GUI placement acceptance.

## Stopgap and remaining field gates

The controlled Monitor comparison on 2.1.277 used the same `persistent: true`,
two-second timeout request in both arms. With `tengu_breezy_crescent=true` it
expired; with false it remained alive across twelve seconds, accepted a later
signal and stopped only with TaskStop. The helper passed 27 fixture checks,
including 20 bounded revert-versus-replacement races.

That proves the mechanism in the local fake firstParty provider route. A real
signed-in-account before/after check remains open; default five-/30-minute limits
were not measured by that pair. Cached/session-pinned flags read the seed, while
blocking reads can return defaults. The workaround does not freeze every gate.

Native receive still needs ordinary signed-in/CCS field use after reviewed
installation. Legacy Monitor aliases require deliberate migration, and arbitrary
custom configuration roots cannot be recreated by automatic formation launch.

Network-loss/offline-spool injection, mixed-harness cross-machine relay and actual
terminal placement/formation UX are separate acceptance scopes. OpenCode and the
full cross-harness route matrix remain subsequent milestones. Desktop harness
clients remain v2; transport remains independent of iTerm2, tmux or a future host.
