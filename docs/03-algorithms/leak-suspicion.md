# MP-ALG-005 — Anomaly & Leak Suspicion

**Status:** Draft
**Version:** 0.1.0

**The word "leak" is a conclusion. This product does not draw conclusions about other people's software.**

What we can do: observe growth patterns, compare them against workload activity and history, and report a *suspicion* with evidence, confidence, and the alternatives we could not exclude. That is a genuinely useful answer. "Chrome is leaking" is not.

---

## 1. Classification vocabulary

Every growth finding is classified into one of five buckets. Only one of them even mentions leaks, and it is named `leak_suspicion`, never `leak`.

| Bucket | Definition | Typical evidence |
|---|---|---|
| `workload_growth` | Growth correlates with observable work (CPU, subprocess spawns, I/O, file activity) and stops when the work stops | CPU/IO co-movement; subprocess count rose; growth halted after the task ended |
| `cache_or_retrievable` | Growth in a process whose known role is caching; may be reclaimed by the app itself; growth plateaus under a ceiling | plateau shape; known-cache process names; no pressure contribution while headroom is ample |
| `subprocess_accumulation` | Growth driven by an increasing *count* of processes rather than per-process growth; processes are not exiting | child count monotone increase; per-process footprint flat; parents still alive |
| `leak_suspicion` | Sustained growth that does **not** correlate with workload, does not plateau, and continues after the triggering activity ended | see §3 |
| `unknown` | Evidence insufficient, or conflicting | coverage too low, window too short, evidence contradictory |

A classification is a *label plus its basis*. The basis is mandatory.

---

## 2. The signals

### 2.1 Growth shape

For each identity/group, over a window, fit the simplest of:

| Shape | Signature | Interpretation |
|---|---|---|
| Flat | slope ≈ 0 | not a contributor |
| Plateau/ceiling | rises then asymptotes | cache-like; usually benign |
| Linear | constant positive slope | steady accumulation — needs correlation to be called anything |
| Accelerating | positive second derivative | concerning; investigate correlation carefully |
| Sawtooth | rises then drops repeatedly | expected for caches/GC; benign unless peaks grow |
| Step | single jump, flat after | allocation event; correlate with the timestamp |

Shape classification is done with **robust** statistics (median-based slope, Theil–Sen or repeated pairwise slopes) rather than OLS, because process samples contain spikes from GC cycles and shared-page changes.

### 2.2 Correlation with observed work

Compute co-movement between `delta_i` and each activity signal over the window:

- process CPU time delta,
- child process spawn count,
- disk read/write bytes (if available),
- "activity events" (session start, tool run) if we have them.

Use a simple, explainable measure: sign-agreement rate plus Spearman rank correlation on the sampled series. **Not** a p-value theatre — we report the coefficient and the sample count, and the policy uses thresholds.

If growth correlates with work ⇒ `workload_growth` unless there is strong counter-evidence.
If growth continues while all activity signals are flat for ≥ `quiet_window` (start: 10 min) ⇒ the growth is **unexplained**, which is the necessary condition for any suspicion.

### 2.3 Plateau test

`cache_or_retrievable` requires evidence of a ceiling: the series has stopped rising for ≥ `plateau_window` (start: 15 min) after prior growth, or its maximum over the last 24 h was reached earlier and not exceeded.

### 2.4 History and repetition

For each group, keep a compact history of past growth episodes: (start, duration, magnitude, shape, classification). A **repeat** of the same shape after the same trigger strengthens whichever classification matches; a repeat *without* the trigger strengthens suspicion.

This is the "learning memory" idea from pt, adapted: pt learns from user decisions; we learn from observed episode recurrence. We do not learn from user "kill/don't kill" decisions because we never ask.

---

## 3. `leak_suspicion` criteria

All of the following must hold:

1. **Sustained**: growth over ≥ `min_suspect_window` (start: 20 min) with no plateau.
2. **Uncorrelated**: no meaningful co-movement with CPU/IO/spawn activity over the same window, and activity has been at a low baseline for ≥ `quiet_window`.
3. **Not explained by known cache behaviour**: process/group is not in the cache-role table, or exceeds its historical ceiling by a large margin.
4. **Visible and measurable**: coverage adequate (`denied_fraction` low); we are not speculating about processes we cannot see.
5. **Reproducible in the record**: the ledger contains the full series, so a human can check.

If 1–5 hold, emit:

```jsonc
{
  "classification": "leak_suspicion",
  "confidence": 0.64,
  "basis": [
    "grew 1.8 GB over 47 min with no plateau",
    "CPU flat at 0.3% for the last 31 min",
    "no child process spawned in 29 min",
    "similar growth occurred 2× previously without matching activity"
  ],
  "alternative_explanations": [
    "internal cache with an undocumented ceiling",
    "allocator fragmentation rather than retention",
    "growth in memory mapped by the process but owned by a library"
  ],
  "what_would_change_our_mind": [
    "a plateau of ≥ 15 min",
    "CPU or I/O rising to match the growth rate",
    "the process exiting and memory not returning to the OS"
  ],
  "recommended_next_step": "run Instruments (Leaks / Allocations) against PID <n> if you want certainty"
}
```

The `what_would_change_our_mind` field is not decoration: it is how we keep the claim falsifiable, and it gives the user a concrete experiment.

### 3.1 What we never say

| Never | Why |
|---|---|
| "X is leaking" | not established by external observation |
| "X has a memory leak" in a notification title | same |
| A leak percentage | invented precision |
| A recommendation to restart the app | that is the user's call, and restarting loses work — the exact harm we exist to avoid |

We *may* say: "growth is unexplained by observed activity; if it continues, restarting that app when convenient may help, but you will lose its in-app state." Even this is a suggestion with the cost stated.

---

## 4. Anomaly detection (beyond per-group growth)

Three system-level anomalies worth detecting, all explainable:

| Anomaly | Detection | Why it matters |
|---|---|---|
| **Compressor runaway** | `compressor_pct` rising while free_pct falls and no single process accounts for it | indicates aggregate memory pressure, not one bad app |
| **Swap cliff** | `swap_rate` crosses a threshold and stays | thrash onset; latency about to degrade |
| **Pressure without growth** | kernel says warn/critical but no process grew materially | likely cache pressure / other users / kernel; tells us *not* to blame a process |

The third is especially important: it prevents the product from inventing a culprit when the truth is "the machine is simply full".

---

## 5. Thresholds and their status

All thresholds below are **starting values, explicitly unverified**. They must be derived from traces on the target machine before v1 (ADR-009).

| Parameter | Start value | Meaning |
|---|---|---|
| `quiet_window` | 10 min | activity baseline for "uncorrelated" |
| `min_suspect_window` | 20 min | minimum sustained growth |
| `plateau_window` | 15 min | flat duration that counts as a plateau |
| `min_growth_for_suspicion` | 200 MB | below this, not worth a claim |
| `corr_threshold` | 0.5 | Spearman ρ above which growth is "correlated with work" |
| `denied_fraction_max` | 0.2 | above this, classification is `unknown` |

---

## 6. Tests

| Test | Proves |
|---|---|
| T-LK-1 | Synthetic leak (linear, no CPU) ⇒ `leak_suspicion` |
| T-LK-2 | Same growth with matching CPU ⇒ `workload_growth` |
| T-LK-3 | Cache-like plateau ⇒ `cache_or_retrievable` |
| T-LK-4 | Rising child count with flat per-process ⇒ `subprocess_accumulation` |
| T-LK-5 | Low coverage ⇒ `unknown`, not a suspicion |
| T-LK-6 | Schema rejects any output lacking `alternative_explanations` |
| T-LK-7 | Renderer cannot produce the phrase "is leaking" from any classification (string-level test) |
| T-LK-8 | Repeat-episode history changes confidence in the expected direction |
| T-LK-9 | Replay of a real recorded incident reproduces the same classification |

---

## 7. Honesty rules

1. Every classification names the bucket *and* the basis.
2. `unknown` is a first-class, frequently correct answer.
3. If we cannot see enough processes, we say so rather than classifying anyway.
4. Every suspicion lists what evidence would refute it.
5. No language that implies certainty about another vendor's code.
