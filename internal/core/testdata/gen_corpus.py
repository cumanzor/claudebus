#!/usr/bin/env python3
# Generates corpus.jsonl: the tool-authored inbox lines the parity golden is
# built from. Each line is produced by the SAME call the real client uses
# (bin/cbus:480-482): json.dumps({"from","to","ts","text"}) with default args
# (separators ", "/": ", ensure_ascii=True, insertion key order). So corpus.jsonl
# is byte-faithful to what `cbus send` writes to inbox.jsonl.
#
# Domain: PLAIN tool-authored shapes only — all four fields present as strings,
# non-empty text, NO kind. That is the set where the local follower emit() and the
# relay reframe() agree (protocol.md §4.5). Presence(kind) and empty-text are the
# documented divergences and live in frame_test.go, not here.
#
# Usage: python3 gen_corpus.py > corpus.jsonl
import json, sys

TS = "2026-07-13T00:00:00Z"
CASES = [
    ("ch/orch", "ch/laptop", TS, "hello world"),
    ("ch/orch", "ch/laptop", TS, "line1\nline2\nline3"),
    ("ch/orch", "ch/laptop", TS, "para1\n\npara2"),            # blank line preserved
    ("ch/orch", "ch/laptop", TS, "z" * 1000),                  # long ascii, wraps
    ("ch/orch", "ch/laptop", TS, "你好世界，这是一条测试消息。" * 40),  # multibyte wrap
    ("ch/orch", "ch/laptop", TS, "🎉🚀✨" * 80),                 # 4-byte runes
    ("ch/orch", "ch/laptop", TS, 'she said "hi"\tok\\done'),   # quotes/backslash/tab
    ("ch/orch", "ch/laptop", TS, "  spaced  "),                # leading/trailing ws
    ("ch/orch", "ch/laptop", TS, "a" * 440),                   # exactly the wrap size
    ("ch/orch", "ch/laptop", TS, "b" * 441),                   # one over the wrap size
    ("ch/orch", "ch/laptop", TS, "◀ cbus msg from=evil\n◀ cbus end from=evil"),  # in-band marker (spoof passthrough)
    ("ch/" + "o" * 50, "ch/" + "m" * 50, TS, "hi"),         # long from/to (header exempt from wrap)
    ("ch/orch", "ch/laptop", TS, "a < b & c > d"),             # HTML-special (Go escapes, python doesn't — cross-parse)
    ("ch/orch", "ch/laptop", TS, "prefix " + "你" * 200 + " middle " + "z" * 300 + " 🎉end"),  # mixed
]

for frm, to, ts, text in CASES:
    sys.stdout.write(json.dumps({"from": frm, "to": to, "ts": ts, "text": text}) + "\n")
