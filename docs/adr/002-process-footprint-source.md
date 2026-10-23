# ADR-002 — Per-process memory metric source

**Status:** Verified (Phase 0 evidence: `spike/footprint/NOTES.md` S-5/S-7, 2026-10-23)
**Date:** 2026-10-23
**Related:** MP-REQ-006, MP-REQ-008, `../03-algorithms/telemetry-model.md` §3

---

## Context

Attribution needs a per-process memory number whose *deltas* are meaningful. Candidates:

| Source | Field | Cost | Meaning | Caveat |
|---|---|---|---|---|
| libproc task info | `proc_taskinfo.pti_resident_size` | 1 call incl. other fields | resident pages | shared pages counted per process; excludes compressed |
| libproc rusage v4 | `rusage_info_v4.ri_physical_footprint_bytes` | 1 extra call | Apple's "physical footprint" (what Activity Monitor shows) | additional syscall per PID |
| libproc rusage v4 | `ri_resident_size` | same call | resident | same caveat |
| Mach task_info | `MACH_TASK_BASIC_INFO.resident_size` | task port per PID, usually denied for other processes without entitlement | resident | not viable cross-process |
| `ps` parsing | `rss` column | fork+exec, slow, brittle | resident | rejected |

`[Verified: sbh]` sbh uses `proc_pidinfo(PROC_PIDTASKALLINFO)` for `pti_resident_size`/`pti_virtual_size` and separately reads `proc_pid_rusage` V4 for I/O fields; it filters out PIDs it cannot read. It does not use `ri_physical_footprint_bytes` for attribution.

---

## Options

**A. `pti_resident_size` only.** One call per PID, cheap, but RSS's shared-page inflation distorts group totals (a forked test runner can invent gigabytes).

**B. `ri_physical_footprint_bytes` only.** Best semantic match to "what this app costs", but doubles the per-PID syscall count; availability for other users' processes must be checked.

**C. Both**: resident as the cheap primary; footprint computed on a slower cadence for display and for high-stakes classification.

---

## Decision

**C is the recommendation**, subject to measurement:

- process sampling at cadence `T_proc` reads `PROC_PIDTASKALLINFO` (pid, ppid, name, start time, resident, virtual, CPU) — this is the baseline series used for all deltas;
- footprint (`ri_physical_footprint_bytes`) is read on a slower cadence `T_fp` (start: every 4th process sample) for the **top contributors only** (top 20 by delta), to bound cost;
- reports label which metric produced the number (`resident` vs `footprint`);
- deltas are computed only within a series of the same metric. Never subtract resident from footprint.

---

## Evidence

`[Verified: absence]` **Neither reference repo reads `phys_footprint`.** sbh uses `proc_pid_rusage` V4 for I/O fields only; pt uses `PROC_PIDTASKALLINFO`/`PROC_PIDTBSDINFO`. Neither calls `task_info(TASK_VM_INFO)`. So Option B has **no working reference implementation to learn from** — it is a from-first-principles spike, not a port. That materially raises its cost and moves the burden of proof onto the spike.

Required:

- **E1:** Does `ri_physical_footprint_bytes` exist and populate on the target macOS version? Read `<libproc.h>`'s `rusage_info_v4` layout directly; do not rely on a binding.
- **E2:** Is it populated for *other users'* processes? (Expect denied; must degrade to resident + reduced confidence.)
- **E3:** Does it agree with Activity Monitor's displayed "Memory" for the same process at the same instant (within a few %)? If it does not, the field does not mean what its name suggests and must not be labelled "footprint".
- **E4:** Cost of an extra `proc_pid_rusage` per PID at N=600.
- **E5:** On a fork-heavy workload, quantify shared-page inflation of RSS deltas vs footprint deltas — does C actually improve attribution accuracy?

**Fallback decision rule:** if E1 or E3 fails, adopt **Option A** (resident deltas only), keep the label `resident`, and strengthen the report caveats. Do not ship a metric whose meaning cannot be verified against Apple's own UI.

## Consequences

**+** Better attribution for the fork-heavy coding-agent workload; honest labels.
**−** Two metrics, two cadences, more code paths; a small risk of users comparing numbers that are not comparable (mitigated by labels).

## Revisit triggers

Cost exceeds budget (MP-REQ-071) or E3 shows no accuracy gain.
