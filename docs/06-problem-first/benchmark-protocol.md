# Benchmark Protocol — how we know mp actually helps

**Status:** Draft
**Date:** 2026-10-23
**Machine:** Mac mini M4, 16 GB, 256 GB, dedicated where possible

The previous spec measured whether the daemon *works*. This protocol measures whether the machine *stays up longer*. Those are different questions and the second one is the product.

---

## 1. The primary metric

> **Time-to-first-dialog, per unit of work.** How many hours of productive multi-session work elapse before macOS posts the out-of-memory dialog, compared to the same workload without mp.

Secondary: whether the dialog appears at all in a fixed-duration run.

**Why not "detection accuracy"?** A perfect detector that changes nothing scores perfectly and helps nobody. Accuracy is a diagnostic metric (§4), not the goal.

---

## 2. Workload profiles

Each profile is a scripted, repeatable workload. They must be representative of the owner's real usage, not of a synthetic ideal.

| ID | Profile | Shape |
|---|---|---|
| W1 | single session | one agent, one repo, continuous work |
| W2 | three concurrent | three agents, three repos, interleaved work |
| W3 | six concurrent | the realistic upper bound for 16 GB |
| W4 | long-running | three sessions over 8 h, mostly idle |
| W5 | growth case | one process with a controlled allocation leak (test binary) |
| W6 | orphan case | a parent spawns children and exits, repeatedly |
| W7 | burst | one large allocation at an unexpected moment |
| W8 | telemetry loss | W3 with the collector partially failing |

**Test instrumentation:** each synthetic process reports its own allocation behaviour so ground truth is known. Real agents are used for realism; synthetic ones for ground truth. A run is only valid if it uses both where appropriate.

---

## 3. Comparison design

For each profile, three arms:

| Arm | Configuration |
|---|---|
| **A — baseline** | no mp |
| **B — observe only** | mp running, all actions disabled |
| **C — full policy** | mp with the policy under test |

B is essential and is usually omitted. It separates *"mp helped"* from *"mp ran"*: if C ≈ B, the actions did nothing and only observation-time effects (sampling cost) are visible. If C ≈ A while B is worse than A, mp's own overhead is material.

**Repetition:** 5 runs per arm per profile. Report median and the full spread; a single run is an anecdote.

**Randomization:** interleave arms (A B C A B C …) rather than running all A then all C, so ambient conditions (heat, background updates) do not correlate with the arm.

---

## 4. Metrics

### 4.1 Outcome (the product question)

| Metric | Definition |
|---|---|
| `dialog_count` | number of macOS out-of-memory dialogs |
| `critical_seconds` | total time at kernel pressure critical |
| `paused_seconds` | total time any app is marked `(paused)` |
| `work_interrupted` | count of sessions that lost state or required restart |
| `time_to_first_dialog` | wall time from workload start to first dialog (or "never, within 8 h") |
| `system_latency_p95` | a fixed benchmark probe, measured during the run |

### 4.2 Mechanism (the diagnostic question)

| Metric | Definition |
|---|---|
| `detection_lead_time` | first signal → kernel warning, or → critical |
| `response_latency` | first signal → first protection action (or explicit no-action record) |
| `cause_accuracy` | classification vs. ground truth on synthetic profiles |
| `forecast_coverage` | empirical conformal coverage |
| `false_defer_rate` | admissions deferred that would have fit |
| `orphan_precision` | (if T3 exercised) recommended orphans that were truly orphaned |

### 4.3 Cost (the honesty question)

| Metric | Budget |
|---|---|
| own RSS | see `../05-testing/performance-budget.md` |
| own CPU | same |
| own disk | same |
| added latency on the system | p95 regression vs. arm A must be ≤ 2 % |

**A win that costs 5 % system latency is not a win.** Report the cost next to every outcome.

---

## 5. The harder question: attribution

If arm C shows fewer dialogs than arm A, **why?** Candidates:

1. mp's own footprint displaced enough work to matter (unlikely; it is small).
2. T2 prevented launches that would not have fit (real, but only in profiles where the harness uses a managed launch point).
3. T5 recommendations were followed by the harness (must be controlled — either the harness acts on them deterministically, or it ignores them; never mixed).
4. Randomness.

**Rule:** each run records whether the harness followed recommendations. A run where the harness followed advice is **not** evidence that mp alone prevents dialogs; it is evidence that advice plus a compliant operator does. Both are interesting, but they must not be reported as the same result.

| Reported as | Requires |
|---|---|
| `automatic prevention` | arm C with the harness ignoring all recommendations |
| `advised prevention` | arm C with the harness following recommendations |
| `observation effect` | arm B vs. arm A |

---

## 6. Acceptance thresholds (draft, to be revised with data)

| Claim | Threshold to make it |
|---|---|
| "reduces dialogs" | ≥ 50 % fewer dialogs on W3, median of 5 runs, with a matching reduction on at least one other profile |
| "extends stable operation" | median `time_to_first_dialog` at least 2× on W3 |
| "does not harm" | no profile with a ≥ 2 % p95 latency regression beyond noise |
| "detects early" | median `detection_lead_time` ≥ 5 min on W5/W7 |
| "acts in time" | median `response_latency` ≤ 1 tick + action time |

A claim that misses its threshold is reported as missed. **Reporting a loss is a result**; deleting the run is not.

---

## 7. What this protocol cannot establish

| Cannot | Because |
|---|---|
| prove a dialog was *prevented* | counterfactual; only statistical comparison over many runs |
| generalize beyond these profiles | the owner's real work will drift |
| validate T3 precision | requires real orphan events, which are rare and must be collected in production |
| measure long-term leak rates of third-party apps | that is the app vendors' data |

---

## 8. Run log

Every run writes `bench/results/<date>-<profile>-<arm>-<n>.json` containing: machine, macOS build, mp commit, profile script hash, metrics, recommendation-followed flag, anomalies. The narrative is written after, and it must include anything that went wrong.

---

## 9. Related

- `../05-testing/test-strategy.md` — the software-correctness tests
- `../05-testing/stress-and-soak.md` — endurance runs
- `product-capability-matrix.md` — which claims this protocol can support
