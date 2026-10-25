# Run 8 — 4/5, and the one miss is the probe's fault

Run 8 is the first run against a daemon carrying all nine fixes. Same method
as before: ground truth in `labels.json` fixed before the run, probes spawned
as ordinary user processes, RSS/footprint sampled independently, harness gate
(`verify_inputs.py`) passed before the run counted.

## Score

```
probe           truth      expected                verdict                 conf  result
probe-children  subprocess subprocess_accumulation subprocess_accumulation 0.60  OK
probe-leak      leak       leak_suspicion          leak_suspicion          0.65  OK
probe-plateau   cache      cache_or_retrievable    cache_or_retrievable    0.70  OK
probe-sawtooth  cache      cache_or_retrievable    leak_suspicion          0.65  WRONG
probe-workload  work       workload_growth         workload_growth         0.90  OK

correct 4/5
```

Before the fixes, the same five shapes scored 0/5 (none classified) in run 3
and 1/5 in run 1.

## The four correct verdicts, with their stated evidence

- `probe-children` — "process count reached 21 (high-water) with growth not
  correlated to activity (corr 0.00)". Twenty-one real child processes.
  `subprocess_accumulation` had never been assigned anywhere before.
- `probe-leak` — "sustained 1499s ≥ 1200s with no plateau; activity flat for
  1499s ≥ 600s (uncorrelated)". Withheld as `unknown / too short` until
  1200 s of evidence existed, then decided.
- `probe-plateau` — "plateau 1005s ≥ 900s after growth". This is the branch
  that was unreachable before `PlateauS` was computed; it now also fires on a
  group whose current-window growth is 0 MB.
- `probe-workload` — "growth co-moves with activity (corr 1.00)".

## The one miss is the probe

`probe-sawtooth` was labelled "cache" on the assumption that cutting a slice
releases memory. It does not: Go keeps freed heap pages mapped. Measured
directly from the child's own footprint over 40 s:

```
686 686 686 686 686 686 686 686 686 686 686 686 686 686 686 686 686 686 686 696
```

Monotonically non-decreasing. The probe never released anything, so the daemon
calling it "unexplained sustained growth" is correct and the *label* is wrong.

Repro: `sawtooth_ground_truth.py` in this directory.

## What run 8 establishes

- The five verdicts now reach their intended buckets on real processes, from
  real signals (process count, release volume, plateau time, CPU correlation).
- Verdicts are withheld until the evidence threshold and then delivered, with
  the threshold named in the basis.
- No cache-shaped group produced a warning; no leak warning fired early.
- The path from measurement to verdict no longer crashes or silently drops the
  groups that matter (run 3's failure).

## What it does not establish

- Threshold correctness (1200 s, 900 s, 8 processes were chosen, not fitted).
- Behaviour under real memory pressure: this machine stayed at Normal/Warn
  throughout, so the swap-collapse regime proved in
  `../2026-10-25-metric-vanishes/` was not exercised end to end.
- L2 / `action_result` on a live daemon.
