# MP-ALG-001 — Telemetry Model

**Status:** Draft
**Version:** 0.1.0

The purpose of this document is to make every number in Memory Pilot *defined*: its unit, its source, its cost, and its failure mode. Ambiguity here becomes a wrong diagnosis later.

---

## 1. Quality is a first-class field

Every measurement carries:

```jsonc
{
  "value": 1234,
  "unit": "bytes",
  "source": "host_statistics64",     // api | command | derived
  "quality": "ok",                    // ok | degraded | unavailable
  "reason": null,                     // required when not ok
  "monotonic_ns": 0,                  // for rate math
  "wall_time": "2026-10-23T10:30:00Z"
}
```

Rules:

- `quality != ok` ⇒ consumers must not treat the value as evidence of calm (SI-4).
- `source: derived` values must name their inputs.
- **Rates use `monotonic_ns` only.** Wall clock is for display and for correlating with external events (see MP-SCN-103).

---

## 2. System-level metrics

| Metric | Unit | Source | Cost | Failure mode |
|---|---|---|---|---|
| `ram_total` | bytes | `sysctl hw.memsize` (u64) | trivial | unavailable ⇒ no percentages possible |
| `page_size` | bytes | `sysconf(_SC_PAGESIZE)` | trivial | must never be hardcoded (16 KiB on Apple Silicon) |
| `pages_free` | pages | `host_statistics64.free_count` | 1 syscall | unavailable ⇒ fallback impossible |
| `pages_active` | pages | `active_count` | same call | |
| `pages_inactive` | pages | `inactive_count` | same call | |
| `pages_wired` | pages | `wire_count` | same call | |
| `pages_speculative` | pages | `speculative_count` | same call | |
| `pages_compressor` | pages | `compressor_page_count` | same call | |
| `pages_throttled` | pages | `throttled_count` | same call | |
| `free_pct` | % | derived: `pages_free / total_pages × 100` | trivial | inherits quality of inputs |
| `compressor_pct` | % | derived: `pages_compressor / total_pages × 100` | trivial | |
| `compressed_bytes` | bytes | `pages_compressor × page_size` | trivial | this is the **physical** RAM the compressor holds, not the logical size of compressed data |
| `swap_total` | bytes | `sysctl vm.swapusage` (text parse) | 1 syscall | parse failure ⇒ unavailable, never 0 |
| `swap_used` | bytes | same | | |
| `swap_rate` | bytes/s | derived, EWMA over `swap_used` | trivial | needs ≥ `min_samples`; else `warming` |
| `kernel_pressure_raw` | int | `sysctl kern.memorystatus_vm_pressure_level` | 1 syscall | decode 1/2/4; other values ⇒ `unavailable`, do **not** guess |
| `kernel_pressure` | enum | derived from raw | | |
| `event_pressure` | enum + age | dispatch source callback | event-driven | stale if no event for a long time while kernel says otherwise |
| `disk_free` | bytes | `statfs(2)` boot volume | 1 syscall | |
| `disk_total` | bytes | `statfs(2)` | | |

**Cross-check policy:** `doctor` compares `pages_free`/`pages_active`/… against `vm_stat(1)` output within 1 page, `swap_*` against the raw sysctl string, and records the comparison result. A persistent mismatch is a bug, not noise.

---

## 3. Process-level metrics

| Metric | Unit | Source | Cost | Notes |
|---|---|---|---|---|
| `pid` | int | `proc_listpids` | | |
| `ppid` | int | `PROC_PIDTASKALLINFO.pbsd.pbi_ppid` | | 0 = none |
| `start_sec`, `start_usec` | int | `pbi_start_tvsec`, `pbi_start_tvusec` | | **identity anchor** |
| `name` | string | `pbi_name` → `pbi_comm` | | fixed char arrays |
| `exe_path` | string | `proc_pidpath` | 1 call/pid | may fail; cache by identity |
| `resident_bytes` | bytes | `pti_resident_size` | included | **not** physical footprint; see caveat |
| `virtual_bytes` | bytes | `pti_virtual_size` | included | not a pressure signal |
| `cpu_user_ns`, `cpu_sys_ns` | ns | `pti_total_user/system` | included | for correlating growth with activity |
| `disk_read_bytes`, `disk_write_bytes` | bytes | `proc_pid_rusage(RUSAGE_INFO_V4)` | 1 call/pid | optional; extra cost |
| `physical_footprint_bytes` | bytes | `RUSAGE_INFO_V4.ri_physical_footprint_bytes` | 1 call/pid | `[Open]` ADR-002 — preferred for footprint if cost is acceptable |

### The RSS caveat (must appear in every report that uses it)

`resident_bytes` counts pages resident in the process's address space. It:
- counts shared pages in each process that maps them,
- excludes compressed pages (which live in the compressor, not in any process),
- includes file-backed pages that are reclaimable.

Therefore:
1. **Never sum `resident_bytes` across processes and call it "memory used."**
2. **Always use deltas over time** for growth attribution — shared pages cancel in a delta of the same process, which is exactly what attribution needs.
3. If `physical_footprint_bytes` is available and affordable, prefer it for display; use `resident` deltas as the fallback.

### Sampling cost

One `proc_pidinfo(PROC_PIDTASKALLINFO)` per PID per tick. With ~600 processes and a 5 s tick that's ~120 calls/s — acceptable on M4 but not free. Mitigation:

- Process enumeration runs at a **lower cadence** than system VM stats (default: every 4th tick, or every 10 s).
- PIDs that fail get backed off (don't retry a denied PID every tick).
- Coverage counters record how many PIDs were attempted / succeeded / denied.

---

## 4. Derived series (what gets stored)

Raw samples are too large to keep for days. We store **aggregated series** at a 10 s base resolution plus event annotations:

| Series | Resolution | Retention |
|---|---|---|
| system pressure series (level, free_pct, compressed, swap_used, disk_free) | 10 s | 72 h |
| process groups (top N by delta, N=20) | 30 s | 72 h |
| per-process raw | 60 s, top 50 only | 6 h |
| events / decisions / actions | as they happen | 30 d |
| calibration records | per forecast | 30 d |

Rationale: 10 s × 72 h = 25 920 points per series; at ~64 bytes/point that is ~1.7 MB per series. Bounded by design (MP-REQ-061).

---

## 5. Units and naming rules

- Bytes, always base-2 (KiB semantics) internally; render as MB/GB with the unit stated.
- Percentage points, not fractions, in all user-facing text. Fractions internally.
- Rates in bytes/s, never "MB/min" in stored data.
- Times: RFC3339 UTC for wall; monotonic ns for math.
- Process identity: `(pid, start_sec, start_usec)` tuple, never bare PID.

---

## 6. What is *not* collected, deliberately

| Not collected | Why |
|---|---|
| Command lines of other users' processes | privacy; also frequently denied |
| Full environment | unnecessary, sensitive |
| Window titles, browser URLs | out of scope, privacy |
| Network connections | not relevant to memory pressure |
| Full memory region maps | expensive; only needed for leak forensics in a future opt-in mode |
| Per-thread statistics | cost/benefit does not justify it for v1 |

---

## 7. Validation requirements

| Rule | Enforced by |
|---|---|
| every metric has unit/source/quality | schema validation at emit |
| no metric may be persisted with `quality: ok` if it was derived from a non-ok input | estimator unit test |
| `swap_total == 0` must be distinguishable from "swap unknown" | separate optional types |
| `page_size` is read, never assumed | code review + test that injects a fake 4096 and 16384 |
| monotonic timestamps are non-decreasing | test on clock-jump injection |
