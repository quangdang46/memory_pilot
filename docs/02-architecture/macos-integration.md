# MP-ARCH-004 — macOS Integration

**Status:** Partially Verified — every claim below is tagged:
`[Verified: sbh]` = confirmed by reading a working implementation in storage_ballast_helper
`[Apple doc]` = documented by Apple
`[Open]` = needs a prototype on the target Mac before it can be locked

The single most useful discovery from the reference audit: **sbh already implements the macOS memory-pressure integration we need, in production-quality Rust.** It is not disk-specific. We can adopt its approach with confidence and adapt it to Go.

Source anchors (repo `storage_ballast_helper`):
- `crates/sbh_mach/src/mach.rs` — Mach/dispatch FFI wrappers
- `src/platform/macos/pal.rs` — pressure assembly and dispatch subscription
- `src/platform/macos/sys.rs` — `vm_stat` parsing, swap, sysctl
- `src/daemon/loop_main.rs` — event-driven wake on pressure change
- `src/daemon/policy.rs` — memory pressure as an axis of the behaviour matrix

---

## 1. The pressure signal stack (three independent sources)

Memory Pilot must never depend on a single source. The design uses three, in precedence order:

### 1.1 Kernel memory-pressure level — `kern.memorystatus_vm_pressure_level`

`[Verified: sbh]` sbh reads `sysctl kern.memorystatus_vm_pressure_level` as an `i32` and decodes **XNU dispatch masks**, not a 0-based enum:

| Raw value | Meaning |
|---|---|
| 1 | Normal |
| 2 | Warn |
| 4 | Critical |
| anything else / read failure | do not override; fall back to derived estimate |

sbh's comment at `src/platform/macos/pal.rs:496` records exactly this ("XNU exports dispatch masks here, rather than its internal 0-based enum"). This is the single most important detail to copy — a naive 0/1/2 decode is wrong.

**Precedence rule (adopted):** the kernel level wins when available. The derived VM-signal estimate is used only as a fallback, and must be labelled as such.

### 1.2 Dispatch memory-pressure events — `DISPATCH_SOURCE_TYPE_MEMORYPRESSURE`

`[Apple doc]` + `[Verified: sbh]`. sbh creates a dispatch source on a global QoS-utility queue with mask `NORMAL | WARN | CRITICAL`, sets an event handler that calls `dispatch_source_get_data()` and maps the bitmask to the strongest set bit, and exposes it as a subscription with a liveness handle. See `crates/sbh_mach/src/mach.rs:748-781` and `:336-365`, `:870-894`.

**Design consequences we copy:**
- Events are a *wake-up* signal, not a data source. On event, re-read the state (sbh does: `memory_pressure_from_dispatch_event` re-reads `read_current_memory_pressure` and only falls back to an empty event-derived struct if the read fails — `pal.rs:522-530`).
- The daemon's sleep is *interruptible*: sbh caps each sleep slice at 500 ms and drains pressure events between slices (`loop_main.rs:171`, `:3849-3890`). This makes reaction latency bounded by the slice, not by the poll interval.
- Subscription failure is not fatal: sbh falls back to a dedicated polling thread that emits only on level *change*, with a liveness weakref so it exits when the handle drops (`pal.rs:556-591`).

**Go implementation note `[Open]`:** Go has no dispatch. Options, in order of preference:
1. **cgo shim**: a tiny C file wrapping `dispatch_source_create` + a Go callback registered via `//export`. This mirrors sbh exactly and is the lowest-risk path. Cost: cgo (violates MP-REQ-079's "no cgo unless forced" — this would be one of the documented forced cases).
2. **Pure-Go polling** at 1–2 s with change detection. Simpler, no cgo, but loses the kernel's own notion of pressure transitions and adds up to 2 s latency.
3. **cgo for the dispatch source only**, with polling as the always-on liveness path (belt and braces, matching sbh's fallback structure).

Prototype ADR-001 must measure: event latency vs. polling latency, CPU cost of each, and the behaviour of the dispatch source across sleep/wake.

### 1.3 Derived VM-signal estimate (fallback)

`[Verified: sbh]` sbh's fallback inference (`pal.rs:597-619`) is deliberately simple and conservative:

```
free_pct       = free_count / total_pages * 100
compressor_pct = compressor_page_count / total_pages * 100
swap_active    = swap_used_bytes > 0

if free_pct < 2.0 and swap_active            -> Critical
else if free_pct < 5.0 and (compressor_pct >= 10.0 or swap_active) -> Warn
else                                          -> Normal
```

These thresholds are sbh's *disk-tool* numbers for a *fallback* path; Memory Pilot must re-derive them from measurements on the target machine (ADR-004/Q-3). It should not inherit them by accident. But the *shape* (free scarcity + compressor activity + swap spill) is the right shape.

---

## 2. VM statistics

`[Verified: sbh]` `host_statistics64(HOST_VM_INFO64)` via a `vm_statistics64` buffer — not `vm_stat(1)` parsing on the hot path. Fields consumed:

| Field | Meaning | Notes |
|---|---|---|
| `page_size_bytes` | from `sysconf(_SC_PAGESIZE)` | **Must not be hardcoded** (16 KiB on Apple Silicon) |
| `free_count` | free pages | |
| `active_count` | active pages | |
| `inactive_count` | inactive (reclaimable-ish) pages | |
| `wire_count` | wired pages | not reclaimable |
| `speculative_count` | speculative pages | |
| `compressor_page_count` | pages held by the compressor | compressed-memory size = count × page size |
| `throttled_count` | pages throttled | |

`accounted_pages() = free + active + inactive + wire + compressor` (sbh `sys.rs:235-241`).

**ABI pitfall `[Verified: sbh]`** — `crates/sbh_mach/src/mach.rs:44-95` documents a real incident: `libc`'s `vm_statistics64` struct grew from 24 to 57 fields across a `libc` version bump; the count passed to `host_statistics64` is an **ABI revision selector, not a buffer-size hint**, and the kernel rejected the larger count with `KERN_INVALID_ARGUMENT` — silently breaking **every macOS memory read** while the disk-pressure half of the daemon kept working. The fix: a locally-defined `VmStatistics64Rev0` mirror struct used only to derive the count, **`HOST_VM_INFO64_REV0_COUNT = 38`** (`size_of::<VmStatistics64Rev0>() / size_of::<integer_t>()`), while the real read buffer stays the full `libc::vm_statistics64` (a superset with an identical field prefix). A regression test pins this (`mach.rs:928-935`).

**Hard requirement for the Go implementation:** declare our own 38-`integer_t`-wide struct for the `host_statistics64` count and do not derive it from a third-party binding's `unsafe.Sizeof`. This is not a style preference; it is the specific bug that silently kills memory telemetry.

Fields present in the kernel's fuller struct but intentionally **not read** by sbh (and not needed by us either, noted so nobody "completes" the struct and breaks the count): `zero_fill_count`, `reactivations`, `pageins`, `pageouts`, `faults`, `cow_faults`, `lookups`, `hits`, `purges`, `purgeable_count`, `decompressions`, `compressions`, `swapins`, `swapouts`, `external_page_count`, `internal_page_count`, `total_uncompressed_pages_in_compressor`.

`[Verified: sbh]` sbh also ships a `vm_stat` *text* parser (`sys.rs:1074-1117`) requiring: page size line, `pages free`, `pages active`, `pages inactive`, `pages wired down`, `pages speculative`, `pages occupied by compressor`, `pages throttled`. It is used for fixture testing / cross-checking.

**Cross-check rule for Memory Pilot:** in `doctor`, compare our computed values to `vm_stat` text output and fail loudly on mismatch beyond 1 page. `[Adopt]`

---

## 3. Swap

`[Verified: sbh]` `sysctl vm.swapusage` returns a string like:

```
total = 2048.00M  used = 512.25M  free = 1535.75M  (encrypted)
```

sbh parses it into a struct, lowercasing and reading `(encrypted)` as a flag (`sys.rs:1055-1072`). Values are in `total/used/free` with M/G suffixes.

`[Verified: sbh]` sbh also handles the **binary** form of the same sysctl (`sys.rs:1318-1336`), whose layout is `[0..8]=total, [8..16]=free, [16..24]=used, [28..32]=encrypted flag` — note **free precedes used**, the opposite of the textual order. A parser that assumes "total, used, free" from the string form will silently swap two fields when it meets the struct form. **Rule: if any field is missing or unparsable ⇒ `SwapUsage::Unknown` (explicit), never zeros.**

**Derived metrics Memory Pilot needs:**
- `swap_used_bytes`, `swap_total_bytes`
- `swap_rate = d(used)/dt` over the EWMA window — the *rate* is what signals thrash onset, not the absolute (charter §2.2)
- **swap headroom vs. disk:** on a 256 GB SSD, swap growth consumes disk. The pressure model must include free disk as an input whenever `swap_rate > 0` (MP-SCN-003).

`[Verified: sbh]` sbh's RAM-scratch doc explicitly warns: *"Process VmSwap does not account for shmem swap"* and *"nominal_ram_plus_swap_bytes is reported for context, not as safe remaining allocation capacity"* (`docs/ram-scratch.md:34-38`). Copy this framing verbatim into our telemetry docs: swap total is not "available memory".

---

## 4. Process inventory

`[Verified: sbh]`

| Need | API | Notes |
|---|---|---|
| PID list | `proc_listpids(PROC_ALL_PIDS)` | includes kernel/root PIDs we may not query |
| Per-process basics + task info | `proc_pidinfo(pid, PROC_PIDTASKALLINFO, …)` → `proc_taskallinfo` | sbh's primary path |
| pid, ppid | `pbsd.pbi_ppid` | 0 ⇒ no parent |
| name | `pbi_name`, fallback `pbi_comm` | fixed char arrays, NUL-trimmed |
| start time | `pbsd.pbi_start_tvsec`, `pbi_start_tvusec` | **identity anchor for PID reuse** |
| virtual / resident | `ptinfo.pti_virtual_size`, `pti_resident_size` | resident ≠ footprint (no compressed/shared accounting) |
| CPU | `pti_total_user`, `pti_total_system` (ns) → µs | |
| executable path | `proc_pidpath(pid, PROC_PIDPATHINFO)` | |
| disk I/O | `proc_pid_rusage(pid, RUSAGE_INFO_V4)` → `ri_diskio_bytesread/written` (cumulative only, no delta, no op count), `ri_resident_size`, self-only `ri_pkg_idle_wkups + ri_interrupt_wkups` | |
| memory regions w/ paths | `proc_pidinfo(pid, PROC_PIDREGIONPATHINFO2)` — **flavor 22**, iterated by address | used for build-dir detection in sbh; **out of scope for us**. `[Verified]`: the broader flavor 8 (all regions) measured ~12 s for ~900 processes and returns no path for anonymous mappings — confirms this API is too expensive for a sampling loop and must never be used for inventory |

**Open-file / socket introspection `[Verified: sbh, out of scope for us]`:** `proc_pidfdinfo(PROC_PIDFDSOCKETINFO)` reads a bound AF_UNIX path at **hand-computed struct offsets** (24+160, 24+240+16 into `socket_fdinfo`) that sbh pins only with a live-kernel test, not a documented ABI guarantee. Not needed for memory pressure; recorded here as a warning against any future temptation to add FD introspection without the same caution.

**Critical distinction for G-4:** `resident_memory_bytes` is *not* the process's memory pressure contribution. Shared pages, compressed memory, and file-backed pages all distort it. Apple's own guidance and sbh's docs both caution against treating RSS sums as "used RAM". Memory Pilot must:
- use deltas of the same metric over time (which is what matters for growth), and
- never present `Σ RSS` as a meaningful total, and
- label footprint with the API it came from.

`[Open]` ADR-002 should decide whether to add `ri_physical_footprint_bytes` from `RUSAGE_INFO_V4` (closer to "physical footprint" as Activity Monitor defines it) alongside `pti_resident_size`. sbh reads V4 but uses it for I/O; we may want the footprint field specifically.

**Permission reality `[Verified: sbh]`:** sbh enumerates *all* PIDs but filters out its own PID and cannot read every process. Its `process_list` uses `filter_map` — PIDs that fail are silently dropped (`pal.rs:190-199`). Memory Pilot must **count** the drops and report coverage (MP-REQ-032), because silent dropping is exactly the "missing data looks like good news" failure SI-4 forbids.

**PID reuse `[Verified: pt + sbh]`:** both repos key process identity on the **microsecond start time** from `proc_bsdinfo`:

- pt: `specs/target-identity.md:47-64` defines `start_id = "<boot_id>:<start_time_ticks>:<pid>"` for Linux; on macOS it computes `pbi_start_tvsec * 1_000_000 + pbi_start_tvusec` and calls it *"exact, unlike `ps etime`"* (`collect/macos.rs:677-761`), revalidating against the same source immediately before any action (`action/signal.rs:621-645`).
- pt's id type carries the tuple directly (`pt-common/src/id.rs:44-46`).
- sbh: `start_time_unix_ms` from `pbi_start_tvsec/tvusec` (`pal.rs:1003-1010`).

**Our identity rule:** `(pid, start_tvsec, start_tvusec)`. No boot-id is needed on macOS if the start time is microsecond-exact and always read from the same API — but the tuple must never be reconstructed from `ps`'s `etime`/`lstart` text, which is exactly what pt does for its *base* record and why it needs a separate exact source. Even though we never signal a process, identity matters for *attribution stability*: a PID recycled between samples must not inherit the previous process's growth history.

---

## 5. Disk headroom

`[Verified: sbh]` `statfs(2)` → free/total bytes for the boot volume; `[Apple doc]` APFS "purgeable" space is not free space. sbh is emphatic about this (`docs/macos.md:125`): *"Purgeable storage is controlled by macOS … not treated as free space when making pressure decisions."*

**Adopt:** Memory Pilot reports free disk as `statfs` available bytes, excludes purgeable, and does not attempt APFS snapshot thinning (that is a disk-tool feature; NG-5/NG-13).

---

## 6. LaunchAgent

`[Verified: sbh]` sbh's generated user plist (`tests/snapshots/launchd_user.plist`) is a good template. Key settings and why:

| Key | sbh active generator value (`daemon/service/launchd.rs:276-341`) | Why it matters for us |
|---|---|---|
| `Label` | `com.sbh.daemon` | ours: `com.mempilot.agent` |
| `ProgramArguments` | binary + `daemon` | |
| `RunAtLoad` | true | start at login |
| `KeepAlive` | `{SuccessfulExit: false, Crashed: true}` | **restart on crash, not on clean exit** — lets `mempilot stop` actually stop it |
| `ThrottleInterval` | 60 | prevents crash-loop storms |
| `Nice` | **5** (not 19 — see below) | background politeness without starvation |
| `LowPriorityIO` | true | do not compete with user I/O |
| `ProcessType` | **Standard** | see the measured reason below |
| `WorkingDirectory` | data dir | |
| `StandardOutPath`/`StandardErrorPath` | user: `~/Library/Logs/sbh/{sbh.log,sbh.err}`; system: `/var/log/sbh/...` | launchd-owned capture of early crashes before our logger is up |
| `EnvironmentVariables` | `SBH_CONFIG`, `SBH_CONFIG_PATH`, `RUST_LOG`, `SBH_LAUNCHD_LABEL` | config path, log level |
| Resource limits | **none** — no `HardResourceLimits`/`SoftResourceLimits` | our RSS budget is self-enforced (SI-6), not `setrlimit` |

**Measured rationale for `Nice=5` + `ProcessType=Standard` `[Verified: sbh]`:** sbh's code records that `ProcessType=Background` with `Nice=19` was tried and rejected — at system load ~700 the daemon got **0.1 s of CPU in two minutes and wrote no state for 40 minutes** (`launchd.rs:314-319`). A memory-pressure daemon that cannot run when the machine is loaded is useless precisely when it is needed. **Adopt `Nice=5` + `Standard`; do not adopt a more "polite" configuration out of principle.**

**Doc/code drift warning:** sbh's own `docs/macos.md:59-66` still says `Nice=19` and the docs elsewhere describe FSEvents as absent when the backend exists and is selected. Conclusion: **trust the generator functions, not the prose** when copying platform configuration.

**Stale code path:** sbh also ships a `#[deprecated]` `legacy_inline` generator (`service.rs:824-870`) emitting a third, obsolete variant (`ThrottleInterval 10`, no `ProcessType`, no env vars). Not the live path; do not copy it.

**Lifecycle `[Verified: sbh]` (`launchd.rs:355-390`):** `bootstrap` → on "already loaded" `bootout` + retry → non-killing `kickstart` → `print`. Note the deliberate choice of `kickstart` **without** `-k` so installing/updating does not kill a running daemon.

**Additions Memory Pilot needs beyond sbh's template:**
- `MachServices`: **not needed** (we use a Unix socket).
- `LimitLoadToSessionType`: `Aqua` — the daemon is only meaningful with a logged-in session.
- `ProcessType`: still worth measuring `Adaptive` vs `Standard` for reaction latency (`[Open]`, Q-12) — but `Background` is already ruled out by sbh's measurement.

**Lifecycle rules `[Verified: sbh]`:**
- Never self-daemonize (no fork). launchd owns the lifecycle. `[Apple doc]`
- Handle `SIGTERM` for ordered shutdown, flushing the ledger.
- Write a `state.json` heartbeat at a fixed cadence (sbh: 30 s, stale at 90 s, `self_monitor.rs`) so `doctor` and `status` can detect a wedged daemon.

---

## 7. Permissions

`[Verified: sbh]` and `[Apple doc]`:

| Capability | Requirement |
|---|---|
| `host_statistics64`, `sysctl`, `statfs`, `proc_listpids` | none beyond normal user |
| `proc_pidinfo` on own-user processes | none |
| `proc_pidinfo` on other users' / SIP-protected processes | denied; must degrade |
| Full Disk Access | only needed to read protected *files* (e.g. `~/Library/Mail`) — **not needed for memory telemetry** |

`[Adopt]`: sbh probes FDA by attempting to list `~/Library/Mail` and caching the result. Memory Pilot should **not require FDA at all** and should state that in `doctor` output ("Full Disk Access: not required for memory telemetry"). If a future provider needs it, it becomes an explicit, per-provider requirement.

---

## 7.5 What neither reference implementation does (a negative but useful fact)

`[Verified: absence confirmed in both repos]` Neither sbh nor pt reads:

- `task_info(TASK_VM_INFO)` / `phys_footprint` — the exact field Activity Monitor's "Memory" column uses.
- Per-process compressed or swapped bytes.
- Host CPU load (`host_statistics(HOST_CPU_LOAD_INFO)`, `processor_info`).
- `purgeable_count` (present in the raw kernel struct, never surfaced by either tool).

**Consequence for ADR-002:** the field we hoped to adopt (`ri_physical_footprint_bytes` via `RUSAGE_INFO_V4`) has **no working reference implementation in either audited repo** — it is not merely "unmeasured", it is "nobody we reviewed has wired it up". This downgrades ADR-002 Option B from "adopt, pending a cost measurement" to "a genuine spike with no fallback reference": Phase 0 must either (a) get `phys_footprint` working from first principles against Apple's `<libproc.h>`/`<mach/task_info.h>` headers and validate it against Activity Monitor's displayed number, or (b) accept `pti_resident_size` deltas as the only footprint signal for v1 and document the resulting imprecision in every report (`process-attribution.md` §3.1 already carries this caveat; it now carries more weight).

## 8. What macOS will not let us do

Recorded here so no future design drifts into assuming it:

| Not possible | Consequence for design |
|---|---|
| Read another process's heap or object graph | no heap-level relief, ever (NG-4) |
| Force another app to release memory | only the app's own APIs can do this |
| Tune the compressor, swap policy, or jetsam behaviour | not exposed |
| Suppress the out-of-memory dialog | NG-3 |
| Get exact "physical footprint" for all processes without per-PID calls | cost grows with process count; needs a budget (ADR-002) |
| Trust RSS as "memory used" | always report the API + caveat |

---

## 9. Verification checklist for this document

Before any of this is marked `[Verified]` (as opposed to `[Verified: sbh]`), it must be re-proven **on the target Mac mini M4, macOS version TBD** by the prototype described in ADR-001:

1. `kern.memorystatus_vm_pressure_level` values observed: 1/2/4 confirmed under induced pressure.
2. Dispatch source latency to callback: measured, p50/p95.
3. `host_statistics64` struct/count accepted by the running kernel.
4. `vm_stat` cross-check within 1 page on 20 consecutive samples.
5. Process enumeration count vs. `ps -ax | wc -l`, with drop count recorded.
6. Idle CPU/RSS of a bare collector loop (informs MP-REQ-070).
7. Behaviour across sleep/wake and across a forced OOM event (with a deliberate memory hog in a *test* process).
