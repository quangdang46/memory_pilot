# Why the leak warnings did not work — evidence summary

Five independent defects, each measured on the target machine. Four are the
absence of a required piece rather than a tuning problem; one (defect 1) was
repaired earlier today and is listed for completeness.

Status key: FIXED = repaired and test-guarded; OPEN = present in the running
daemon.

## 1. Nothing was ever classified — FIXED

The daemon built its policy engine and discarded it; attribution was never
fed process data. Evidence: 12 hours of ledger, 17 406 system samples,
8 413 decisions, **zero** attributions, **zero** action results. The report
command had no grouped-growth data to show because none was ever computed.

## 2. The measurement collapses under pressure — OPEN

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

## 3. Two of the five verdicts cannot be produced — OPEN

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

## 4. Only the top five groups are judged, ranked by raw bytes — OPEN

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

## 5. Release volume is measured, stored, and ignored — OPEN

`Google Chrome`, a browser doing ordinary caching, was labelled
`leak_suspicion` at 0.65 in four consecutive windows. Its own attribution
records, same windows:

| window | growth | **release** | processes | verdict |
|---|---|---|---|---|
| 08:05 | +1952 MB | **2851 MB** | 18 | leak_suspicion |
| 08:10 | +1396 MB | **1697 MB** | 20 | leak_suspicion |
| 08:15 | +1779 MB | **1468 MB** | 20 | leak_suspicion |
| 08:20 | +2441 MB | **3097 MB** | 24 | leak_suspicion |
| 08:25 | +1641 MB | 2395 MB | 24 | unknown |

A process that releases *more* than it grows — every single window — is the
textbook signature of a cache. `attribution` already computes `ReleaseBytes`
and records it. `pipeline.CloseWindow` builds `classify.SeriesStats` from
slope, activity and coverage and never passes release. `classify` has no field
for it.

So the one signal that would clear Chrome of being a leak is measured,
stored, and not consulted. Meanwhile the growth-during-CPU signal that does
reach the classifier is ambiguous for a browser (its renderer processes burn
CPU while filling caches), and the process count rises (18 → 24), which is the
*subprocess accumulation* signature — a bucket that cannot be assigned.

This is what the owner sees on screen: warnings about Chrome, repeatedly. The
warnings are not wrong about "memory went up". They are wrong about what that
means, and the product has the data to know better.

---

## What this means for the original complaint

The user's report was "repeated leak waves, Memory Pilot had no effect". The
evidence supports a stronger statement.

- A real leak that drives the machine into pressure is **invisible**: the
  field it is measured with collapses exactly then (defect 2).
- A benign cache is **accused**: release volume is ignored (defect 5), the
  cache verdict is unreachable (defect 3), and Chrome is labelled
  `leak_suspicion` four windows running.
- A sustained unexplained grower that is not the largest is **never judged at
  all**: only the top five by bytes reach the classifier (defect 4).

All three failure directions are present at once. Fixing the wiring (defect
1, done earlier today) produces a daemon that reports confidently and wrongly,
which is worse than a silent one.

## Order the fixes must go in

1. Measure `phys_footprint`, not `resident_size` (defect 2). Everything
   downstream is computed from this number; until it is right, no threshold or
   classifier change can be trusted.
2. Feed release volume and process count into classification (defects 5, 3).
   The data is already collected and recorded.
3. Stop ranking candidates by absolute bytes alone (defect 4). Sustained
   growth rate and "has it ever stopped" matter more than size.
4. Re-run the labelled experiment. Only then is a verdict worth acting on.
