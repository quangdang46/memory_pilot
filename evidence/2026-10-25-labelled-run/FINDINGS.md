# Evidence run 1 — labelled workloads vs Memory Pilot verdicts

Method: three binaries with distinct `pbi_comm` names, one per known memory
shape, spawned as ordinary user processes. Ground truth fixed *before* the run
in `labels.json`. The daemon was not modified during the run. RSS sampled
independently every 60 s from `ps`, not from mp's own numbers.

## Ground truth vs verdict

| probe | label (truth) | mp verdict | confidence | verdict time | match |
|---|---|---|---|---|---|
| probe-leak | steady growth, no CPU, no plateau | `leak_suspicion` | 0.65 | 1274 s | **yes** |
| probe-workload | growth with CPU work | `workload_growth` | 0.90 | ~1300 s | **yes** |
| probe-plateau | grows 80 s then holds flat | `leak_suspicion` | 0.65 | 1665 s | **NO** |

Before the 1200 s evidence threshold, all three read `unknown / too short` —
honest, and the behaviour the earlier repair was supposed to produce.

## The false positive, diagnosed

`probe-plateau` stopped growing after ~80 s and held at 6 MB for the rest of
the run. Its growth per window went 60 MB → 24 MB → **0 MB**. The classifier
still said "unexplained sustained growth".

Cause, read from the code after the fact: `classify.SeriesStats.PlateauS` is
the field that decides "this stopped growing, it is a cache, not a leak", and
`attribution.SeriesFor` never computes it. It is always 0, so the plateau
branch in `classify.go:65` is unreachable. A process that grows briefly and
then stops is therefore classified as if it were still climbing.

This is not a threshold that needs tuning. The signal is absent.

## What this run does and does not establish

Establishes:
- the daemon distinguishes a CPU-correlated grower from an idle grower on real
  processes (0.90 vs 0.65), which is the core discrimination the product needs
- it withholds a verdict until the evidence threshold, rather than guessing
- it produces a *wrong* verdict — a false alarm — for the plateau shape, and
  the mechanism is a missing computation, not a judgement call

Does not establish:
- that the verdicts are right for shapes other than these three
- anything about `action_result`/L2 on the live daemon (pressure never reached
  Critical; see the earlier repair record)
- that 20 minutes is the right evidence window

## Raw data

- `samples.tsv` — per-minute RSS for the three probes + mp's verdict at that
  minute (independent of mp)
- `started.txt`, `first_verdict` — timing
- ledger records: `~/.local/share/mempilot/ledger.0001.jsonl`, kind
  `classification`, groups `probe-*`

## Additional observation: the plateau probe was never re-evaluated

`probe-plateau` has exactly one classification record in the whole run
(07:30:01, `leak_suspicion`). The report window closes every 300 s, and the
group kept appearing in attribution windows (24 MB, then 0 MB growth), but no
further verdict was recorded for it.

Reason: `pipeline.CloseWindow` classifies the top N groups *that grew more
than `MinGroupGrowthBytes` (32 MB) in that window*. A group that has plateaued
grows 0 MB, so it drops out of classification entirely — it is not re-judged
as a cache, it simply stops being judged. The one verdict it received was
from the window where it was still growing, and nothing ever revisits it.

So there are two independent defects on the same path:
1. `PlateauS` is never computed (always 0) → the cache branch is unreachable.
2. A group that stops growing is not re-classified at all → even a correct
   plateau test would never run, because the group is filtered out first.

Fixing only (1) would not change this run's outcome.
