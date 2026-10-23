# ADR-010 — Fallback and degraded-mode triggers

**Status:** Proposed
**Date:** 2026-10-23
**Related:** `../04-safety/failure-recovery.md`, SI-4, SI-6, SI-7

---

## Context

"Fallback" is used loosely in most systems and ends up meaning "keep going but hope". Here it must be a specific set of conditions with specific, tested behavior.

---

## Decision

Two orthogonal degradation axes, never conflated:

**Axis 1 — Capability degradation** (what we can do): Normal → ReducedDetail → ReducedCadence → Minimal → Silent → Exit. Defined in `../04-safety/failure-recovery.md` §2. Triggered by faults, budget breaches, or host pressure.

**Axis 2 — Confidence degradation** (what we can claim): the forecast/attribution quality flags. Triggered by missing data, drift, low coverage.

A system can be fully capable but low-confidence (all sources fine, forecast drifting) or degraded-capable but high-confidence (reduced cadence, but the samples we do have are clean). Conflating them would produce either silent overclaiming or needless shutdowns.

### Trigger table

| Trigger | Axis 1 | Axis 2 |
|---|---|---|
| Metric unavailable | — | confidence ↓, `unknown` fields |
| Partial process coverage | — | attribution confidence ↓ |
| Forecast coverage below target | — | `forecast_degraded`; action gate closed |
| Worker panic | step down one level | — |
| Ledger write failure | step down | history claims marked incomplete |
| Own RSS > warn | step down | — |
| Own RSS > hard | exit 10 | — |
| Host pressure ≥ Prevent | step down | — |
| Config invalid at start | exit 3 | — |
| Schema major mismatch | exit 12 | — |
| Clock jump | — | estimator reset → `warming` |

### Recovery

Recovery is *stepwise and quiet-gated*: after `recovery_quiet` (60 s) with no new fault and one fully successful sample, step up one level. Never jump from Minimal to Normal.

---

## Consequences

**+** The user sees two independent signals: "how much is it watching" and "how much should I trust the watching".
**−** More states to test. Mitigated by a table-driven state machine test with a golden transition matrix.

## Revisit triggers

Operational experience shows the axes interact in practice in ways this table does not capture.
