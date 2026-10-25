#!/usr/bin/env python3
"""Verify that a running experiment's inputs match its declared labels.

Run this ~60s after starting a run. If it fails, the run is not evidence:
report the harness defect and discard the run rather than scoring it.

Usage: verify_inputs.py <run-dir>
"""
import json
import os
import subprocess
import sys
import time


def ps_table():
    out = subprocess.run(["ps", "-Ao", "pid=,ppid=,rss=,comm="],
                         capture_output=True, text=True).stdout
    rows = []
    for line in out.splitlines():
        parts = line.split(None, 3)
        if len(parts) < 4:
            continue
        pid, ppid, rss, comm = parts
        rows.append({"pid": int(pid), "ppid": int(ppid),
                     "rss_kb": int(rss), "comm": comm})
    return rows


def children_of(table, pid):
    return [r for r in table if r["ppid"] == pid]


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: verify_inputs.py <run-dir>")
    run = sys.argv[1]
    labels = json.load(open(os.path.join(run, "labels.json")))
    started = os.path.join(run, "started.txt")
    if os.path.exists(started):
        t0 = os.path.getmtime(started)
        print(f"run age: {time.time() - t0:.0f}s\n")

    table = ps_table()
    by_name = {}
    for r in table:
        base = os.path.basename(r["comm"])
        by_name.setdefault(base, []).append(r)

    failures, notes = [], []
    for name, spec in sorted(labels.items()):
        rows = by_name.get(name)
        if not rows:
            failures.append(f"{name}: not running (label expects it)")
            continue
        row = rows[0]
        pid = row["pid"]
        kids = children_of(table, pid)
        rss_mb = row["rss_kb"] / 1024
        mode = spec["mode"]

        print(f"{name}: pid={pid} ppid={row['ppid']} rss={rss_mb:.0f}MB "
              f"children={len(kids)}")

        if mode == "children":
            want = spec.get("children", 20)
            # Children accumulate 1 per 20s; require visible progress.
            if len(kids) == 0:
                failures.append(f"{name}: no children yet — cannot test "
                                f"'subprocess accumulation'")
            elif len(kids) >= want:
                notes.append(f"{name}: reached {len(kids)}/{want} children")
            # Lineage matters: children must be reachable from the probe.
            for k in kids:
                if k["ppid"] != pid:
                    failures.append(f"{name}: child {k['pid']} not parented")
        else:
            if rss_mb < 10:
                failures.append(f"{name}: only {rss_mb:.0f}MB resident — the "
                                f"shape may not have started allocating")

        if row["ppid"] == 1:
            failures.append(f"{name}: reparented to launchd (ppid=1) — "
                            f"lineage lost; subprocess grouping untestable")

    print()
    for f in failures:
        print("FAIL", f)
    for n in notes:
        print("note", n)
    if failures:
        print("\ninputs do NOT match the labels — discard this run")
        return 1
    print("inputs match the labels (as far as this check can tell)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
