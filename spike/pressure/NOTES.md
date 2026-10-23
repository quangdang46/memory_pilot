# S-3: kernel pressure values under induced pressure

Target machine (Mac mini M4, Mac16,10, 16 GB, darwin 25.4.0). Controlled Go
allocator (`main.go`, CGO_ENABLED=0, purego sysctlbyname + host_statistics64),
sampling `kern.memorystatus_vm_pressure_level`, `kern.memorystatus_level`,
and free+speculative pages every 100 ms while ramping touched allocations in
512 MB chunks. Reproduce: `CGO_ENABLED=0 go build -o pressure main.go && ./pressure`.

## Observed value table (raw sysctl output)

| Induced load | kern.memorystatus_vm_pressure_level | kern.memorystatus_level | free+spec MB |
|---|---|---|---|
| baseline (idle, 7.1 GB swap already used) | 2 | 43–45 | 22–38 |
| +0.5 GB | 2 | 41 | 24 |
| +2 GB | 2 | 39–40 | 10 |
| +4 GB | 2 | 37 | 10 |
| +6 GB | 2 | 36 | 22 |
| +8 GB (run 1 & 2) | 2 | 36–37 | 10–22 |
| +10…12 GB (run 3, second process) | 2 | 35–36 | 90–103 |

**Distinct values observed across all runs: {2} only.**

## Decode verdict

- The sysctl returns the XNU `event.h` pressure **masks**: 1 = NORMAL,
  2 = WARN, 4 = CRITICAL — *not* a 0/1/2 scale. This is the decode Firefox
  (`AvailableMemoryWatcherMac.cpp`: "4 => critical, 2 => warning, 1 => normal
  (values from kernel event.h file)") and Chromium rely on, and matches
  newosxbook's `kern.memorystatus_vm_pressure_level` documentation.
  Verified: [apple.stackexchange](https://apple.stackexchange.com/questions/170227/how-is-the-y-value-for-the-memory-pressure-graph-in-activity-monitor-calculated), newosxbook.com "No pressure, Mon!".
- The observed value 2 throughout heavy induced pressure (7+ GB swap already
  in use, memorystatus_level floor 35) is consistent: the machine sits in the
  WARN band; 2 = WARN. A 0/1/2 decode would misread this state as "critical"
  and a 0/1/2 *normal* mapping would have shown 2 only if normal — refuted by
  `kern.memorystatus_level` simultaneously sitting in its warning band.
- **NORMAL (1) and CRITICAL (4) were not induced.** CRITICAL was targeted with
  three ramps up to +12 GB; the kernel held a ~90 MB free cushion, the elastic
  compressor absorbed the working set, and swap used actually fell during the
  heaviest run. Forcing further would enter jetsam territory for third-party
  apps on the owner's working machine — refused on safety grounds (SI-1
  spirit). The collector must treat any value ∉ {1,2,4} as UNKNOWN (never
  mapped to calm), which the decode above mandates.

## Additional observations

- `kern.memorystatus_level` is a separate 0–100 scale (observed 35–45); do not
  conflate it with the pressure masks.
- Sysctls absent on this build: `kern.memorystatus_vm_page_free_target`,
  `kern.memorystatus_available_pages` (unknown oid). Available-page counts come
  from `host_statistics64` instead.
- `purge` requires root — cannot be used unprivileged for NORMAL induction.
- Event cadence: dispatch events are wake-ups only; every event must be
  followed by a re-read of the sysctls (macos-integration.md). With S-2 closed
  NOT-APPLICABLE, v1 polls; cadence is a Phase 1/2 configuration, not a kernel
  guarantee measured here.

## Exit items

- ADR-001 decode half: VERIFIED for the mask decode (observed WARN=2 under
  load; 1/4 anchored by kernel event.h decode used by Firefox/Chromium, values
  ∈ {1,2,4}, unknown → fail-safe).
