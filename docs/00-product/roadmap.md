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
| S-1 | Can Go call `host_statistics64` + `proc_listpids` + `proc_pidinfo` without cgo? | `spike/mach/` + a note with the exact struct + count used (incl. the rev0-count-38 trap) |
| S-2 | cgo dispatch source: latency and cost | measurements, p50/p95 |
| S-3 | `kern.memorystatus_vm_pressure_level` values under induced pressure | observed value table |
| S-4 | Idle RSS/CPU of an empty daemon with ticker + JSONL writer | baseline numbers |
| S-5 | Per-PID sampling cost at N=600 | µs/pid, and the resulting CPU budget |
| S-6 | Does a purge-style request actually reduce pressure? | ADR-006 verdict with numbers |
| S-7 | Which per-process footprint source: `phys_footprint`, `RUSAGE_INFO_V4`, or `pti_resident_size` deltas? | measurement vs Activity Monitor + ADR-002 decision with the imprecision documented if (b) |

**Why S-7 exists:** ADR-002's exit evidence needs a measurement spike, and it was previously implicit. It is now numbered so the Phase 0 gate has a named owner.

**Exit gate:** ADR-001, ADR-002, ADR-004, ADR-008 updated to `Verified` or `Rejected`, each with evidence.

**Parallelism rule (binding):** `P1SKEL` (module tree, stubs, build gates ST-1..ST-4) may run during Phase 0 — the gates must exist before the code they guard. **Every other Phase 1 task is blocked by the spike it depends on** (`P1PRES`←S-1,S-2; `P1VM`←S-1; `P1PROC`←S-1,S-5,S-7; `P1SELF`←S-4; `P1LAUNCHD`←S-4). A 24-hour run against unverified platform reads proves the wrong thing.

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

Process identity, grouping, delta accounting, classification buckets.

**Exit gate:** scenario MP-SCN-005 and the `leak-like` / `workload-growth` / `cache-sawtooth` traces classify correctly; the phrase test passes; coverage reporting is present everywhere.

(`report`/`explain` renderers live in Phase 5 with the rest of the CLI surface — they consume attribution output but are built against stable CLI contracts, not against in-progress attribution work. Traceability: MP-REQ-062..064 belong to Phase 5 work items.)

---

## Phase 4 — Policy, executor, intervention tiers, safety surface

Policy engine, planner, executor, actionguard, and the providers within the ADR-013/ADR-014 v1 scope: T1 self-actions; T2 deferral via `mp run`; T3 detect+recommend only; **T4 cooperative-release framework (no default adapter)**; **L2 managed-stop (owned + pre-authorized + identity-revalidated)**; T5 advisories. Plus verification, escalation-ladder wiring, in-development safety runs, and the development-time exit gate G-P4.

**Exit gate:** the full safety suite is green — ST-22 unmodified, ST-23/ST-24 added — and the per-class breakers/budgets exist. No mechanism that frees *non-owned, non-cooperating* memory exists; T3 `act` still has no executor code path (ADR-012/ADR-013). The single signal call site is `stopOwnedChild` (ST-1), and a managed stop can only target an `OwnedPid`.

(`status`/`policy`/CLI commands are built in Phase 5; the Phase 4 gate needs a harness CLI, not the user-facing surface. The pre-release re-run of the full suite is `P6SAFETY` in Phase 6 — G-P4 proves the executor as built, P6SAFETY proves the product as shipped.)

---

## Phase 5 — CLI surface, reporting, session guidance

`status`/`report`/`explain`/`doctor`/`policy`, session lifecycle with resume awareness, orphan detect+recommend reporting, relief-candidate reporting, and the report-language honesty rules. Consumes Phase 2–4 outputs through stable contracts.

**Exit gate:** golden CLI output tests, exit-code namespace tests, SCN-008/009/010 green; no advisory promises recoverability without a verified adapter; no report presents self-trim as system relief.

---

## Phase 6 — Long-run validation, fault injection, and canary

Fault-injection suites, 72-hour soak, replay against the full corpus, 7-day observe-mode deployment on the owner's machine (MP-REQ-050 baseline vs. with-tool incident counts), calibration report from real data, and the canary-evidence gate for T4/L2 enforce promotion.

**Exit gate:** a written report with real numbers; the go/no-go for the enforce mode of any canary-gated class. The former "Phase 6 — first real provider" becomes: *any provider beyond the ADR-014 paths enters through the same v1-scope-contract §5 widening procedure (ADR amendment + safety suite + measured canary evidence), regardless of phase numbering.*

---

## Exit gates, one per phase (binding)

Each phase has exactly one gate bead; the epic does not close until its gate closes. The gate is the *proof of the phase-level claim*, not a duplicate of any component task.

| Phase | Gate | Proves |
|---|---|---|
| 0 | **G-P0** | S-1..S-7 recorded (or S-2 not-applicable with S-1's evidence); ADR-001/002/004/008 Verified-or-Rejected; measured constants replaced their P-tags |
| 1 | **G-P1** | 24 h on the target Mac: correct collection, within budget, no monotonic growth, clean restart |
| 2 | **G-P2** | forecast beats the naive baseline on the corpus, MP-REQ-020..026, nothing actionable without confidence |
| 3 | **G-P3** | SCN-005 + bucket correctness on the three traces, phrase test, coverage on every attribution path |
| 4 | **G-P4** | ST-1..ST-24 green on the executor as built (via the development ST run), ST-22 unmodified, T3 act absent |
| 5 | **G-P5** | golden CLI output, exit-code namespaces, SCN-008/009/010, honest-language rules |
| 6 | **G-P6** | pre-release replay of MP-SCN-001..011 on the final build, 7-day baseline report, canary decisions |
| spec | **G-FREEZE** | G1–G8 current, G7 script passes, G3 scope decided, owner freeze signed |

**Ordering rule inside Phase 0:** S-1 runs first (it decides whether cgo is needed). S-2 runs only if S-1 says a cgo bridge is required; otherwise S-2 closes not-applicable citing S-1. **S-3 is independent of S-2** — it reads sysctl values, not dispatch latency — and may run in parallel with S-1/S-2. S-4, S-5, and S-7 are mutually independent.

---

## Phase-numbering note (binding)

The implementation beads (`.beads/`) are the detailed build plan; this roadmap is the authority for **phase scope and exit gates**. Where they once disagreed — trace corpus/replay placement, CLI placement, the safety-suite's two runs (development G-P4 vs. pre-release P6SAFETY), and the S-7 footprint spike — the two now agree as written above and in the Phase 1 epic's parallelism rule. A future disagreement is a bug in one of the two, to be resolved by editing the stale one, not by interpreting around it.

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
