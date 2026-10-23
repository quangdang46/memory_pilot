# Reference Audit — macOS Telemetry (cross-repo)

**Sources:** `storage_ballast_helper` (sbh) and `process_triage` (pt), both cloned to `.tmp/`
**Purpose:** a single consolidated reference for *how to obtain macOS memory/process telemetry*, so the Go implementation does not have to rediscover it.

The two repos take opposite approaches, and the contrast is instructive:

- **sbh**: direct syscalls/Mach/libproc + dispatch, no shelling out on the hot path. Designed for a daemon polling every few seconds.
- **pt**: shells out to `ps`/`lsof`/`netstat`/`sysctl` for most data, using libproc only where exactness matters (identity), because it is a CLI run occasionally.

**Memory Pilot is a daemon ⇒ follow sbh.**

---

## 1. Metric → source table

| Metric | Source | Unit / formula | Cost | Reliability caveats | Anchor |
|---|---|---|---|---|---|
| Total RAM | `sysctl hw.memsize` (u64) | bytes | ~10 µs | none observed | sbh `pal.rs:428-430` |
| Page size | `sysconf(_SC_PAGESIZE)` | bytes | trivial | **never hardcode** (16 KiB on ARM) | sbh `mach.rs:856-864` |
| Free/active/inactive/wired/speculative/compressor/throttled pages | `host_statistics64(HOST_VM_INFO64)` | pages | one syscall | struct/count ABI pitfall (§2) | sbh `mach.rs:507-530` |
| Derived free % | `free_count / total_pages × 100` | % | trivial | — | sbh `pal.rs:432-448` |
| Compressed memory | `compressor_page_count × page_size` | bytes | trivial | physical bytes held, **not** logical size of compressed data | sbh `pal.rs:457-461` |
| Swap total/used/free | `sysctl vm.swapusage` (string) | bytes | one syscall | text parse; `(encrypted)` suffix | sbh `sys.rs:921-923, 1055-1072` |
| Kernel pressure level | `sysctl kern.memorystatus_vm_pressure_level` | i32, **masks 1/2/4** | one syscall | other values ⇒ unavailable | sbh `pal.rs:482-506` |
| Pressure change events | `DISPATCH_SOURCE_TYPE_MEMORYPRESSURE` | bitmask | ~0 idle | requires dispatch (cgo in Go) | sbh `mach.rs:746-781, 870-894` |
| Disk free/total (boot vol) | `statfs(2)` | bytes | one syscall | purgeable ≠ free | sbh `sys.rs:349-375` |
| PID list | `proc_listpids(PROC_ALL_PIDS)` | PIDs | one syscall | includes unreadable PIDs | sbh `mach.rs:560-590` |
| Process name, ppid, start time, virtual, resident, CPU | `proc_pidinfo(pid, PROC_PIDTASKALLINFO)` → `proc_taskallinfo` | bytes / ns | ~30–80 µs per PID | denied for others' processes | sbh `pal.rs:945-984` |
| Exact identity (start, uid, ppid) | `proc_pidinfo(pid, PROC_PIDTBSDINFO)` → `proc_bsdinfo` | start = `tvsec*1e6+tvusec` | cheap | public API; avoids private structs | pt `collect/macos.rs:677-760` |
| Executable path | `proc_pidpath` | string | one call/PID | may fail; cache per identity | sbh `pal.rs:956` |
| Disk I/O per process | `proc_pid_rusage(pid, RUSAGE_INFO_V4)` | bytes | one call/PID | optional; cost adds up | sbh `libproc.rs:69-72` |
| Memory regions with paths | `proc_pidinfo(pid, PROC_PIDREGIONPATHINFO2)` | struct | expensive | **out of scope for us** | sbh `mach.rs:632-670` |
| SIP status | `csrutil status` (shell) | enum | fork+exec | CLI-only; not for a daemon | pt `collect/macos.rs` |
| Session id | `getsid(2)` | int | trivial | — | pt `collect/macos.rs:704-712` |
| Full Disk Access state | probe `~/Library/Mail` listing | enum | one stat/readdir | **not needed for memory telemetry** | sbh `pal.rs:629-660` |

---

## 2. The `vm_statistics64` ABI pitfall (important)

`sbh/crates/sbh_mach/src/mach.rs:44-95` documents a real bug they hit:

- `host_statistics64` takes a `mach_msg_type_number_t*` count that must match the **revision** of the struct the kernel accepts.
- The `libc` crate widened `vm_statistics64` from 24 to 57 fields.
- Passing the larger count returned `KERN_INVALID_ARGUMENT`.
- Their fix: pin an explicit rev0 count constant derived from the original layout.

**Consequence for Go:** define the struct and the count ourselves (`HOST_VM_INFO64_COUNT` for rev0) and do not derive the count from `unsafe.Sizeof` of a third-party binding's struct. Write a test that asserts the call succeeds and returns plausible pages on the target OS.

Fields consumed (sbh `sys.rs:222-241`):

```
page_size_bytes, free_count, active_count, inactive_count, wire_count,
speculative_count, compressor_page_count, throttled_count

accounted_pages = free + active + inactive + wire + compressor
```

`accounted_pages` is a useful internal consistency check: it should be close to total pages (differences come from speculative/other categories).

---

## 3. `vm_stat` cross-check (text parser)

sbh keeps a full parser (`sys.rs:1074-1140`) requiring:

- a page-size line: `"page size of NNNN bytes"`
- `pages free`, `pages active`, `pages inactive`, `pages wired down`, `pages speculative`, `pages occupied by compressor`, `pages throttled`

Values are parsed by taking the first whitespace token, trimming a trailing `.` and commas.

**Use in Memory Pilot:** `doctor` cross-check only. Never on the hot path (it is a forked process).

---

## 4. Swap parsing

Sample format (`vm.swapusage`):

```
total = 2048.00M  used = 512.25M  free = 1535.75M  (encrypted)
```

sbh lowercases, extracts `total/used/free` with M/G suffixes, and flags `(encrypted)` (`sys.rs:1055-1072`).

**Critical framing from sbh's own docs** (`docs/ram-scratch.md:34-38`):

> *"Process VmSwap does not account for shmem swap. These comparisons identify risk, not the exact owner or cause of swapped pages. `nominal_ram_plus_swap_bytes` is reported for context, **not as safe remaining allocation capacity**: zram itself uses RAM…"*

For macOS the analogue: swap total is **not** added to available memory. Swap growth is a latency and disk-headroom signal.

---

## 5. Pressure level decoding

sbh `pal.rs:492-506`:

```
Some(1) -> Normal
Some(2) -> Warn
Some(4) -> Critical
_       -> keep the derived-VM fallback
```

Comment: *"XNU exports dispatch masks here, rather than its internal 0-based enum."*

**Do not** decode 0/1/2. Values `0`, `3`, and anything else mean "no usable kernel evidence" ⇒ fall back and label provenance `derived`.

---

## 6. Dispatch source details

sbh `mach.rs:746-781`:

- queue: global queue, `DispatchQoS::Utility`
- mask: `NORMAL | WARN | CRITICAL`
- handler: `dispatch_source_get_data()` → `from_dispatch_data` → strongest set bit
- context: a `Box` pointer stored via `set_context`; freed in the **cancel** handler
- callback wrapped in `catch_unwind`

sbh `pal.rs:508-591`:

- on native success: `SubscriptionHandle::active_with_resource`
- on failure: conversation to a polling thread at `MEMORY_PRESSURE_POLL_INTERVAL`, emitting **on level change only**, with a `Weak` liveness token.

sbh `loop_main.rs:171, 3849-3890`:

- `MEMORY_PRESSURE_WAKE_INTERVAL = 500 ms`: the daemon's sleep is sliced so events are drained promptly; reaction latency is bounded by the slice, not the poll interval.

**Go implication:** this is the one place cgo is very likely unavoidable. Prototype (ADR-001 / spike S-2) must measure callback latency and cost. A pure-Go polling fallback must exist regardless.

---

## 7. Process enumeration

sbh `pal.rs:190-199`:

```rust
proc_listpids_safe()?
  .into_iter()
  .filter(|pid| *pid > 0 && *pid != current_pid)
  .filter_map(process_info_for_pid_without_command_line)   // silently drops failures
```

`process_info_from_task_all` (`pal.rs:950-975`) maps `proc_taskallinfo`:

| Field | Mapping |
|---|---|
| ppid | `pbsd.pbi_ppid` (0 ⇒ `None`) |
| name | `pbsd.pbi_name`, fallback `pbi_comm` |
| start | `pbi_start_tvsec`, `pbi_start_tvusec` → unix ms |
| virtual | `ptinfo.pti_virtual_size` |
| resident | `ptinfo.pti_resident_size` |
| CPU | `pti_total_user/system` (ns) → µs |

**Divergence for Memory Pilot:** count the drops (`denied`) instead of silently filtering. Coverage must be part of the snapshot (MP-REQ-032).

pt's alternative for identity uses `PROC_PIDTBSDINFO` and documents *"exact, unlike `ps etime`"* (`collect/macos.rs:677-760`) — we take identity from `PROC_PIDTASKALLINFO` since it already returns the same start fields plus sizes in one call.

---

## 8. Permissions

| Capability | Needs | Evidence |
|---|---|---|
| `host_statistics64`, `sysctl`, `statfs` | normal user | sbh runs as LaunchAgent |
| `proc_listpids`, `proc_pidinfo` on own-user processes | normal user | sbh/ pt both |
| `proc_pidinfo` on other users' processes | often denied | both repos degrade |
| Full Disk Access | **not for memory telemetry**; only for reading protected files | sbh probes FDA only for file scanning |
| Root | not required for anything in our v1 scope | sbh ships a user-scope plist with empty entitlements |
| SIP disabled | never required | pt notes dtruss requires it; we use no dtrace |

---

## 9. Not present in either repo (explicit unknowns)

| Question | Status |
|---|---|
| `phys_footprint` / `task_info(TASK_VM_INFO)` | **absent in both** — no reference implementation exists for the metric Activity Monitor displays |
| Per-process compressed / swapped bytes | **absent in both** |
| Host CPU load (`host_statistics(HOST_CPU_LOAD_INFO)`, `processor_info`) | **absent in both** |
| `purgeable_count` | present in sbh's mirror struct, **never surfaced** |
| PT's `proc_samples` Parquet table | **schema defined, no production writer** — only tests write it |
| PT's macOS launchd plist | **spec doc only**, no generator in code |
| PT's macOS daemon memory/orphan triggers | **structurally cannot fire** — inputs read `/proc`, zero on darwin |
| sbh CPU history | **I/O history only**; per-process CPU exists per-sample but is not historised |
| sbh `docs/macos.md` claims on FSEvents / `Nice` | **stale** — the FSEvents backend exists and is selected; `Nice` is 5, not 19 |
| `socket_fdinfo` field offsets (sbh) | pinned only by a live-kernel test, **no documented ABI** |
| Behaviour of the dispatch source across sleep/wake | **unknown** — not handled explicitly in either repo |
| `kern.memorystatus_vm_pressure_level` stability across macOS 13–26 | **unknown** — inferred from one working tool |
| Purge (`memory_pressure(1)`) measured effect | **unknown** — neither implements it |
| Cost of `proc_pid_rusage` per PID at scale | **unknown** — sbh calls it only where it needs I/O data |
| Any public API to suppress the macOS OOM dialog | **not found** anywhere reviewed |

These become Phase 0 spikes (`00-product/roadmap.md`).

---

## 10. Reuse guidance summary (portable rules)

1. **Port the rev0 count (38) explicitly.** Declare our own 38-`integer_t` count for `host_statistics64`; never derive it from a binding's struct size. This is the single bug that silently kills all memory telemetry.
2. **Dispatch source preferred, sysctl level authoritative, heuristic fallback only.** Values 1/2/4; anything else ⇒ unavailable.
3. **`PROC_PIDTASKALLINFO`** for the combined start-time + RSS + VSZ + CPU read (one call), **`RUSAGE_INFO_V4`** for I/O; key history on `(pid, start_tvsec, start_tvusec)`.
4. **Never** use `PROC_PIDREGIONPATHINFO` flavor 8 for inventory (≈12 s for ~900 procs); flavor 22 only, and not at all in our scope.
5. **Never** spawn `lsof` per PID or re-parse `launchctl list` per PID (pt does both; they are prohibitive in a sampling loop).
6. **launchd:** copy the active generator (`Nice 5`, `ProcessType Standard`, `KeepAlive{Crashed}`, `ThrottleInterval 60`, no resource limits). Do not copy `Nice 19 + Background` — sbh measured that configuration starving the daemon at load ~700.
7. **Swap:** handle both textual and binary `vm.swapusage` (free precedes used in the binary layout); missing field ⇒ explicit unknown, never zero.
8. **Trust generator functions over prose** — both repos ship platform docs that have drifted from their code.

---

## 10. Summary for implementation

**Do:**
1. `host_statistics64` with our own struct + rev0 count.
2. `sysctl` for memsize, swap, and the kernel pressure level (masks 1/2/4).
3. Dispatch source for events, wrapped with a mandatory polling fallback.
4. `PROC_PIDTASKALLINFO` per PID on a slower loop; count denied PIDs.
5. `statfs` for disk headroom; treat purgeable as informational only.
6. LaunchAgent with `KeepAlive{Crashed}` + `ThrottleInterval`; never self-daemonize.

**Don't:**
1. Hardcode page size.
2. Shell out on the hot path (`ps`, `vm_stat`, `lsof`).
3. Treat swap as memory.
4. Conflate resident size with memory pressure contribution.
5. Require root or FDA.
6. Use private/undocumented structs.
