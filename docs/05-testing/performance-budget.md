# MP-TEST-006 — Performance Budgets

**Status:** Draft — **all numbers are targets to be validated, not measurements**
**Version:** 0.1.0
**Target machine:** Mac mini M4, 16 GB RAM, 256 GB SSD, macOS 15+ (version to be pinned)

A memory-pressure daemon on a 16 GB machine must justify every megabyte and every CPU cycle it takes. sbh's approach — publish the budget, monitor yourself, degrade before you become the problem — is adopted.

---

## 1. Budgets

| Metric | Idle (no pressure) | Loaded (pressure ≥ Prevent) | Hard limit |
|---|---|---|---|
| RSS | ≤ 60 MB | ≤ 120 MB | 400 MB → self-protective exit |
| CPU (avg over 5 min) | ≤ 0.3 % of one core | ≤ 2 % | 5 % sustained for 10 min → degrade |
| CPU (peak 1 s) | ≤ 5 % | ≤ 15 % | — |
| Disk writes | ≤ 1 KB/s avg | ≤ 4 KB/s avg | 1 MB/s burst |
| Ledger growth | ≤ 3 MB/day | ≤ 15 MB/day | 50 MB/day |
| Disk retained | ≤ 500 MB total across segments | same | 1 GB |
| File descriptors | ≤ 20 | ≤ 32 | 64 |
| Goroutines | ≤ 25 | ≤ 40 | 100 |
| Startup → first sample | ≤ 2 s | — | 5 s |
| Reaction latency (pressure event → state update) | — | ≤ 1 s p95 (event-driven) / ≤ cadence p95 (poll) | 2 s |

**Why 60 MB idle:** Go runtime overhead plus a modest sample store. Realistic for a daemon that keeps 72 h of aggregated series in RAM only if we're careful — we are not; the series live on disk, memory holds a window. If the implementation needs more, the budget gets renegotiated explicitly in an ADR, not silently.

---

## 2. Sampling cost model

For a tick at cadence `T`:

```
per-tick cost ≈ C_vm + C_swap + C_sysctl + (C_proc × N / P)
  C_vm      ≈ one host_statistics64 syscall     (~10 µs)
  C_swap    ≈ one sysctl read                   (~10 µs)
  C_sysctl  ≈ one sysctl read                   (~5 µs)
  C_proc    ≈ one proc_pidinfo + one proc_pidpath (~30–80 µs)
  N         = visible process count (~400–800)
  P         = process-sampling divisor (1 = every tick, 4 = every 4th tick)
```

At N=600, P=4, T=5 s: ≈ 0.03 s of syscall time per 5 s ⇒ ~0.6 % of one core. That already exceeds the idle budget, so:

- **P must be ≥ 8** at idle, or process sampling moves to a separate slower loop.
- `proc_pidpath` is cached per identity (not re-read every sample).
- `proc_pid_rusage` (for I/O) is optional and off by default at idle.

**Design consequence:** process sampling cadence is decoupled from system sampling cadence. System VM stats can be cheap and frequent (1–5 s); process enumeration is expensive and slower (10–30 s). The estimator needs the former; attribution needs the latter. This is reflected in `test-strategy.md` and must be in the architecture as two loops, not one.

---

## 3. Memory model for our own process

| Resident component | Budget |
|---|---|
| Go runtime baseline | ~10–15 MB |
| Config + policy | < 1 MB |
| In-memory sample window (last 30 min @ 5 s = 360 samples × ~200 B) | ~0.1 MB |
| Process identity table (600 × ~200 B) | ~0.2 MB |
| Per-group history (20 groups × 24 h @ 1 min) | ~2 MB |
| Ledger write buffers | < 1 MB |
| IPC buffers | < 1 MB |
| Slack / fragmentation | rest |

That leaves a lot of room under 60 MB; the risk is accidental retention (growing maps, unbounded caches). Enforcement:

- all caches have explicit caps and eviction;
- a periodic `runtime.ReadMemStats` check feeds the self-monitor (with the caveat that Go's `HeapInuse` ≠ RSS);
- the self-monitor reads its **own** RSS via `proc_pid_rusage`/Mach task info (the same API we use for others — an honest self-measurement).

Go-specific: set `GOMEMLIMIT` (soft limit) via `debug.SetMemoryLimit` to a value below the hard cap (e.g. 250 MB) so GC gets aggressive before we hit the wall. **Note:** `GOMEMLIMIT` is a soft limit on Go-managed memory; it does not cap total RSS (stacks, cgo, mmap). Document this rather than pretending it is a hard cap.

---

## 4. Measurement protocol

Every budget claim must be produced by a scripted, repeatable measurement:

```
bench/budget/
  run_idle.sh          # 1 h idle, samples every 10 s
  run_loaded.sh        # 8 h with the standard synthetic workload
  run_pressure.sh      # induces pressure via test allocator, 30 min
  analyze.py           # computes p50/p95/p99, slope, and pass/fail vs budget
```

Results are committed as `bench/results/<date>-<macOS>-<arch>.json` with the machine description. A release note cites the committed result.

---

## 5. Degradation triggers (tie to SI-6)

| Condition | Action |
|---|---|
| RSS > 120 MB | drop process sampling to P = 16; trim caches |
| RSS > 200 MB | disable attribution for non-incident windows; cap history |
| RSS > 400 MB for 3 checks | exit 10 (self-protective) |
| CPU avg > 5 % for 10 min | increase cadence; disable optional series |
| Ledger write rate > cap | drop detail records, keep events |
| Any of the above while host ≥ Prevent | act immediately, not after 10 min |

The last row is the important one: the thresholds are not static — they tighten when the host is under pressure.

---

## 6. What would make us fail this budget

Recorded as an explicit risk list:

1. **Process sampling too frequent.** N=800 × every 5 s. Mitigated by decoupling (P).
2. **Keeping raw history in memory.** Must be on disk.
3. **Log rotation storms.** Bounded.
4. **Goroutine-per-process.** Forbidden (worker pools only).
5. **Unbounded map keys** (one entry per PID ever seen). Identity table must evict dead identities after a grace period.
6. **JSON encoding on the hot path at high frequency.** Batch records.

---

## 7. Open questions

- **Q-10:** Does reading `proc_pid_rusage` per PID materially change the cost? Measure before enabling by default.
- **Q-11:** Is `Runtime.ReadMemStats` every 30 s acceptable, or does stop-the-world measurement cost more than it saves? (It is cheap, but measure.)
- **Q-12:** Does launchd `ProcessType=Adaptive` improve reaction latency over `Standard` on the target machine? `Background`+`Nice 19` is already ruled out (sbh measured the daemon getting 0.1 s CPU in 2 min at load ~700); `Standard` is the verified baseline.
