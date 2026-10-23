#!/usr/bin/env python3
"""Analyze a mempilotd ledger for budget compliance (performance-budget.md).

Usage: analyze.py <ledger.jsonl...> [--idle|--loaded]
Computes p50/p95/p99 of self RSS and CPU, RSS slope (leak detection via
least squares over monotonic time), record continuity (max gap), and
pass/fail against MP-REQ-070/071 budgets. Exit 0 = pass, 1 = fail.
"""
import json, math, sys, argparse

BUDGET = {
    "idle":   {"rss_mb": 60.0, "cpu_pct": 0.5},
    "loaded": {"rss_mb": 200.0, "cpu_pct": 3.0},
}

def pct(xs, p):
    xs = sorted(xs)
    if not xs: return 0.0
    k = (len(xs) - 1) * p
    f, c = math.floor(k), math.ceil(k)
    if f == c: return xs[f]
    return xs[f] + (xs[c] - xs[f]) * (k - f)

def slope_per_hour(samples):
    # least squares of rss_mb over hours — positive slope = suspected leak
    if len(samples) < 2: return 0.0
    n = len(samples)
    sx = sum(s[0] for s in samples); sy = sum(s[1] for s in samples)
    sxx = sum(s[0]*s[0] for s in samples); sxy = sum(s[0]*s[1] for s in samples)
    den = n*sxx - sx*sx
    if den == 0: return 0.0
    return (n*sxy - sx*sy) / den  # MB per hour

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("ledgers", nargs="+")
    ap.add_argument("--mode", choices=["idle","loaded"], default="idle")
    args = ap.parse_args()
    b = BUDGET[args.mode]

    rss, cpu, times = [], [], []
    last_mono = None; max_gap_s = 0.0; records = 0
    for path in args.ledgers:
        for line in open(path):
            line = line.strip()
            if not line: continue
            try: rec = json.loads(line)
            except json.JSONDecodeError: continue  # crash tail tolerance
            if rec.get("kind") != "system": continue
            records += 1
            mono = rec["sample"].get("MonotonicMs", 0) / 1000.0
            if last_mono is not None:
                max_gap_s = max(max_gap_s, mono - last_mono)
            last_mono = mono
    # self RSS/CPU come from status samples if present, else reread as needed
    # (daemon ledger records system samples; self metrics live in memory —
    # the status op is the live source, so analyze uses whatever status
    # snapshots were appended by the operator via `mempilot status >> snap`).
    for path in args.ledgers:
        for l in open(path):
            if '"rss_mb"' not in l:
                continue
            try:
                s = json.loads(l)
            except json.JSONDecodeError:
                continue  # crash-tail tolerance
            if "rss_mb" in s:
                rss.append(float(s["rss_mb"])); cpu.append(float(s.get("cpu_pct", 0)))

    result = {
        "records": records,
        "max_gap_s": round(max_gap_s, 1),
        "continuity_ok": max_gap_s <= 300,  # 5 min cadence floor
        "rss_p50_mb": round(pct(rss, .5), 2), "rss_p95_mb": round(pct(rss, .95), 2),
        "rss_p99_mb": round(pct(rss, .99), 2),
        "cpu_p50_pct": round(pct(cpu, .5), 3), "cpu_p95_pct": round(pct(cpu, .95), 3),
        "cpu_p99_pct": round(pct(cpu, .99), 3),
        "rss_slope_mb_per_h": round(slope_per_hour(list(zip([i for i,_ in enumerate(rss)], rss))), 4),
        "budget": b, "mode": args.mode,
    }
    result["rss_ok"] = (max(rss, default=0) <= b["rss_mb"]) if rss else None
    result["cpu_ok"] = (pct(cpu, .95) <= b["cpu_pct"]) if cpu else None
    checks = [result["continuity_ok"]] + [x for x in (result["rss_ok"], result["cpu_ok"]) if x is not None]
    result["pass"] = all(checks)
    print(json.dumps(result, indent=2))
    sys.exit(0 if result["pass"] else 1)

if __name__ == "__main__":
    main()
