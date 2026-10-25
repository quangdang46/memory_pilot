# run21 — 4/5, and the one miss is still the label

Run21 is the first run on the daemon carrying the fixes from this session
(defects 10–17). Same method: labels written before the run, inputs gated,
ledger scored afterwards.

```
probe           truth      expected                verdict                 result
probe-children  subprocess subprocess_accumulation subprocess_accumulation OK
probe-leak      leak       leak_suspicion          leak_suspicion          OK
probe-plateau   cache      cache_or_retrievable    cache_or_retrievable    OK
probe-sawtooth  cache      cache_or_retrievable    leak_suspicion          WRONG
probe-workload  work       workload_growth         workload_growth         OK

correct 4/5
```

## What is different from run8's 4/5

`probe-children` reached `subprocess_accumulation` and **stayed** there for six
consecutive windows (13:35 → 14:00). Before this session that bucket could not
fire on its own signature at all: the group held 288 MB across 21 processes and
the window reported growth 0 MB (defects 12 and 17). The verdict is now
produced from the shape it describes.

`probe-plateau` is now correct on the plateau branch itself — `plateau 1425s ≥
900s` — rather than by accident.

## The miss is the label, measured twice

The daemon says `probe-sawtooth` is unexplained sustained growth. Ground truth
was measured independently, twice, from the process's own `phys_footprint`:

```
run8  (40 s):    686 686 686 ... 686 696
run21 (115 s):   13.5 13.5 23.7 33.7 33.7 33.7 33.7 33.7 33.7 43.9 43.9
                 53.9 53.9 63.9 63.9 73.9 73.9 73.9 73.9 73.9 73.9 84.0 84.0
```

Monotonically non-decreasing in both. Go keeps freed heap pages mapped, so the
probe never returns memory. A probe that never releases anything is not a
cache, and a classifier that calls it "sustained growth with no plateau" is
describing what is actually happening.

The label is wrong, not the verdict. It is left unchanged in `labels.json` so
the score keeps reporting 4/5 rather than being edited to 5/5 after the fact.

## The scope of this evidence

Four of five labelled shapes correct. What is still not proven is unchanged
from `FINAL-STATUS.md`: Critical pressure never reached live, thresholds chosen
not fitted, and no 72-hour soak.
