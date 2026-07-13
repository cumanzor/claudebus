#!/usr/bin/env python3
# GOLDEN GENERATOR — the local follower's framer, lifted VERBATIM from
# bin/cbus:515-551 (HEAD f213e26). wrap() and emit() below are byte-for-byte the
# follower's own code; only the stdin driver at the bottom is added. This IS the
# corpus of record for the Go framer parity tests: it reads tool-authored inbox
# lines on stdin and writes each framed delivery block exactly as `cbus tail`
# streams to its stdout (frame bytes + one trailing '\n' per message).
#
# DO NOT edit wrap()/emit(): they must stay identical to bin/cbus so the golden
# reflects the real follower. The lift's fidelity to the LIVE follower (env /
# encoding / locale drift) is separately proven by a one-time capture recorded in
# golden_test.go's header.
#
# Usage: python3 emit_golden.py < corpus.jsonl > corpus.golden
import sys, json

# --- BEGIN verbatim lift: bin/cbus:517-551 — byte-exact modulo ONE declared
# omission, line 521 `inbox, start = sys.argv[1], sys.argv[2]` (follower runtime
# args, structurally N/A to a stdin driver). Everything else here is character-exact
# with the follower, including the U+25C0 marker escapes. --------------
try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass
BYTES = 440
def wrap(seg):
    out, cur, n = [], [], 0
    for c in seg:
        b = len(c.encode("utf-8"))
        if n + b > BYTES and cur:
            out.append("".join(cur)); cur, n = [], 0
        cur.append(c); n += b
    out.append("".join(cur))
    return out
def emit(line):
    line = line.rstrip("\n")
    if not line:
        return
    try:
        m = json.loads(line)
    except Exception:
        sys.stdout.write(line + "\n"); sys.stdout.flush(); return
    if not (isinstance(m, dict) and "text" in m):
        sys.stdout.write(line + "\n"); sys.stdout.flush(); return
    frm = str(m.get("from", "?")); to = str(m.get("to", "?"))
    ts = str(m.get("ts", "")); kind = m.get("kind")
    head = "\u25c0 cbus msg from=%s to=%s ts=%s" % (frm, to, ts)
    if kind: head += " kind=%s" % kind
    body = []
    for seg in str(m.get("text", "")).split("\n"):
        body.extend(wrap(seg) if seg else [""])
    end = "\u25c0 cbus end from=%s" % frm
    sys.stdout.write("\n".join([head] + body + [end]) + "\n")
    sys.stdout.flush()
# --- END verbatim lift -------------------------------------------------------

# stdin driver (NOT part of the follower; feeds complete lines, equivalent to the
# follower's pend-buffered readline loop for a corpus of newline-terminated lines).
# Reconfigure stdin too (driver code, not the lift) so a C-locale regen can't crash
# decoding the UTF-8 corpus.
try:
    sys.stdin.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass
for line in sys.stdin:
    emit(line)
