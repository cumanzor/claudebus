#!/usr/bin/env python3
# Generates corpus_local.jsonl: presence(kind)-bearing inbox lines for the LOCAL
# follower golden (core.LocalEmit). The local follower KEEPS `kind=` in the header
# (protocol.md §4.5) — the class the relay reframe() drops and the plain corpus
# therefore excludes. These lines exercise that kept-kind rendering plus body wrap
# UNDER a kind header. The parity-domain (no-kind) golden is the existing
# corpus.jsonl / corpus.golden, reused by the LocalEmit parity test.
#
# Shape from bin/cbus:341-343 (BroadcastPresence): {from,to,ts,kind,event,text}.
# `event` is stored but never rendered (emit reads only from/to/ts/text/kind) — its
# presence here proves it is ignored. Lines use python json.dumps (default args),
# byte-faithful to a bash-written presence line; a canonical-Go presence line frames
# identically (D8 presence cross-parse), asserted separately in the Go test.
#
# Usage:
#   python3 gen_corpus_local.py > corpus_local.jsonl
#   python3 emit_golden.py      < corpus_local.jsonl > corpus_local.golden
import json, sys

TS = "2026-07-13T00:00:00Z"
CASES = [
    ("ch/orch", "ch/mbp", TS, "presence", "join", "joined ch as orch"),
    ("ch/orch", "ch/mbp", TS, "presence", "leave", "left ch"),
    ("ch/orch", "ch/mbp", TS, "presence", "departed", "departed (listener gone)"),
    ("ch/orch", "ch/mbp", TS, "presence", "rename", "renamed orch -> lead"),
    ("ch/orch", "ch/mbp", TS, "presence", "join", "line1\nline2\nline3"),          # multi-line body under a kind header
    ("ch/orch", "ch/mbp", TS, "presence", "join", "joined 🎉 " + "你" * 200),        # multibyte wrap under a kind header
    ("ch/orch", "ch/mbp", TS, "presence", "join", "x" * 1000),                     # long body wraps under a kind header
    ("ch/" + "o" * 50, "ch/" + "m" * 50, TS, "presence", "join", "hi"),            # long from/to + kind (header exempt from wrap)
]

for frm, to, ts, kind, event, text in CASES:
    sys.stdout.write(json.dumps(
        {"from": frm, "to": to, "ts": ts, "kind": kind, "event": event, "text": text}) + "\n")
