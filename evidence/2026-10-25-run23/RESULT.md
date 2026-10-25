# run23 — 4/5 on the daemon carrying all 25 fixes

```
probe-children  subprocess_accumulation  0.60  OK   (5 consecutive windows)
probe-leak      leak_suspicion           0.65  OK
probe-plateau   cache_or_retrievable     0.70  OK
probe-workload  workload_growth          0.90  OK
probe-sawtooth  leak_suspicion                 the label is wrong (measured twice)
correct 4/5
```

## The scare, and what it was

The first scoring read 3/5, with `probe-children` apparently changing its mind
from `subprocess_accumulation` to `cache_or_retrievable` in the final window.
Reading that window's signals explained it:

```
20:32:25  subprocess_accumulation  procs=21  net=319MB  release=0
20:37:25  cache_or_retrievable     procs=8   net=142MB  release=177MB
```

The probes had exited at ~20:33. Their children died with them, the group
released 177 MB, and the daemon described exactly that. The measurement was
right; the scoring was reading a verdict taken during shutdown.

`score.py` now takes an optional `ended.txt` and ignores verdicts after it.
With that bound: 4/5, and `probe-children` never changed its mind.

## What this run establishes

- The five shapes reach their intended buckets on a daemon that has been
  running for hours, with real machine load and 75% process visibility.
- `subprocess_accumulation` fires on its own signature — 21 processes, 319 MB
  net growth, no CPU correlation — and holds that verdict rather than drifting.
- No advisory was raised for node, zsh, Chrome, bash or wezterm during the run.
  The only advisories were for `probe-children`, which was genuinely
  accumulating.

## Still not proven

Unchanged: Critical pressure never reached live; thresholds chosen not fitted;
no 72-hour soak. See `../FINAL-STATUS.md`.
