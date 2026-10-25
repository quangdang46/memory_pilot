# Why the leak warnings did not work — evidence summary

Three independent defects, each measured on the target machine, each enough on
its own to make the product fail at its stated job. None of this is a tuning
problem; all three are the absence of a required piece.

## 1. Nothing was ever classified (fixed)

The daemon built its policy engine and discarded it; attribution was never
fed process data. Evidence: 12 hours of ledger, 17 406 system samples,
8 413 decisions, **zero** attributions, **zero** action results. The report
command had no grouped-growth data to show because none was ever computed.

## 2. The measurement collapses under pressure (not fixed)

`attribution.go` computes growth from `ProcessSample.ResidentBytes`.

A process holding 400 MB of touched, incompressible data was sampled from two
sources at the same instants:

| source | start | under pressure |
|---|---|---|
| `resident_size` (= `ps rss`) | 71.5 MB | **8.2 MB** |
| `phys_footprint` | 427.7 MB | **427.7 MB** |

Nothing was freed. With the ground truth taken from the child's own allocation
counter instead of any kernel field:

| process holds | `phys_footprint` | `resident_size` |
|---|---|---|
| 50 MB | 61.9 MB | 52.9 MB |
| 100 MB | 112.3 MB | **29.7 MB** |
| 140 MB | 152.6 MB | **39.7 MB** |

`resident_size` has almost no relationship to how much memory a process holds
once the system starts reclaiming. So in the only regime the product exists
for, a leaking process measures as *shrinking*. The collector already fetches
`phys_footprint` (`platform.go:151`) and nothing reads it.

## 3. Two of the four verdicts cannot be produced (not fixed)

`classify` declares five buckets; only two are reachable.

- `cache_or_retrievable`: the deciding field `PlateauS` is never computed by
  `attribution.SeriesFor`, so the branch at `classify.go:65` is dead. Beyond
  that, `CloseWindow` classifies only groups growing more than 32 MB in the
  window, so a group that plateaued is filtered out before the test could run
  — it is never re-judged.
- `subprocess_accumulation`: `Bucket = SubprocessAccumulation` is **never
  assigned anywhere**. The classifier's input struct has no process-count
  field.

A cache-shaped workload is therefore reported as `leak_suspicion`. Measured:
`probe-plateau` held flat at 6.2 MB for 10 minutes and was labelled
"unexplained sustained growth" at confidence 0.65.

## What this means for the original complaint

The user's report was "repeated leak waves, Memory Pilot had no effect". The
evidence supports a stronger statement: even with the wiring repaired, a real
leak that drives the machine into pressure would be *invisible* — its
measured size collapses exactly then — and a benign cache would be *accused*.
Both failure directions were present at once.

Fixing (1) alone, which is what an earlier commit did, produces a daemon that
reports confidently and wrongly. That is worse than a silent one.

## 4. The classifier only looks at the top five groups — and which five is decided by raw bytes (measured)

Run 3 kept five labelled workloads alive for 26+ minutes on a busy machine.
Not one of them produced a single classification, while `Google Chrome`,
`opencode.exe` and `node` were classified repeatedly.

Attribution windows during that run, top groups only:

```
08:20:21 | wezterm-gui, Google Chrome, node, opencode.exe, com.docker.back,
           probe-sawtooth, python3.14, MTLCompilerServ
08:25:21 | wezterm-gui, Google Chrome, com.apple.Virtu, node,
           opencode.exe, com.docker.back, Docker Desktop, python3.14
```

The probes were running (verified in `ps`, RSS climbing steadily). They lost
to processes with bigger byte counts: wezterm-gui reported +4 904 MB in one
window, Google Chrome +2 441 MB. A probe growing 6-14 MB per tick cannot
place in the top five against that.

`pipeline.CloseWindow`:

```go
for i, g := range w.Groups {
    if i >= p.Cfg.TopN { break }          // TopN = 5, ranked by bytes
    if g.GrowthBytes < p.Cfg.MinGroupGrowthBytes { continue }
```

So the product classifies the five biggest growers by absolute bytes. A leak
that matters — sustained, unexplained — is often neither the biggest nor the
fastest; it is the one that will not stop. It never reaches the classifier.

This is why the owner sees warnings about Chrome and node (big, active, mostly
legitimate) and nothing about the process that is actually running away.

**Interaction with defect 2:** top-N is ranked by `ResidentBytes`, the metric
that collapses under pressure. On a machine in real trouble, the ranking is
computed from numbers that are partly fiction.
