# ADR-009 — History retention vs. detection horizon

**Status:** Proposed
**Date:** 2026-10-23
**Related:** MP-REQ-035, MP-REQ-061, `../03-algorithms/leak-suspicion.md`

---

## Context

Different consumers need different looks into the past:

| Consumer | Needs |
|---|---|
| Forecast (EWMA) | last 30 min, high resolution |
| Burst detection | last 30–60 min of rates |
| Attribution | last 10–60 min of per-process deltas |
| Leak suspicion (quiet window, plateau) | 20–60 min |
| Repeat-episode history | days to weeks, coarse |
| Post-incident explain | the incident window (hours) |
| Calibration | weeks of forecast outcomes |
| Long-term stats (`report --last 7d`) | 7 days, coarse |

---

## Decision

Three resolutions, three horizons, all within the 500 MB disk budget:

| Tier | Resolution | Retention | Content |
|---|---|---|---|
| Hot | 5–10 s (system), 10–30 s (process top-20) | 72 h | full series used by the algorithms |
| Warm | 1 min aggregates | 30 d | mean/min/max per series; group growth totals |
| Cold | 1 h aggregates | 1 y (rotated) | per-group growth, incident records, calibration records |

**Episode memory** (for repeat detection) lives in the warm tier as compact `episode` records: `{group, start, duration, growth_bytes, shape, classification, trigger}` — a handful of bytes each, so a year of episodes is trivial.

**Calibration records** live in the warm tier and are never aggregated away (they are the evidence that the forecast is honest).

---

## Evidence

Estimated volume: hot tier for 72 h ≈ 4 series × 26k points × ~64 B ≈ 7 MB; warm tier (30 d × 1440 min × ~20 groups) ≈ 0.5 M small records ≈ 30 MB; cold tier negligible. Total well under 500 MB. E1: confirm by measurement.

## Consequences

**+** Algorithms get the resolution they need; the archive stays small; explain can cover a year at coarse resolution.
**−** Coarse tiers cannot answer fine-grained questions about old incidents. Accepted: if an incident matters, the hot tier still covers it for 72 h, and `mempilot snapshot` can archive a full-resolution copy of a window on demand.

## Revisit triggers

Episode detection proves to need finer historical structure, or the disk budget is exceeded.
