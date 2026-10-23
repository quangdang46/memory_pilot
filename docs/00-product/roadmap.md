# Implementation Roadmap

**Status:** Draft
**Version:** 0.1.0

The owner asked for a complete product, not a thin MVP. That does not mean writing every feature at once — it means the **architecture is complete from day one** and the build order follows technical dependency, not feature slicing. Nothing here is a throwaway prototype; each phase produces components that stay.

Each phase has an **entry condition** (what must be true before starting) and an **exit gate** (what proves it is done). A phase that cannot meet its gate does not proceed; the gate is changed only by an ADR.

---

## Phase 0 — Verification spikes (before any architecture is locked)

Purpose: replace `[Open]` with `[Measured]` for the decisions the design depends on.

| Spike | Question | Deliverable |
|---|---|---|
| S-1 | Can Go call `host_statistics64` + `proc_listpids` + `proc_pidinfo` without cgo? | `spike/mach/` + a note with the exact struct + count used |
| S-2 | cgo dispatch source: latency and cost | measurements, p50/p95 |
| S-3 | `kern.memorystatus_vm_pressure_level` values under induced pressure | observed value table |
| S-4 | Idle RSS/CPU of an empty daemon with ticker + JSONL writer | baseline numbers |
| S-5 | Per-PID sampling cost at N=600 | µs/pid, and the resulting CPU budget |
| S-6 | Does a purge-style request actually reduce pressure? | ADR-006 verdict with numbers |

**Exit gate:** ADR-001, ADR-002, ADR-004, ADR-008 updated to `Verified` or `Rejected`, each with evidence.

---

## Phase 1 — Skeleton & platform

| Component | Contents |
|---|---|
| repo layout | `cmd/{mempilot,mempilotd}`, `internal/...`, `Makefile`, CI |
| `internal/clock`, `model`, `store` | with contract tests |
| `platform` interfaces + `platform/macos` collector | system sample, process sample, pressure read |
| launchd | plist generator, install/uninstall |
| ledger | JSONL writer, rotation, crash tolerance, retention skeleton |
| `doctor` (partial) | platform probe, ledger writability, cross-check vs `vm_stat` |

**Exit gate:** daemon runs 24 h on the target Mac, collects correctly, budget met (MP-REQ-070), no leaks, clean restart.

---

## Phase 2 — Pressure model & forecast

Estimator, forecaster, calibration, burst detection + the replay harness and the first trace corpus.

**Exit gate:** on the trace corpus, T1/T2 forecasts beat the naive baseline and meet coverage targets; MP-REQ-020..026 pass; no output can be emitted without confidence.

---

## Phase 3 — Attribution & classification

Process identity, grouping, delta accounting, classification buckets, `report`/`explain` renderers.

**Exit gate:** scenario MP-SCN-005 and the `leak-like` / `workload-growth` / `cache-sawtooth` traces classify correctly; the phrase test passes; coverage reporting is present everywhere.

---

## Phase 4 — Policy, safety surface, CLI

Policy engine, planner, executor, actionguard, self-only providers, verification, IPC, `status`/`policy` commands.

**Exit gate:** the full safety suite is green, including ST-22 (the forbidden scenario). No relief beyond self-trimming exists.

---

## Phase 5 — Long-run validation

7-day observe-mode deployment on the owner's machine; baseline vs. with-tool incident counts (MP-REQ-050); calibration report from real data.

**Exit gate:** a written report with real numbers; the go/no-go for the enforce mode of any future provider.

---

## Phase 6 — (Conditional) first real provider

Only if Phase 0 S-6 or a later experiment produces a mechanism with a measured, repeatable, bounded effect.

**Exit gate:** canary evidence over ≥2 weeks with no adverse events.

---

## What is never a phase

- A kill button. (ADR-012)
- A "free memory" command. (NG-2)
- A GUI before the data model is proven. (NG-9)

---

## Sequencing rationale

```
P0 ──► P1 ──► P2 ──┬──► P3 ──► P4 ──► P5
                   │
                   └── (P2 needs the corpus; the corpus needs P1's recorder)
```

P2 before P3 because attribution's windows depend on the pressure model's cadence and state definitions. P4 last among the build phases because policy needs real attribution and forecast outputs to evaluate, and because the safety surface should be built once the shapes it guards are stable.

---

## Effort intuition (not a commitment)

| Phase | Relative effort |
|---|---|
| P0 spikes | small, high value |
| P1 skeleton | medium |
| P2 forecast + replay | large (the intellectual core) |
| P3 attribution | large |
| P4 policy/CLI | medium |
| P5 validation | calendar time, not effort |

The forecast and attribution phases carry the product's value; they should get the time that a naive plan would spend on UI.
