# Product Capability Matrix

**Status:** Draft
**Date:** 2026-10-23

The previous spec's implicit promise was "we detect and we tell you". This document makes the four capability levels explicit, per feature, so that no one — implementer or user — mistakes one for another.

---

## 1. The four levels

| Level | Definition | Evidence of success |
|---|---|---|
| **D — Detect** | the system observes and reports a true state | observation matches an independent measurement |
| **P — Prevent** | the system stops a *future* deterioration | the deterioration did not occur (counterfactual — see caveat) |
| **R — Remediate** | the system reduces *current* pressure | measured pressure fell after the action |
| **A — Advise** | the system helps a human decide | the human acted, or reported the advice as useful |

**Detect ≠ Prevent.** A warning that arrives before the dialog still leaves the user clicking Resume. Only P or R changes the outcome.

**A caveat on P:** prevention is inherently counterfactual. You cannot observe the crisis that did not happen. Claims of prevention must be stated as `prevented (counterfactual)` and never as a measured effect.

---

## 2. Capability matrix

| Feature | D | P | R | A | Notes |
|---|---|---|---|---|---|
| System pressure monitoring | ✅ | — | — | — | foundation for everything |
| Process inventory + coverage | ✅ | — | — | — | |
| Growth detection per identity | ✅ | — | — | — | |
| Risk forecasting (calibrated) | ✅ | — | — | — | enables T2 decisions |
| Cause classification | ✅ | — | — | — | chooses the action tier |
| **Admission control (T2)** | ✅ | ✅ | — | ✅ | **the strongest P** |
| **Session-close recommendation (T5)** | ✅ | ⚠️ | — | ✅ | P only when the user acts |
| **Orphan detection (T3 detect)** | ✅ | — | — | ✅ | |
| **Orphan recommendation (T3 recommend)** | ✅ | ⚠️ | — | ✅ | |
| Orphan termination (T3 act) | ✅ | — | ✅ | — | **off by default** |
| Self-trim (T1) | ✅ | — | ⚠️ | — | R only for our own footprint |
| Cooperative release (T4) | — | — | — | — | **empty: no API exists** |
| Suppressing the OS dialog | — | — | — | — | impossible, and wrong |
| Preventing a running process from growing | — | — | — | — | impossible without T3 |

**Reading the matrix:** the R column has almost nothing in it, and that is correct. A user-space daemon on macOS **cannot** remediate memory held by another process without either cooperation (which does not exist) or termination (which destroys work). The product's value is concentrated in **P (admission control)** and **A (advice)**.

---

## 3. What this means for the product promise

The honest product statement is:

> Memory Pilot prevents the situations it can prevent — by stopping work from starting that would not fit, and by telling you precisely what is consuming memory before the system forces the issue. It cannot free memory that another application is holding, and it will not kill your sessions to do it.

That is a narrower promise than "no more memory warnings", and it is the only one that survives contact with macOS.

---

## 4. Per root cause, the best available capability

| Root cause (from `problem-coverage-matrix.md`) | Best achievable level | Mechanism |
|---|---|---|
| RC-1 aggregate demand | **P** | T2 admission control |
| RC-2 idle sessions | **A**, ⚠️ P | T5 recommendation with resume path |
| RC-3 orphan accumulation | ⚠️ **R** | T3 (opt-in) |
| RC-4 single unbounded process | **A** only | precise naming; no remedy exists |
| RC-5 swap/disk | ⚠️ **P** | follows from T2 |
| RC-6 compression | **D** only | early signal, not actionable |
| RC-7 own footprint | **R** | T1 |
| RC-8 misdiagnosis | **D** quality | coverage + confidence |

**No row reaches R without T3.** That is the single most consequential fact in this table, and it is why T3's design (and its default-off posture) matters so much.

---

## 5. Feature → module → test

| Feature | Core module | Tier | Acceptance |
|---|---|---|---|
| pressure monitoring | A1 System Monitor | — | IT-1, T-PE-1..8 |
| process inventory | A2 Process Inventory | — | IT-4 |
| growth detection | A3 Growth Detector | — | T-AT-1..8 |
| forecasting | A4 Risk Forecaster | — | T-FC-1..11 |
| cause classification | A5 Cause Analyzer | — | SCN-001/002/003 |
| admission control | A6 + launch point | T2 | **SCN-007** |
| session recommendation | A6 + adapter | T5 | **SCN-008** |
| orphan detection | A2 + A5 | T3 detect | SCN-009 |
| orphan termination | — | T3 act | **not in v1** |
| self-trim | — | T1 | ST-19..21 |
| verification | A7 | — | T-AA-6 |
| core-without-adapters | architecture | — | **IT-11** |

---

## 6. The three questions a reviewer should ask of any new feature

1. **Which column does it fill?** If a feature claims P but cannot show a prevented allocation, it is D wearing a P label.
2. **Which tier does it belong to, and is that tier's default consistent with what it can break?** Anything that can destroy work is off by default.
3. **Does it work with zero adapters?** If not, it belongs in layer B, not the core.

---

## 7. Related

- `problem-coverage-matrix.md` — root causes and their coverage
- `system-protection-core.md` — the module structure
- `protection-actions.md` — tiers in detail
- `benchmark-protocol.md` — how each level is measured
