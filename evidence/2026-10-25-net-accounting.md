# Defects 23–24 — the numbers the user sees were not measurements

Both were found by comparing what the daemon claimed against what the process
tree actually held, on the owner's running machine. Neither was visible in any
labelled run: the probes are small and steady, and these bugs need scale and
churn.

## 23 — gross growth used as size

`history.growthBytes` summed every upward step. For 46 node processes each
wobbling a few MB per sample that reaches absurd numbers:

```
15:19  net=?  gross=11393 MB   "node grew by 11393 MB"
17:45  net=?  gross=51284 MB
18:41  net=?  gross=73310 MB
```

The machine has 16 GB. The node group held 3.4 GB. The advisory grew more
ridiculous every window, which is worse than a false positive: it is a number
the reader can see is wrong.

Gross is still accumulated, because the cycling branch needs "allocated
400 MB, freed 400 MB". `netGrowthBytes` — the signed sum — is what the
accumulation and suspicion branches read.

## 24 — a departure did not leave the net

Fixing 23 revealed this. Arrivals add their footprint to net; departures added
only to `releaseBytes`. Node spawns a ~220 MB helper per session and reaps it,
so net grew **2846 MB in 285 seconds** while the group held a steady 4.3 GB.

This is defect 13 one accumulator higher, and the same mistake: I fixed one
side of a two-sided rule and moved on. `TestChurnDoesNotInflateNetSize` measures
7800 MB of net growth for a group holding 1.2 GB when the subtraction is
removed.

## What the fix looks like on the running machine

```
REAL   37 node processes, 3197 MB
DAEMON net=-161 MB  gross=3032 MB  bucket=cache_or_retrievable
```

Before: `net=2846 MB`, advisory "node grew by 11393 MB" every window.
After: net is negative (the group shrank), gross is still reported for
cycling detection, no advisory.

Zero advisories in the ten minutes after restart. Buckets produced:
`workload_growth`, `cache_or_retrievable`, `unknown` — no leak and no
accumulation, which is correct for a machine doing normal work.

## The discipline this cost

Two of the three worst bugs in this session — 73 GB and 2846 MB — were
arithmetic on accumulators, and both survived a passing test suite because the
tests ran at lab scale. The check that found them is not a unit test: it is
reading the process tree at the same moment and comparing. That check is now
`/tmp/final_check.sh` and it is what run22 exists to complement.
