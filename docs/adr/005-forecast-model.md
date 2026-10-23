# ADR-005 — Forecast model complexity

**Status:** Proposed
**Date:** 2026-10-23
**Related:** `../03-algorithms/forecasting.md`, MP-REQ-021..026

---

## Context

There is a temptation to reach for a sophisticated model (ARIMA, state-space, gradient boosting, "AI"). sbh's experience is instructive: it uses EWMA + a quadratic projection + conformal bounds + a guardrail, and the complexity it added beyond that is in *calibration of trust*, not in the model itself.

The prevailing failure mode for forecasting tools is not under-fitting — it is confident nonsense. A quadratic projection is explainable ("you're filling at 12 MB/s, accelerating, so ~9 minutes"), testable against an analytic answer, and cheap.

---

## Decision

**Tiered model, each tier only after the previous one is validated:**

| Tier | Model | Gate to advance |
|---|---|---|
| T0 | Naive: current rate × distance | reference baseline only |
| T1 | EWMA rate + quadratic projection + jitter-based confidence | beats T0 on replay (lead time and false-alarm rate) |
| T2 | T1 + one-sided conformal lower bound + drift guardrail | coverage within tolerance on replay |
| T3 | T2 + burst/regime conditioning (different model params for burst vs. steady) | measurably better on `burst-*` traces without degrading steady traces |

Explicitly **not** in scope until T3 is proven: neural nets, per-process predictive models, multivariate state-space models.

**Rule:** no tier ships "because it is more sophisticated". A tier must show a measurable improvement on the recorded trace corpus, and the improvement must survive the false-positive budget.

---

## Evidence

None yet. Baseline metrics to report per tier:

- median absolute error of ETA (log-scale),
- lead time (how early the warning fired before the actual critical transition),
- false-positive rate (warnings that did not materialize within the forecast horizon),
- calibration coverage vs. target.

## Consequences

**+** Explainable, testable, cheap; degrades gracefully.
**−** May underperform a learned model on highly periodic workloads (e.g. a nightly build at 2 a.m.). If that pattern proves important, T3's regime conditioning is the first place to look — not a neural net.

## Revisit triggers

T3 fails to beat T2 on periodic workloads by a useful margin after the corpus includes real periodic traces.
