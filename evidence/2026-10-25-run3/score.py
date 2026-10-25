#!/usr/bin/env python3
"""Score Memory Pilot's verdicts against pre-declared ground truth.

Usage: score.py <run-dir>

Reads <run-dir>/labels.json (truth, written before the run) and the daemon
ledger, then prints per-probe agreement. Read-only: it never writes to the
daemon's data and never changes a verdict.

Design notes:
- Ground truth comes from the file, not from this script, so a label cannot be
  adjusted after seeing the result.
- A probe counts as correct only if the LAST verdict for it matches. An early
  `unknown` is not a failure: withholding judgement until evidence arrives is
  the intended behaviour, recorded separately as `withheld`.
- A probe with no verdict at all is reported as `silent`, which is different
  from wrong: the daemon said nothing.
"""
import json
import os
import sys
from collections import defaultdict

LEDGER = os.path.expanduser("~/.local/share/mempilot/ledger.0001.jsonl")


def load_labels(run_dir):
    with open(os.path.join(run_dir, "labels.json")) as f:
        return json.load(f)


def run_start(run_dir):
    """Timestamp the run began, so earlier runs' verdicts cannot be scored."""
    path = os.path.join(run_dir, "started.txt")
    if not os.path.exists(path):
        return None
    return open(path).read().strip()[:19]  # 2026-10-25T01:03:09Z


def load_verdicts(groups, since=None):
    """Last verdict per group, plus how many times each bucket appeared."""
    last = {}
    seen = defaultdict(list)
    if not os.path.exists(LEDGER):
        return last, seen
    with open(LEDGER, errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line or '"classification"' not in line:
                continue
            try:
                r = json.loads(line)
            except json.JSONDecodeError:
                continue
            g = r.get("group")
            if g not in groups:
                continue
            # Ignore verdicts from before this run started: a probe name reused
            # across runs would otherwise inherit the earlier run's result.
            at = r.get("at", "")
            if since:
                # ledger timestamps are local "+07:00"; started.txt is UTC Z
                try:
                    from datetime import datetime, timezone
                    ts = datetime.fromisoformat(at)
                    start = datetime.fromisoformat(since.replace("Z", "+00:00"))
                    if ts.tzinfo is None:
                        ts = ts.astimezone()
                    if start.tzinfo is None:
                        start = start.replace(tzinfo=timezone.utc)
                    if ts.astimezone(timezone.utc) < start.astimezone(timezone.utc):
                        continue
                except ValueError:
                    pass
            rec = {
                "at": r.get("at", ""),
                "bucket": r.get("bucket", ""),
                "confidence": r.get("confidence", 0.0),
                "growth_mb": r.get("growth_mb", 0),
                "basis": r.get("basis", []),
            }
            last[g] = rec
            seen[g].append(rec)
    return last, seen


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: score.py <run-dir>")
    run_dir = sys.argv[1]
    labels = load_labels(run_dir)
    since = run_start(run_dir)
    last, seen = load_verdicts(set(labels), since)

    correct = wrong = silent = 0
    rows = []
    for group, spec in sorted(labels.items()):
        rec = last.get(group)
        if rec is None:
            silent += 1
            rows.append((group, spec["truth"], spec["expect"], "-", "-", "-", "SILENT"))
            continue
        bucket = rec["bucket"]
        # `unknown` after the evidence threshold is not "correct"; it is the
        # daemon declining to decide, which is tracked separately.
        if bucket == spec["expect"]:
            outcome = "OK"
            correct += 1
        elif bucket == "unknown":
            outcome = "WITHHELD"
        else:
            outcome = "WRONG"
            wrong += 1
        rows.append((group, spec["truth"], spec["expect"], bucket,
                     f"{rec['confidence']:.2f}", rec["at"][11:19], outcome))

    w = max(len(r[0]) for r in rows) + 2
    ew = max(len(r[2]) for r in rows) + 2
    vw = max(len(r[3]) for r in rows) + 2
    print(f"{'probe':<{w}}{'truth':<10}{'expected':<{ew}}{'verdict':<{vw}}{'conf':<6}{'at':<10}result")
    print("-" * (w + ew + vw + 32))
    for r in rows:
        print(f"{r[0]:<{w}}{r[1]:<10}{r[2]:<{ew}}{r[3]:<{vw}}{r[4]:<6}{r[5]:<10}{r[6]}")

    total = len(rows)
    print()
    print(f"correct {correct}/{total}   wrong {wrong}   silent {silent}   "
          f"withheld {total - correct - wrong - silent}")

    # History: a group that was judged more than once and changed its mind is
    # worth surfacing — it means the verdict is time-dependent.
    unstable = {g: rs for g, rs in seen.items()
                if len({r["bucket"] for r in rs}) > 1}
    if unstable:
        print("\nverdicts that changed over the run:")
        for g, rs in sorted(unstable.items()):
            trail = " -> ".join(f"{r['bucket']}({r['confidence']:.2f})" for r in rs)
            print(f"  {g}: {trail}")

    return 0 if wrong == 0 and silent == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
