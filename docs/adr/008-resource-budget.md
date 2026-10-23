# ADR-008 — Daemon resource budget

**Status:** Verified (Phase 0 evidence: `spike/daemon-idle/NOTES.md` S-4, 2026-10-23 — empty-daemon baseline 3–5 MB RSS, ≈0.003 % CPU on the target Mac mini M4; budgets have a measured denominator)
**Date:** 2026-10-23
**Related:** `../05-testing/performance-budget.md`, MP-REQ-070..074

---

## Context

On a 16 GB machine, a memory daemon that uses 300 MB is 1.9 % of RAM permanently spent on watching RAM. That is a bad trade if the product cannot prevent pressure; acceptable if it does. The budget must also *tighten* under pressure, because that is when the cost matters most.

sbh publishes RSS warn/hard limits (512 MiB / 1 GiB) and restarts itself at the hard limit (`self_monitor.rs`). Those numbers are for a disk scanner with a walker; ours must be an order of magnitude smaller.

---

## Decision

| Budget | Value | Enforcement |
|---|---|---|
| Idle RSS | ≤ 60 MB (target 35 MB) | shape of the implementation; measured in CI bench |
| Loaded RSS | ≤ 120 MB | same |
| Hard exit | 400 MB, 3 consecutive checks | exit code 10 (self-protective) |
| Idle CPU | ≤ 0.3 % | cadence + P-factor |
| Loaded CPU | ≤ 2 % | |
| Go soft limit | `debug.SetMemoryLimit(250 MB)` | GC pressure before the wall |
| Disk/day | ≤ 15 MB ledger | aggregation + retention |
| Disk total | ≤ 500 MB | retention sweep |

**Under host pressure**, budgets tighten: at `Prevent`, process sampling drops to every 15 s and FP sampling is disabled unless the state is `Critical`; at `Critical`, only the system loop runs at 1 s and the ledger keeps events + aggregated samples only.

**Consequence we accept:** during a crisis, attribution granularity is coarser. That is the right trade — the daemon must not compete for the resource it is measuring.

---

## Evidence

None. E1: idle RSS of the skeleton. E2: loaded RSS after 8 h. E3: does `SetMemoryLimit` actually keep RSS near the limit on darwin (it governs Go heap, not stacks/cgo)?

## Revisit triggers

E1/E2 exceed budget by > 50 % with no obvious waste — renegotiate the budget in this ADR rather than quietly exceeding it.
