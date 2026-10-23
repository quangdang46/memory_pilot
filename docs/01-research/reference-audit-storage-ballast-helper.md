# Reference Audit — storage_ballast_helper (sbh)

**Repo:** `github.com/Dicklesworthstone/storage_ballast_helper`
**Cloned to:** `.tmp/storage_ballast_helper` (shallow clone, 2026-10-23)
**License:** MIT
**Size:** ~300 Rust files; main crate + `crates/{sbh_mach, sbh_btrfs}`
**What it is:** a cross-platform Rust daemon that prevents **disk-full** incidents for AI coding workloads.

**Why it matters here despite being a disk tool:** it is a mature, carefully-engineered *pressure-control daemon* on macOS, and — crucially — it already contains a real macOS **memory-pressure** integration that is not disk-specific. That integration is the most valuable thing we can learn from it.

---

## 1. Verified: the macOS memory-pressure integration

This was not evident from the README; it came from reading the source.

### 1.1 Kernel level via sysctl

`src/platform/macos/pal.rs:482-506`

```rust
sysctl::read::<i32>("kern.memorystatus_vm_pressure_level")
...
pressure.level = match kernel_level {
    Some(1) => MemoryPressureLevel::Normal,
    Some(2) => MemoryPressureLevel::Warn,
    Some(4) => MemoryPressureLevel::Critical,
    _ => pressure.level,   // keep conservative VM fallback
};
```

The comment is explicit: *"XNU exports dispatch masks here, rather than its internal 0-based enum. Sparse free pages and old swap usage do not imply current pressure."* **This is the single most valuable finding of the audit.** A naive 0/1/2 decode is a real, likely bug in any fresh implementation.

### 1.2 Dispatch source subscription

`crates/sbh_mach/src/mach.rs:746-781, 870-894`

- Creates a dispatch source of type `_dispatch_source_type_memorypressure` on a **global queue at QoS Utility**.
- Event mask = `NORMAL | WARN | CRITICAL`.
- Event handler: `dispatch_source_get_data()` → bitmask → strongest set bit (`MemoryPressureEvent::from_dispatch_data`).
- Cancel handler frees the boxed context (explicit ownership across the FFI boundary).
- Callback wrapped in `catch_unwind` — a panic in user code cannot unwind through C.

### 1.3 Events are a wake-up, not a payload

`src/platform/macos/pal.rs:522-530`:

```rust
fn memory_pressure_from_dispatch_event(event) -> MemoryPressure {
    let dispatch_level = memory_pressure_level_from_dispatch_event(event);
    let mut pressure = read_current_memory_pressure("...dispatch").unwrap_or_else(|_|
        empty_dispatch_memory_pressure(dispatch_level));
    if dispatch_level != Unknown { pressure.level = dispatch_level; }
    pressure
}
```

On every event it **re-reads** the full state; if the read fails it degrades to a level-only struct rather than fabricating numbers. Our design copies the re-read and diverges by marking quality `degraded` instead of returning a partially-empty state (SI-4).

### 1.4 Polling fallback with a liveness handle

`pal.rs:556-591`: if the native subscription fails, a thread polls at `MEMORY_PRESSURE_POLL_INTERVAL` and emits **only on level change**, holding a `Weak` liveness reference so it exits when the subscription handle is dropped. No leak, no zombie thread.

### 1.5 Event-driven wake with bounded slices

`src/daemon/loop_main.rs:171, 3849-3890`:

```rust
const MEMORY_PRESSURE_WAKE_INTERVAL: Duration = Duration::from_millis(500);
```

The daemon's sleep is split into ≤500 ms slices; between slices it drains pressure events. Reaction latency is therefore bounded by 500 ms, not by the (5 s) poll interval. `MEMORY_PRESSURE_CHANNEL_CAP = 16`.

### 1.6 Memory pressure as a second axis of the behaviour matrix

`src/daemon/policy.rs:298-444`, `src/daemon/loop_main.rs:1504-1530`.

`BehaviorPressureLevel { Normal, Warn, Critical }` is derived from `MemoryPressureLevel` (Unknown → Warn, conservatively), and the behaviour dispatch table is a **3 × 5 matrix**: memory pressure (rows) × disk pressure (columns) → `{scan_aggressiveness, cleanup_action, ballast_action, notification_priority}`.

**Adopt the idea, not the table.** Our equivalent is: pressure state × availability/trust of forecast → behaviour. We keep the principle that *an unknown signal is treated as the more conservative of the known levels*.

### 1.7 Derived fallback thresholds

`pal.rs:597-619`:

```
free_pct < 2.0 && swap_used > 0                        -> Critical
free_pct < 5.0 && (compressor_pct >= 10.0 || swap>0)   -> Warn
else                                                    -> Normal
```

Used only when the kernel level is unreadable. **Do not inherit these numbers** — they are a disk tool's fallback; re-derive for our machine (ADR-004, Q-3). The shape (free scarcity + compressor work + swap spill) is correct.

---

## 2. Verified: VM statistics and swap

| Concern | Implementation | Note for us |
|---|---|---|
| VM stats | `host_statistics64(HOST_VM_INFO64)` via `mach2`, buffer typed `libc::vm_statistics64` | `[Verified]` no shelling out |
| Struct/count ABI | explicit rev0 count constant; long comment about `libc` widening the struct 24→57 fields causing `KERN_INVALID_ARGUMENT` (`mach.rs:44-95`) | **Define our own struct + count in Go; do not trust a binding's size** |
| Page size | `sysconf(_SC_PAGESIZE)` — never hardcoded (`mach.rs:856-864`) | 16 KiB on Apple Silicon |
| Required fields | free, active, inactive, wired, speculative, compressor, throttled (`sys.rs:222-241`) | |
| `vm_stat` text parser | requires page-size line + 7 named counters (`sys.rs:1074-1117`) | Good for cross-check/fixtures |
| Swap | `sysctl vm.swapusage` string parse; `(encrypted)` flag (`sys.rs:1055-1072`) | Rate of change matters more than the absolute value |
| Accounting identity | `accounted_pages = free + active + inactive + wire + compressor` (`sys.rs:235-241`) | Useful as an internal consistency check |

**sbh's own caution, worth quoting in our docs** (`docs/ram-scratch.md:34-38`): *"Process VmSwap does not account for shmem swap. These comparisons identify risk, not the exact owner or cause of swapped pages. `nominal_ram_plus_swap_bytes` is reported for context, **not as safe remaining allocation capacity**."*

---

## 3. Verified: process telemetry

| Field | API | Notes |
|---|---|---|
| PID list | `proc_listpids(PROC_ALL_PIDS)` | |
| Name / ppid / start / sizes / CPU | `proc_pidinfo(pid, PROC_PIDTASKALLINFO)` → `proc_taskallinfo` | `pal.rs:945-984` |
| start identity | `pbsd.pbi_start_tvsec/tvusec` → `start_time_unix_ms` | PID-reuse anchor |
| exe path | `proc_pidpath` | cached |
| cwd | `proc_pid_cwd` | used by sbh; **we should not collect** (privacy, no benefit) |
| disk I/O + resident | `proc_pid_rusage(RUSAGE_INFO_V4)` | `ri_diskio_bytesread/written`, `ri_resident_size` |
| memory regions | `PROC_PIDREGIONPATHINFO2` | sbh uses it for build-dir detection; **out of scope** |

**Failure handling observed:** `filter_map` drops unreadable PIDs silently (`pal.rs:190-199`). **Divergence:** we must count them and report coverage (MP-REQ-032), because silent dropping is exactly the "missing data looks like good news" failure our SI-4 forbids.

---

## 4. Verified: EWMA rate estimator (`src/monitor/ewma.rs`)

Adoptable almost verbatim; the math is not disk-specific.

**Adaptive alpha with burst damping** (`ewma.rs:190-200`):

```rust
burstiness = |inst_rate - ewma_rate| / (|ewma_rate| + 1)
damping    = 1.0 / (2.0 * burstiness + 1.0)
alpha      = (base_alpha * damping).clamp(min_alpha, max_alpha)
// defaults: base 0.30, min 0.10, max 0.75
```

**Acceleration from the smoothed rate** (not raw samples) — avoids noise amplification when the interval varies. We need this because our cadence is adaptive (ADR-004).

**Quadratic projection with numerical care** (`ewma.rs:443-490`):

```rust
disc = v*v + 2*a*d;
if disc < 0 return INF;                       // deceleration reverses before arrival
root = sqrt(disc);
t = if a < 0 { 2*d / (v + root) }             // conjugate form: avoids cancellation
    else      { (-v + root) / a };
if a < 0 && t > -v/a { return INF }           // unphysical
```

**Confidence composition** (`ewma.rs:295-310`):

```
sample_term    = min(samples/min_samples, 1)
residual_term  = 1/(1 + residual_ewma/(|rate|+1))
stability_term = 1/(1 + 3*prediction_jitter_ewma)
confidence     = clamp(0.3*stability + 0.2*residual + 0.5*sample, 0, 1)
```

with `prediction_jitter` measuring how much the *ETA itself* moved between ticks, normalized by `max(|t_now|, |t_prev|, 60s)`. **This is the best idea in the file** — it penalizes forecasts that swing wildly even if each estimate looks reasonable.

**Burst state** (`ewma.rs:329-410`): median + MAD×1.4826 robust scale over a 200-sample rate history; `upper_bound = median + 3*MAD`; `burst_probability` from magnitude (÷10) and duration (÷5), taking the max; requires 30 samples to calibrate; skips when `median < 1.0`.

**Fallback** (`ewma.rs:311-328`): before `min_samples` or when `confidence < 0.2`, returns `fallback_active: true`. We add the rule: fallback ⇒ not actionable by policy.

---

## 5. Verified: daemon architecture patterns

| Pattern | Where | Adopt? |
|---|---|---|
| Bounded channels with per-stage caps (`SCANNER_CHANNEL_CAP=2`, `EXECUTOR_CHANNEL_CAP=64`, `REPORT_CHANNEL_CAP=64`) | `loop_main.rs` | Yes |
| Thread liveness heartbeats + stall detection (10 s check, 60 s stall) | `loop_main.rs` | Yes (Go: heartbeat via atomic timestamp) |
| Limited respawns per window (`MAX_RESPAWNS=3 / 300 s`) | `loop_main.rs` | Yes |
| Self-monitor writing `state.json` every 30 s, stale at 90 s | `self_monitor.rs` | Yes (we use 5 s / 30 s) |
| Own RSS warn/hard limits, self-restart at hard limit (`512 MiB / 1 GiB`) | `self_monitor.rs` | Yes, with much smaller numbers (ADR-008) |
| Circuit breaker on repeated failures (5 failures → 30 s cooldown) | `scanner/deletion.rs` | Yes, adapted |
| Policy modes observe/canary/enforce with persisted mode and promotion/demotion | `daemon/policy.rs` | Yes — this is our `observe/canary/enforce` |
| Fallback-safe mode + automatic demotion with reason | `policy.rs` | Yes |
| Evidence ledger: SQLite + JSONL dual write, degrade if one fails | `logger/dual.rs` | Partially — JSONL only in v1 (ADR-003) |
| `explain` with `--why-not` / `--replay` semantics | `cli_app.rs` | **Yes, both are excellent**: "why wasn't this done" and "re-score a past decision with current logic" |
| Error codes `SBH-XXXX` with retryable flag | `core/errors.rs` | Yes, as `MP-XXXX` |
| Prometheus textfile export | `telemetry` | Later (nice for the owner's own dashboards) |
| Conformal **lower bound** (`conformal.rs`) | `monitor/conformal.rs` | **Verified mechanism** — read line-by-line: one-sided lower bound, `k>n ⇒ q=1 ⇒ lo=0` fail-safe, censored resolution, `discard_pending` on recovery, adaptive-coverage (`alpha += gamma*((1-target) - err)`), warmup returns `lo = point`. **Adopted** into `forecasting.md` §4.1. **Application to RAM data remains unverified** — needs replay (ADR-005). |
| E-process drift guard | `monitor/guardrails.rs:124-147, 463-508, 583-632` | **Verified mechanism · Adopted** — directional error (only under-prediction penalized), material-rate floor, severity-scaled penalty, clamp `[-5, 3.5]`, alarm at `e ≥ 20`. See `forecasting.md` §4.2 |
| PredictionScorecard (`Realized/Intervened/FalseAlarm/Inactive`) | `guardrails.rs:773-867` | **Verified mechanism · Adopted** — `Intervened` excluded from the FAR numerator but kept in the denominator; automatic `dynamic_min_confidence` uplift. See `forecasting.md` §4.2b |
| PID controller per mount | `monitor/pid.rs` | **Reject for RAM** — no continuous actuator exists on macOS. (sbh's own README notes the memory-pressure handling is library-only in places.) |
| VOI scan scheduler | `monitor/voi_scheduler.rs` | **Interesting but disk-specific** — "value of information" for choosing where to scan. Our analogue would be choosing *which processes to sample deeply*; keep as an idea, not a port. |
| Ballast files | `ballast/*` | **Reject** — disk-only mechanism; a file is not RAM |
| Quarantine + undo | `scanner/quarantine.rs` | **Reject** — file deletion is out of scope |

---

## 6. launchd configuration (verified)

`tests/snapshots/launchd_user.plist`:

| Key | Value | Comment |
|---|---|---|
| `Label` | `com.sbh.daemon` | |
| `RunAtLoad` | true | |
| `KeepAlive` | `{SuccessfulExit: false, Crashed: true}` | **restart only on abnormal exit** — lets a clean stop actually stop |
| `ThrottleInterval` | 60 | crash-loop damping |
| `Nice` | 5 | background politeness |
| `LowPriorityIO` | true | I/O politeness |
| `ProcessType` | **Standard** (measured; `Background`+`Nice 19` rejected at load ~700) | measure `Adaptive` vs `Standard` for Memory Pilot (Q-12) |
| `StandardOutPath`/`StandardErrorPath` | `~/Library/Logs/sbh/*.log` | launchd capture, pre-logger |
| `EnvironmentVariables` | config path, `RUST_LOG`, label | |

Also verified: `packaging/macos/sbh.entitlements.plist` is **empty** — no special entitlements, consistent with a user-scope daemon.

---

## 7. Documentation practices worth copying

- A generated **runtime-constants table** in `AGENTS.md`/`README.md` from the code (`sbh docs --render`), with a test that fails when the table is stale (`docs.rs`, `cargo test --bin sbh`). **Adopt**: our constants table in docs must be generated and checked.
- **"Fail conservative"** as a written design principle.
- Explicit statements of what the tool does *not* claim (`docs/ram-scratch.md` is a model of this: "not a memory reservation", "not an atomic snapshot", "missing evidence cannot clear an existing warning").
- Error codes with retryability.
- A `--why-not` command that explains why a candidate was *not* acted on. Adopt as `mempilot explain --why-not`.

---

## 8. Divergences (deliberate)

| sbh | Memory Pilot |
|---|---|
| Disk pressure is the primary axis | Memory pressure is the primary axis; disk is a *contributing* input via swap |
| Deletes files | Deletes nothing outside its own data dir |
| PID controller drives batch sizes | No PID; discrete actions with rate limits |
| Silent `filter_map` drop of unreadable PIDs | Coverage counted and reported |
| SQLite + JSONL | JSONL first |
| 512 MiB / 1 GiB self-limits | 60/120 MB budgets, 400 MB hard exit |
| Ballast files | None (rejected: ADR-011) |

---

## 9. License note

MIT License. Reading for architectural ideas is fine; copying code is not done here. This audit produced *design notes and algorithms described in prose*, not copied source.
