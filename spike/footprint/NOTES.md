# S-5 + S-7: per-PID sampling cost at N=600, and the footprint source choice

Target Mac mini M4, darwin 25.4.0, CGO_ENABLED=0, purego into libSystem.
Program: `main.go`. Reproduce: `CGO_ENABLED=0 go build -o footprint main.go && ./footprint`.

## S-5 — per-PID sampling cost (N=600, 3 passes each, best)

| API | N=600 cycle | per-PID | share of one core at 5 s cadence |
|---|---|---|---|
| `proc_pidinfo(PROC_PIDTASKINFO)` | 1.12–1.49 ms | ~2.4–2.9 µs | ≈0.03 % |
| `proc_pid_rusage(RUSAGE_INFO_V4)` | 0.68 ms | ~1.1 µs | ≈0.014 % |

Live pids on the machine at measure time: 586 (the N=600 workload pads with
repeats). 403/586 readable in a single pass — pids exit between enumeration
and read; the collector must treat per-pid read failure as missing evidence,
never as zero (SI-4).

Performance-budget.md §budget arithmetic (0.6 % estimate at N=600, P=4, T=5 s)
was pessimistic by ~20×: measured cost is ≈0.03 %. The 5 s process cadence with
P=1 (every pid every cycle) fits the idle budget with two orders of magnitude
of headroom.

## S-7 — footprint source: DECISION: `proc_pid_rusage(RUSAGE_INFO_V4).ri_phys_footprint`

Measured the same instant on 389 readable pids:

- `PROC_PIDTASKINFO.pti_resident_size` vs `rusage_info_v4.ri_resident_size`:
  389/389 agree within 1 MB (avg |diff| ≈ 0 KB) — the two resident-size paths
  are interchangeable.
- phys_footprint vs resident_size: NOT interchangeable — footprint is the
  honest memory-burden number. Top examples: pid 7905 footprint 8629 MB vs
  resident 1205 MB (compressed/swap-backed pages included); sum(footprint) =
  28.5 GB vs sum(resident) = 8.5 GB across the system. Activity Monitor's
  "Memory" column is phys_footprint, so this is the source users cross-check
  against.
- Resident-size *deltas* would track paging behavior, not memory burden —
  wrong signal for attribution and leak suspicion.

ADR-002 exit evidence: choose `ri_phys_footprint` from `proc_pid_rusage`
RUSAGE_INFO_V4 (single cheap call, no task port, no cgo — see S-1), reported
alongside resident_size; RSS-only metrics are explicitly labeled as such (SI-5).

Also verified incidentally: `RUSAGE_INFO_V4` also carries
`ri_lifetime_max_phys_footprint` and `ri_interval_max_phys_footprint` — free
input for leak-suspicion windows without extra syscalls.
