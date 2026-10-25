# Evidence — the metric Memory Pilot measures disappears under pressure

Date: 2026-10-25. Target: Mac mini M4, darwin 25.4.0, 16 GB RAM.
All measurements below are from this machine, reproducible with the scripts in
this directory. No product code was changed to produce any of them.

---

## 1. The observation

A process allocated 400 MB of touched, **incompressible** data (`os.urandom`
payload) and held every reference. Two independent sources were sampled at the
same instants:

| t (s) | `ps rss` = `rusage.resident_size` | `rusage.phys_footprint` |
|---|---|---|
| 0 | 71.5 MB | **427.7 MB** |
| 5 | 30.7 MB | **427.7 MB** |
| 10 | 30.7 MB | **427.7 MB** |
| 25 | 15.7 MB | **427.7 MB** |
| 35 | 8.4 MB | **427.7 MB** |
| 45 | 8.2 MB | **427.7 MB** |

The process never freed anything. `resident_size` fell by 98 %. Its pages were
swapped out (compression was ruled out: the payload is random bytes, and the
compressor did not grow).

`phys_footprint` stayed flat, which is correct: the process still owns 428 MB
of dirty memory. It is just not resident.

## 2. Why this matters for the product

`internal/attribution/attribution.go` computes every growth delta from
`ProcessSample.ResidentBytes`:

```go
a.series[key] = &series{... prevBytes: int64(p.ResidentBytes) ...}
now := int64(p.ResidentBytes)
```

The collector already fetches `phys_footprint` into `FootprintBytes`
(`platform.go:151`) and attribution ignores it.

Consequence: **exactly when the machine is under pressure — the only time the
product is supposed to act — a leaking process's measured size collapses.**
The growth signal is destroyed by the very condition the tool exists to
detect. A real leak that pushes pages to swap reads as a process *shrinking*.

This is consistent with the owner's original report: repeated leak waves, and
Memory Pilot never flagged the actual culprit. It was not only the wiring
(defects fixed earlier); the measurement itself vanishes under load.

## 3. What was NOT established

- Whether `phys_footprint` alone is the right metric. It includes compressed
  and swapped pages, so it answers "how much memory does this process own",
  which is what attribution needs — but it is not comparable to headroom
  deltas, which are physical. Mixing them without care would double-count
  swapped pages against a shrinking free-page pool.
- Whether the daemon's own live runs showed the same collapse. The probes in
  run 1 did grow (`probe-leak` reached 447 MB RSS), so on this machine at
  that time pages were not evicted for those processes. The collapse is
  condition-dependent: it appears when the system decides to reclaim, which
  is precisely the regime of interest.
- Anything about the classifier's correctness. That is a separate evidence run
  (see `2026-10-25-labelled-run/`).

## 4. Reproduction

```
cd evidence/2026-10-25-metric-vanishes
python3 compare_sources.py     # ps rss vs proc_pid_rusage, same instants
python3 compression_test.py    # rules out compression as the cause
python3 rusage_probe.py        # which rusage fields retain the truth
```

`compare_sources.py` needs no privileges. It spawns a Python child that holds
400 MB and samples both sources for 45 s.

## 5. The finding in one line

Memory Pilot measures a number that goes to zero under pressure, while the
number that stays correct was already being collected and thrown away.

---

## 6. Correction: footprint does track growth; the earlier "stalls" reading was an artifact

A follow-up measurement (child reports its own allocated total on stdout, so
the ground truth does not depend on any kernel field) shows:

| child reports | phys_footprint | ps rss |
|---|---|---|
| 25 MB | 36.8 MB | 44.8 MB |
| 50 MB | 61.9 MB | 52.9 MB |
| 75 MB | 87.1 MB | 24.7 MB |
| 100 MB | 112.3 MB | 29.7 MB |
| 125 MB | 137.5 MB | 24.7 MB |
| 140 MB | 152.6 MB | 39.7 MB |

`phys_footprint` follows the process's own allocation within ~12 MB
(fixed overhead + allocator slack). `resident_size` — the field attribution
uses — wanders between 24 and 53 MB and **bears almost no relation to how much
memory the process holds**.

An earlier probe of this directory reported footprint as "stalling" at
162.7 MB while allocation continued. That probe pre-allocated the full payload
in under two seconds and then idled; once a process stops allocating, footprint
correctly stops changing, and no further growth existed to observe. The
"stalls" reading was a contradiction in that probe's own setup, not a
property of the metric. Section 1's headline table (a process that allocated
everything up front, then held it) remains valid for what it measures — the
collapse of `resident_size` — but it does not support "footprint fails too".

**Corrected conclusion:** `phys_footprint` is the usable metric.
`resident_size` is not, and it is the one the code uses.
