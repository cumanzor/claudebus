# cbus formation cost analysis

Analysis date: 2026-08-17. Supersedes the 2026-08-17 first draft, which reached the
right lever for the wrong reasons and had a scope error that invalidated its headline.

Data: local Claude Code transcripts plus one codex rollout. Requests are deduped by
`requestId` (several transcript records share one API request; counting records
triple-counts). Trigger for the investigation: the Fable 5 subscription bucket on the
personal account filled about two days after reset.

## Read this before using any number here

**Accounts are separate buckets.** The first draft pooled the `personal` and `work` CCS
profiles into one total and concluded the burn was spread across four sessions in three
repos. Two of those sessions belong to the work account and cannot touch the personal
Fable bucket. Split by store before aggregating anything:

| store | Aug fable prompt tokens | active days | mean/day |
|---|---|---|---|
| `~/.ccs/instances/personal` | 1,167M | 15 | 78M |
| `~/.ccs/instances/work` | 1,221M | 16 | 76M |

**Raw prompt tokens overstate charged burn by roughly 9x.** Formation seats run at
98-99% cache read, which lists at 0.1x input. The reviewer seat below is 119M raw and
13.1M list-weighted input-equivalent. Every figure here is labelled raw or weighted.
Which one the subscription actually meters is unresolved and is not answerable from
transcripts.

**Dedupe does not remove retries.** A semantically retried request gets a new
`requestId`, so it survives dedupe as a distinct request. Counts here are an upper bound
on distinct model calls.

## What filled the bucket

One formation, one day.

Personal-account fable by day:

| date | fable prompt tokens (raw) | sessions |
|---|---|---|
| 08-14 | 36M | 4 |
| **08-15** | **210M** | 5 |
| 08-16 | 2M | 1 |
| 08-17 | 0 | 0 |

08-16 collapsing to 2M and 08-17 to zero is the bucket running out, against a 78M/day
August mean. 203M of the 210M (97%) is a single formation, `dancetrack-init`, with two
fable seats that started within 40 minutes of each other:

| seat | session | raw | weighted | turns | floor | peak ctx |
|---|---|---|---|---|---|---|
| `dancetrack-init/reviewer` | 60e256c1 | 118M | 13.1M | 339 | 22k | 653k |
| `dancetrack-init/orchestrator` | 2564ee12 | 89M | ~9M | 340 | 22k | 451k |

## Mechanism: cost is quadratic in turn count

Context grows about 1,860 tokens per turn on the reviewer and 1,260 on the orchestrator.
Every turn re-reads everything before it, so total cost is dominated by `growth * n^2/2`:

```
reviewer:  n*floor = 7.5M    growth*n^2/2 = 106.9M
           predicted 114.4M   actual 118M     -> 93% quadratic
orchestrator:                                 -> 91% quadratic
```

The consequence is visible in like-for-like work inside the one session. These are the
same class of task, a milestone closing-range verdict, at different depths:

| verdict | turns | ctx at wake | cost | cost/turn |
|---|---|---|---|---|
| M1 | 28 | 150k | 5.0M | 179k |
| M2 | 20 | 267k | 5.8M | 290k |
| M5 | 26 | 464k | 12.9M | 496k |
| M8 | 11 | 627k | 7.0M | **636k** |

M8 cost 3.6x more per turn than M1 for the same kind of work. The only variable is
session depth. The 8 closing verdicts are 61% of the session's 118M.

## There is no bloat, and no misbehaviour

Worth stating plainly because it rules out the obvious remedies. Total accumulated
content in the reviewer session is 0.99M chars. Tool output is 0.57M of that. Nothing
is being dumped into context.

The reviewer made 294 tool calls: 274 Bash, 11 Read. It was not doing coder work. The
orchestrator dispatched whole milestones (`M1 CLOSING RANGE for verdict:
20247ce..4bece1a`, and so on, 8 of them between 07:37 and 13:10 UTC at roughly 45
minute intervals), and the reviewer did the rigorous thing with each: enumerated the
range, read the changed files, then independently rebuilt environments to verify the
coder's per-step claims before returning `CONDITIONAL APPROVE` with binding findings.

Setup work is per-milestone rather than amortized (clone x7, build x8, pip x8,
download x7 spread across the 8 verdicts), and the reviewer tears down its own scratch
after each verdict ("scratch venvs, TrackEval clone, contact sheets and probe dirs
removed; shared out/ untouched").

So 339 turns is the real size of eight independent milestone verifications, not churn.

## Hypotheses tested and rejected

Each of these was either the first draft's recommendation or a plausible candidate. All
are measured, not argued.

| hypothesis | measurement | verdict |
|---|---|---|
| Presence join/leave events wake peers needlessly | 0 presence-woken requests in the burst; 7 presence notifications out of 161 across the pair, and they are the cheapest wakes | rejected |
| No-op wakes (woke, replayed context, no tool call) | 29% of wakes but 5% of tokens | real, minor |
| Deliveries should be coalesced | no batching exists today (1.0 messages per delivery); 41% of deliveries land within 120s of another, but folding them saves 3-9% because the wakes that cluster are the cheap ones | real, minor |
| Role-file preamble is too large; de-duplicate the doctrine block | the fixed floor is 44k median but only 17% of context cost; trimming 10k per wake saves 4%, and the doctrine block is about 3k so roughly 1% | rejected |
| Lower `autoCompactWindow` | auto-compact currently fires at about 998k, so nothing intervenes during the 22k to 653k ramp. Lowering it to 200-300k saves 48-61% | rejected as a remedy: it caps how large any single session may grow, which is a direct regression for long-horizon work |
| Move seats off fable | fable is 56% of pooled spend, and moving a seat relocates its consumption to a different bucket entirely | available but not recommended: the reviewer seat on fable is a deliberate ruling, and the lever below is larger and model-agnostic |

The first draft's presence figure (about $334, 3% of spend) was arithmetically fine and
answered the wrong question. It averaged presence cost across all context sizes, while
the hypothesis under test was specifically about presence wakes at large context. A
wake at 800k costs 7.5x one at 50k, so the average is not the right operand.

## The lever: retire and respawn worker seats at work boundaries

Same turns, same model, same 1M window. The only change is that the seat does not carry
one context across eight independent verdicts. Split at the 8 real verdict boundaries,
giving 9 sessions of `[54, 64, 31, 50, 25, 37, 27, 28, 16]` turns:

| handoff brief | raw | vs 118M | weighted | vs 13.1M |
|---|---|---|---|---|
| 6k | 22.2M | 81% less | 2.7M | 79% less |
| 20k | 26.9M | 77% less | 3.4M | 74% less |
| 50k | 36.8M | 69% less | 5.0M | 62% less |

Quote this as **62-79%, contingent on brief size**, not as a point estimate. Raw and
weighted move together because the fresh cache write is paid once on a small floor while
the reads it replaces scale with accumulated context.

Two constraints on where this applies:

**It is asymmetric across seats.** A reviewer, coder or tester has natural work
boundaries and is disposable at them. An orchestrator is not: continuity is its job, it
holds who is doing what and what was ruled, and a handoff would have to reconstruct
exactly that. Do not split the orchestrator.

The general principle: continuity should be a property of one seat, not all of them.
Today every seat accumulates as though it were the formation's memory.

**The handoff artifact mostly exists already.** The reviewer's verdict messages are
4.6-5.2k chars and carry the milestone, the exact commit range, binding findings with
`file:line` plus reproduction and fix, and fold items marked no-re-review. They are
already written for a reader who was not present, because they are addressed to the
orchestrator.

Two gaps before this can be automated:

1. Verdicts are one deep. "One binding item, first of the next milestone" chains M(n) to
   M(n+1) only, so a successor at M9 has no ledger of which findings from M4, M6 and M7
   are still open. Needs a cumulative open-findings ledger.
2. The scratchpad path is session-UUID scoped
   (`/private/tmp/claude-501/<proj>/<session-uuid>/scratchpad`), so artifacts do not
   survive a respawn. Moot for this seat because it tears down anyway, but it matters for
   any seat that should carry artifacts forward.

## What would falsify the 62-79%

Recorded because the estimate is a model fit, not a measurement.

- `growth = (peak - floor) / n` is a chord slope. It reproduces the observed total but
  does not identify what a restarted successor would actually consume.
- Equal splitting is a best case because it minimizes `sum(n_j^2)`. Real unequal verdict
  ranges cost more than the table shows.
- The estimate assumes a successor does not re-derive more than the incumbent did. The
  teardown behaviour proves filesystem setup is disposable. It does not prove cognitive
  discovery is disposable. Cross-range dependencies, omitted prior findings, unpinned
  base and head, and lost warm caches would all break it.
- If the subscription meters weighted rather than raw input, the absolute figures shrink
  about 9x while the ranking between seats is preserved, since all seats sit at 98-99%
  cache read.

## Next steps

1. Emit a per-request table: timestamp, seat, requestId, model, effort, uncached input,
   cache read, cache write, output, context size, wake cause, verdict segment, retry
   flag.
2. Run an exact per-request boundary replay rather than another curve fit: for each real
   request retain only content since the preceding verdict boundary, add the real fixed
   prefix plus a candidate brief and cumulative ledger, retokenize. Sensitivity grid over
   brief size (5k/20k/50k) and re-derivation (0/10/25%). Publish raw and weighted
   separately.
3. Add the cumulative open-findings ledger and a checkpoint validator. Do not enable
   automatic respawn until the validator passes.
4. Pilot as a crossover: persistent versus respawned reviewer across at least 3 matched
   milestones per arm. Primary metric is quota units per accepted verdict. Secondary:
   elapsed time, duplicate discovery, reopened or missed binding findings, correctness
   regressions.
5. Predeclare stop conditions: any missed binding finding, degraded verdict correctness,
   or restart overhead large enough to erase the measured benefit.
6. Leave the orchestrator continuous and `autoCompactWindow` unchanged.

## Method note on the review of this analysis

The adversarial review of these findings was done by a codex peer
(`cbus codex --channel cbus-triage --alias advisor`). Its review never arrived. `cbus send`
printed `sent to cbus-triage/main` and exited 0 while writing nothing, and the review had
to be recovered by reading the codex rollout by hand. Cause: `codexbridge.go:85` starts
every bridge-spawned peer with `sandbox: "read-only"` (hardcoded, no override in the
tree), and `appendInbox` (`presence.go:61`) discards both the open and write errors while
`LocalSend` returns success unconditionally. Tracked as `cbus-6ij.7`.

Two consequences worth carrying: a codex peer is currently one-way on the bus, which
blocks spreading formation seats across providers to relieve a single subscription
bucket; and its review, once recovered, changed this document materially, since the
raw-versus-weighted correction above came from it.
