# Run 3 — five labelled shapes, 40 minutes, scored 0/5

Method mirrors run 1: ground truth in `labels.json` fixed before the run;
probes spawned as ordinary user processes with unique `pbi_comm` names; RSS
sampled independently; verdicts read from the daemon ledger.

Harness gates applied before this run counted as evidence:
- `verify_inputs.py` confirmed lineage (`ppid` = launcher, not 1) so
  `probe-children` could actually test subprocess grouping.
- `score.py` was scoped to `started.txt` so earlier runs' verdicts could not
  leak into the score. The first scoring attempt did exactly that and reported
  a meaningless "1/5"; that number was discarded, not published.

## Result

```
probe           truth     expected                 verdict  result
probe-children  subprocess subprocess_accumulation  -        SILENT
probe-leak      leak      leak_suspicion            -        SILENT
probe-plateau   cache     cache_or_retrievable      -        SILENT
probe-sawtooth  cache     cache_or_retrievable      -        SILENT
probe-workload  work      workload_growth           -        SILENT

correct 0/5   wrong 0   silent 5   withheld 0
```

All five probes ran the full 40 minutes. Not one received a classification.

## Why

Attribution windows during the run ranked by raw resident bytes:

```
08:20:21 | wezterm-gui, Google Chrome, node, opencode.exe, com.docker.back,
           probe-sawtooth, python3.14, MTLCompilerServ
08:25:21 | wezterm-gui, Google Chrome, com.apple.Virtu, node,
           opencode.exe, com.docker.back, Docker Desktop, python3.14
```

`pipeline.CloseWindow` classifies the top 5 groups by bytes, after filtering
groups below 32 MB growth. On this machine wezterm-gui and Chrome report
thousands of MB per window, so a probe growing 6-14 MB per tick never places.
Only `probe-sawtooth` broke into the list once (by allocating and releasing
repeatedly) and it was still out of the top 5 in the next window.

In the same period the daemon repeatedly classified Chrome as
`leak_suspicion` — a browser releasing more than it grew in every window — and
`node`/`opencode.exe` the same way.

## What this establishes

On a realistically busy machine, the daemon's warnings and the actual
sustained growers are disjoint sets. The warnings are about the biggest
processes; the sustained growers are not the biggest processes. Both are
visible in the same ledger at the same time.

## What it does not establish

- Nothing about verdict accuracy for probes that do reach the classifier —
  run 1 showed one of three wrong, and the plateau/cache path is unreachable
  by construction (see `../2026-10-25-metric-vanishes/WHY-IT-DOES-NOT-WORK.md`).
- Nothing about L2/action behaviour (pressure never reached Critical).
