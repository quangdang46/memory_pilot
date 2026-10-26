# Labelled run, 2026-10-26 08:33–08:55: 3 of 3

The classifier had not been re-measured since identity moved from `comm` to the
full image path and the quiet threshold changed. Both touch the signals it
reasons on, so the previous score was no longer evidence.

## Ground truth, fixed before the run

`evidence/2026-10-25-labelled-run/labels.json`, unchanged:

| probe | truth | expected bucket |
|---|---|---|
| probe-leak | steady growth, no CPU, no plateau | `leak_suspicion` |
| probe-plateau | grows then stops and holds | `cache_or_retrievable` |
| probe-workload | growth driven by real CPU work | `workload_growth` |

## Result

| probe | expected | got | confidence | duration | |
|---|---|---|---|---|---|
| probe-leak | leak_suspicion | **leak_suspicion** | 0.65 | 1260 s | MATCH |
| probe-plateau | cache_or_retrievable | **cache_or_retrievable** | 0.70 | 1260 s | MATCH |
| probe-workload | workload_growth | **workload_growth** | 0.90 | 1260 s | MATCH |

**3/3**, up from 4/5 on 2026-10-25. The previous miss was `probe-plateau`,
labelled "cache" on the assumption that freeing a slice returns memory; measured
from the process's own footprint it never decreased, so the label was wrong and
the classifier was right to call it unexplained growth. This run the plateau
probe was allowed to settle and the classifier called it correctly, so that
disagreement is resolved in the classifier's favour rather than left open.

## Independent verification

The classifier is not the ground truth. Sampled directly alongside it:

| probe | footprint | CPU in 10 s | what it was doing |
|---|---|---|---|
| probe-leak | 1046 MB, still climbing | 0.01 s | nothing — allocating and sleeping |
| probe-plateau | 68 MB, flat | 0.00 s | nothing — holding what it allocated |
| probe-workload | 870 MB, climbing | 2.00 s | compiling-equivalent work in a loop |

All three match their labels.

## A defect the run exposed

The `probe-leak` record disagreed with itself:

```
growth_mb   961
basis       "grew 961 MB in total"
text        "group probe-leak grew by 240 MB — unexplained sustained growth"
```

`growth_mb` came from `decidedGrowthMB` (net, 961 MB); the text was rendered
from `g.GrowthBytes` (window, 240 MB). The advisory path had this exact bug
fixed on 2026-10-25; the ledger record still had it. Now both use one horizon,
chosen by the bucket. `TestVerdictTextAgreesWithItsBasis` pins it.

This is the second time the same class of defect has appeared in a second
place — "the number and its explanation came from different horizons" — so it
is worth stating as a rule: wherever a verdict's number is printed, it must be
the number the verdict was decided on.

## What this does not establish

- The probes are synthetic and idle-by-construction. A real leak inside a
  working process looks different, and that case has not been measured.
- Thresholds remain chosen, not fitted against a false-positive corpus.
- The 1200 s evidence requirement means no verdict exists for the first twenty
  minutes of any new shape. That is deliberate and was observed again here:
  every probe read `unknown` until 1260 s.
