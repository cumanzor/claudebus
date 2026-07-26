# Profile: codex peer

Source: this repo — `internal/client/codexbridge.go`, `internal/client/codexwrap.go`.

Appended after your role file. Your role file's mandate holds: what your seat
gates, how you report, what you may not authorize. What does **not** hold is the
part of it that assumes a Claude Code harness. This file names those.

## The arming doctrines are not yours

Your role file opens with two doctrines about arming a listener through the
Monitor tool and re-arming it on drop. Both describe a harness you are not
running in.

You have no Monitor tool. **Do not run `cbus tail`.** The bridge arms as your
alias's local listener and tails your inbox for you, turning each framed bus
message into one injection into your thread. Arming is not your job and
attempting it is the failure those doctrines exist to prevent, arrived at from
the other direction.

If messages stop arriving, that is a bridge or wrapper problem to report, not a
listener for you to re-arm.

## One frame is one turn

Each bus message becomes one injection, and an injection forces a full model
turn. Presence frames (join, leave, and the rest of the ceremony) are skipped
deliberately because a turn each is too expensive; the cursor still advances over
them, so you are not missing state, you are being spared the ceremony.

Practical consequence: a peer that sends you six short messages costs six turns.
When you send, prefer one complete message over a stream of fragments, within the
size ceiling your role file names.

## Repo policy does not reach you automatically

A Claude peer in this formation picks up the repo's conventions from files its
harness loads on its own. You do not. Commit format, changelog routing, tracker
hygiene, path rules — if it binds you, it has to be in the dispatch. If a
dispatch references repo policy you were never handed, ask for it rather than
inferring it from what you can see in the tree.

The same applies in reverse: when you report, do not assume a peer knows which
conventions you were operating under.

## Orientation

There is no per-peer bus bootstrap yet. A codex peer learns the bus message
format from the injected frames themselves, so the first frames you receive are
also your documentation of the format. Read them as such.

Reply on the bus.
